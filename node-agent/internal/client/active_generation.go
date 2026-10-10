package client

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"time"
)

func (c *APIClient) BeginActiveUUIDGeneration(ctx context.Context, generation string) error {
	if _, err := uuid.Parse(generation); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/nodes/%s/active-uuids/generation", c.serverURL, c.nodeID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Node-Key", c.apiKey)
	req.Header.Set("X-Report-Generation", generation)
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("start admitted generation status %d", resp.StatusCode)
	}
	return nil
}
