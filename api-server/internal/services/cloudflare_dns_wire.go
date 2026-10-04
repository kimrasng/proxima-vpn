package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const cloudflareDNSMaxBody = 1 << 20

type CloudflareDNSErrorKind string

const (
	CloudflareDNSTransient CloudflareDNSErrorKind = "transient"
	CloudflareDNSDurable   CloudflareDNSErrorKind = "durable"
)

// CloudflareDNSError contains only machine-readable, non-secret upstream data.
type CloudflareDNSError struct {
	Operation  string
	Status     int
	Code       int
	Kind       CloudflareDNSErrorKind
	RetryAfter *time.Duration
}

func (e *CloudflareDNSError) Error() string {
	return fmt.Sprintf("cloudflare dns %s: %s (HTTP %d, code %d)", e.Operation, e.Kind, e.Status, e.Code)
}

type cloudflareDNSEnvelope struct {
	Success *bool `json:"success"`
	Errors  []struct {
		Code int `json:"code"`
	} `json:"errors"`
	Messages []struct {
		Code int `json:"code"`
	} `json:"messages"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type CloudflareDNSClient struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

func NewCloudflareDNSClient(token string) *CloudflareDNSClient {
	return &CloudflareDNSClient{
		token:      token,
		baseURL:    "https://api.cloudflare.com/client/v4",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *CloudflareDNSClient) request(ctx context.Context, operation, method, path string, body []byte) (cloudflareDNSEnvelope, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, strings.TrimRight(c.baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return cloudflareDNSEnvelope{}, &CloudflareDNSError{Operation: operation, Kind: CloudflareDNSTransient}
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := *c.httpClient
	client.Timeout = 10 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return cloudflareDNSEnvelope{}, &CloudflareDNSError{Operation: operation, Kind: CloudflareDNSTransient}
	}
	defer response.Body.Close()

	apiErr := &CloudflareDNSError{Operation: operation, Status: response.StatusCode, Kind: CloudflareDNSDurable}
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		apiErr.Kind = CloudflareDNSTransient
	}
	if seconds, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/int64(time.Second)) {
		delay := time.Duration(seconds) * time.Second
		apiErr.RetryAfter = &delay
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, cloudflareDNSMaxBody+1))
	if err != nil || len(data) > cloudflareDNSMaxBody {
		apiErr.Kind = CloudflareDNSTransient
		return cloudflareDNSEnvelope{}, apiErr
	}
	var envelope cloudflareDNSEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Success == nil {
		apiErr.Kind = CloudflareDNSTransient
		return cloudflareDNSEnvelope{}, apiErr
	}
	if len(envelope.Errors) > 0 {
		apiErr.Code = envelope.Errors[0].Code
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !*envelope.Success {
		return cloudflareDNSEnvelope{}, apiErr
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		apiErr.Kind = CloudflareDNSTransient
		return cloudflareDNSEnvelope{}, apiErr
	}
	return envelope, nil
}
