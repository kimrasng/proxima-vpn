package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

const ConnectionHistoryMaxBatch = 1 << 20

type ConnectionHistoryConfig struct {
	SchemaVersion   int                       `json:"schema_version"`
	Enabled         bool                      `json:"enabled"`
	Revision        nodeprov.HistoryU64       `json:"revision"`
	CollectionEpoch *nodeprov.HistoryUUID     `json:"collection_epoch"`
	Bindings        []nodeprov.HistoryBinding `json:"bindings"`
}

func (c ConnectionHistoryConfig) Apply() nodeprov.HistoryApply {
	return nodeprov.HistoryApply{Op: "apply", Enabled: c.Enabled, ConfigRevision: c.Revision,
		CollectionEpoch: c.CollectionEpoch, Bindings: c.Bindings}
}

func (c ConnectionHistoryConfig) Validate() error {
	if c.SchemaVersion != 1 || c.Bindings == nil {
		return fmt.Errorf("invalid history config")
	}
	line, err := json.Marshal(c.Apply())
	if err != nil {
		return err
	}
	_, err = nodeprov.DecodeHistoryControl(append(line, '\n'))
	return err
}

type ConnectionHistoryBatch struct {
	SchemaVersion int                     `json:"schema_version"`
	BatchID       nodeprov.HistoryUUID    `json:"batch_id"`
	PayloadSHA256 string                  `json:"payload_sha256"`
	Events        []nodeprov.HistoryEvent `json:"events"`
}

type ConnectionHistoryACK struct {
	BatchID       nodeprov.HistoryUUID `json:"batch_id"`
	PayloadSHA256 string               `json:"payload_sha256"`
	Status        string               `json:"status"`
}

func (a ConnectionHistoryACK) Verify(b ConnectionHistoryBatch) error {
	if a.BatchID != b.BatchID || a.PayloadSHA256 != b.PayloadSHA256 ||
		(a.Status != "committed" && a.Status != "expired") {
		return fmt.Errorf("history receipt does not acknowledge immutable batch")
	}
	return nil
}

func (c *APIClient) historyRequest(ctx context.Context, method, suffix string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method,
		fmt.Sprintf("%s/api/v1/nodes/%s/connection-history%s", c.serverURL, c.nodeID, suffix), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Node-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("history request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, ConnectionHistoryMaxBatch+1))
	if err != nil {
		return nil, err
	}
	if len(data) > ConnectionHistoryMaxBatch {
		return nil, fmt.Errorf("history response exceeds limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("history request status %d", resp.StatusCode)
	}
	return data, nil
}

func (c *APIClient) GetConnectionHistoryConfig(ctx context.Context) (ConnectionHistoryConfig, error) {
	var cfg ConnectionHistoryConfig
	data, err := c.historyRequest(ctx, http.MethodGet, "/config", nil)
	if err != nil {
		return cfg, err
	}
	// Canonical decoding rejects duplicate keys and invalid UTF-8 as well.
	data, err = nodeprov.CanonicalHistoryJSON(data)
	if err != nil {
		return cfg, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return cfg, err
	}
	for _, name := range []string{"schema_version", "enabled", "revision", "collection_epoch", "bindings"} {
		value, ok := fields[name]
		if !ok || (name != "collection_epoch" && bytes.Equal(value, []byte("null"))) {
			return cfg, fmt.Errorf("missing or null history config field %s", name)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func (c *APIClient) UploadConnectionHistory(ctx context.Context, body []byte) (ConnectionHistoryACK, error) {
	var ack ConnectionHistoryACK
	if len(body) > ConnectionHistoryMaxBatch {
		return ack, fmt.Errorf("history batch exceeds limit")
	}
	var batch ConnectionHistoryBatch
	if err := json.Unmarshal(body, &batch); err != nil {
		return ack, err
	}
	if batch.SchemaVersion != 1 || batch.BatchID.Validate() != nil || len(batch.Events) == 0 || len(batch.Events) > 1000 {
		return ack, fmt.Errorf("invalid history batch")
	}
	for _, event := range batch.Events {
		if err := event.Validate(); err != nil {
			return ack, err
		}
	}
	hash, err := nodeprov.HistoryEventsDigest(batch.Events)
	if err != nil || hash != batch.PayloadSHA256 {
		return ack, fmt.Errorf("invalid history payload digest")
	}
	data, err := c.historyRequest(ctx, http.MethodPost, "", body)
	if err != nil {
		return ack, err
	}
	data, err = nodeprov.CanonicalHistoryJSON(data)
	if err != nil {
		return ack, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ack); err != nil {
		return ack, err
	}
	return ack, ack.Verify(batch)
}
