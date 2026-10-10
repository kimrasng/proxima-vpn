package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"time"
)

type ActiveUUIDReport struct {
	Generation string   `json:"generation"`
	Sequence   int64    `json:"sequence"`
	UUIDs      []string `json:"uuids"`
}

// ReportActiveUUIDs replaces only this Exit's admitted, still-open egress set.
func (c *APIClient) ReportActiveUUIDs(ctx context.Context, report ActiveUUIDReport) error {
	if _, err := uuid.Parse(report.Generation); err != nil || report.Sequence <= 0 || report.UUIDs == nil {
		return fmt.Errorf("invalid active UUID report")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/nodes/%s/active-uuids", c.serverURL, c.nodeID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Node-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("active UUID report status %d", resp.StatusCode)
	}
	return nil
}
