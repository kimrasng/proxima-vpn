package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"time"
)

type Revocation struct {
	UUID  string `json:"uuid"`
	Epoch string `json:"epoch"`
}

type RevocationSnapshot struct {
	RevokedUUIDs []string     `json:"revoked_uuids"`
	Revocations  []Revocation `json:"revocations"`
}

// GetRevokedDevices obtains the current node-scoped revocation set. Never use
// an error response to infer an empty set or restore a banned credential.
func (c *APIClient) GetRevokedDevices(ctx context.Context) (RevocationSnapshot, error) {
	var result RevocationSnapshot
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/nodes/%s/revoked-devices", c.serverURL, c.nodeID), nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("X-Node-Key", c.apiKey)
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return result, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return result, fmt.Errorf("revocation status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return result, err
	}
	if result.RevokedUUIDs == nil {
		return result, fmt.Errorf("revocation snapshot missing revoked_uuids")
	}
	for _, id := range result.RevokedUUIDs {
		if _, err := uuid.Parse(id); err != nil {
			return RevocationSnapshot{}, fmt.Errorf("invalid revocation UUID")
		}
	}
	for _, revocation := range result.Revocations {
		if _, err := uuid.Parse(revocation.UUID); err != nil {
			return RevocationSnapshot{}, fmt.Errorf("invalid eviction UUID")
		}
		if _, err := uuid.Parse(revocation.Epoch); err != nil {
			return RevocationSnapshot{}, fmt.Errorf("invalid eviction epoch")
		}
		found := false
		for _, id := range result.RevokedUUIDs {
			if id == revocation.UUID {
				found = true
				break
			}
		}
		if !found {
			return RevocationSnapshot{}, fmt.Errorf("eviction missing from revoked_uuids")
		}
	}
	return result, nil
}

// AcknowledgeRevocation reports local closure for the authenticated node only.
func (c *APIClient) AcknowledgeRevocation(ctx context.Context, revocation Revocation) error {
	body, err := json.Marshal(revocation)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/nodes/%s/revoked-devices/ack", c.serverURL, c.nodeID), bytes.NewReader(body))
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
		return fmt.Errorf("revocation ack status %d", resp.StatusCode)
	}
	return nil
}
