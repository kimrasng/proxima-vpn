package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

// RequestBandwidthPermit consumes a single global device budget before bytes
// leave this Exit. It deliberately has no grant cache or unlimited fallback.
func (c *APIClient) RequestBandwidthPermit(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
	var result devicebandwidth.PermitResponse
	if uuid == "" || size <= 0 || size > 65535 || (direction != devicebandwidth.Upload && direction != devicebandwidth.Download) {
		return result, fmt.Errorf("invalid bandwidth permit request")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, err := json.Marshal(devicebandwidth.PermitRequest{DeviceUUID: uuid, Direction: direction, Bytes: size})
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/nodes/%s/bandwidth/permit", c.serverURL, c.nodeID), bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Key", c.apiKey)
	// Never forward the node credential through a redirect, including a same-
	// host downgrade. Reuse the configured transport but not redirect policy.
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("request bandwidth permit: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("bandwidth permit rejected (status %d)", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		return result, fmt.Errorf("decode bandwidth permit: %w", err)
	}
	if result.Allowed && result.DeniedReason != "" {
		return result, fmt.Errorf("invalid permit response")
	}
	if !result.Allowed && result.DeniedReason != "" && result.DeniedReason != "concurrency_limit" {
		return result, fmt.Errorf("unexpected permit denial")
	}
	if !result.Allowed && result.DeniedReason == "" && (result.RetryAfterMS < 1 || result.RetryAfterMS > 1000) {
		return result, fmt.Errorf("invalid bandwidth retry interval")
	}
	return result, nil
}
