package client

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/netip"
	"time"
)

var ErrUUIDCapacity = errors.New("online UUID limit reached")

var ErrAddressCapacity = errors.New("online address limit reached")
var ErrSessionCapacity = errors.New("online session limit reached")

// AdmitSession carries a server-generated exact transport token, not a client
// installation identifier. The control plane requires fresh Exit evidence.
func (c *APIClient) AdmitSession(ctx context.Context, deviceUUID, token string) error {
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != token {
		return errors.New("invalid session token")
	}
	err = c.admitPayload(ctx, deviceUUID, map[string]any{"session_token": token}, false)
	if errors.Is(err, ErrUUIDCapacity) {
		return ErrSessionCapacity
	}
	return err
}

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
	payload := map[string]any{}
	if source != "" {
		payload["source_address"] = source
	}
	return c.admitPayload(ctx, deviceUUID, payload, source != "")
}

// AdmitRelayPeer sends an observed Exit transport tuple; the server resolves
// its original source from eligible Entry reports rather than trusting a
// caller-provided original address.
func (c *APIClient) AdmitRelayPeer(ctx context.Context, deviceUUID, peerIP string, peerPort, localPort int) error {
	ip, err := netip.ParseAddr(peerIP)
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || peerPort < 1 || peerPort > 65535 || localPort < 1 || localPort > 65535 {
		return errors.New("invalid relay admission tuple")
	}
	return c.admitPayload(ctx, deviceUUID, map[string]any{"relay_peer": map[string]any{"ip": ip.Unmap().String(), "port": peerPort, "local_port": localPort}}, true)
}

func (c *APIClient) admitPayload(ctx context.Context, deviceUUID string, payload map[string]any, addressAdmission bool) error {
	device, err := uuid.Parse(deviceUUID)
	if err != nil {
		return fmt.Errorf("invalid UUID admission")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	payload["device_uuid"] = device.String()
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
		if addressAdmission {
			return ErrAddressCapacity
		}
		return ErrUUIDCapacity
	}
	return fmt.Errorf("UUID admission failed (status %d)", resp.StatusCode)
}
