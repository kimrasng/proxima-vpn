package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"time"
)

var ErrUUIDCapacity = errors.New("online UUID limit reached")

// AdmitDevice reserves a slot before SOCKS announces a successful connection.
func (c *APIClient) AdmitDevice(ctx context.Context, deviceUUID string) error {
	device, err := uuid.Parse(deviceUUID)
	if err != nil {
		return fmt.Errorf("invalid UUID admission")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"device_uuid": device.String()})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/nodes/%s/devices/admit", c.serverURL, c.nodeID), bytes.NewReader(body))
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
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusConflict {
		return ErrUUIDCapacity
	}
	return fmt.Errorf("UUID admission failed (status %d)", resp.StatusCode)
}
