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

var ErrAddressCapacity = errors.New("online address limit reached")

// AdmitSource admits a server-configured direct source cohort, never a client
// identity header. Relay original-source attribution is not supported here.
func (c *APIClient) AdmitSource(ctx context.Context, deviceUUID, source string) error {
	if source == "" {
		return errors.New("source admission requires an address")
	}
	return c.admit(ctx, deviceUUID, source)
}

// AdmitDevice reserves a slot before SOCKS announces a successful connection.
func (c *APIClient) AdmitDevice(ctx context.Context, deviceUUID string) error {
	return c.admit(ctx, deviceUUID, "")
}

func (c *APIClient) admit(ctx context.Context, deviceUUID, source string) error {
	device, err := uuid.Parse(deviceUUID)
	if err != nil {
		return fmt.Errorf("invalid UUID admission")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	payload := map[string]string{"device_uuid": device.String()}
	if source != "" {
		payload["source_address"] = source
	}
	body, _ := json.Marshal(payload)
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
		if source != "" {
			return ErrAddressCapacity
		}
		return ErrUUIDCapacity
	}
	return fmt.Errorf("UUID admission failed (status %d)", resp.StatusCode)
}
