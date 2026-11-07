package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

var errReadiness = errors.New("readiness failed")

type Readiness interface {
	Wait(context.Context, string) error
}

type httpReadiness struct{ client *http.Client }

func newReadiness() httpReadiness {
	return httpReadiness{&http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
			DisableKeepAlives: true, ResponseHeaderTimeout: 2 * time.Second, MaxResponseHeaderBytes: 8192},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (h httpReadiness) probe(ctx context.Context, service string) error {
	url := "http://127.0.0.1:2053/ready"
	if service == "web" {
		url = "http://127.0.0.1:8080/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return errReadiness
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(b) > 65536 || resp.StatusCode != http.StatusOK {
		return errReadiness
	}
	if service == "api" {
		var health struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(b, &health) != nil || health.Status != "ok" {
			return errReadiness
		}
	} else if len(b) == 0 {
		return errReadiness
	}
	return nil
}

func (h httpReadiness) Wait(ctx context.Context, service string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := h.probe(ctx, service); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errReadiness
		case <-ticker.C:
		}
	}
}
