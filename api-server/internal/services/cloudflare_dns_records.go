package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type CloudflareDNSRecord struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

func cloudflareDNSPath(zoneID string) string {
	return "/zones/" + url.PathEscape(zoneID) + "/dns_records"
}

func (c *CloudflareDNSClient) ListExact(ctx context.Context, zoneID, hostname string) ([]CloudflareDNSRecord, error) {
	return c.listExact(ctx, zoneID, hostname, true)
}

func (c *CloudflareDNSClient) ListExactAllTypes(ctx context.Context, zoneID, hostname string) ([]CloudflareDNSRecord, error) {
	return c.listExact(ctx, zoneID, hostname, false)
}

func (c *CloudflareDNSClient) listExact(ctx context.Context, zoneID, hostname string, aOnly bool) ([]CloudflareDNSRecord, error) {
	path := cloudflareDNSPath(zoneID)
	q := url.Values{"name.exact": {hostname}, "match": {"all"}}
	if aOnly {
		q.Set("type", "A")
	}
	var records []CloudflareDNSRecord
	for page := 1; ; page++ {
		q.Set("page", strconv.Itoa(page))
		envelope, err := c.request(ctx, "list", http.MethodGet, path+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if envelope.ResultInfo == nil || envelope.ResultInfo.Page != page || (envelope.ResultInfo.TotalPages < page && !(page == 1 && envelope.ResultInfo.TotalPages == 0)) {
			return nil, &CloudflareDNSError{Operation: "list", Status: http.StatusOK, Kind: CloudflareDNSTransient}
		}
		var batch []CloudflareDNSRecord
		if err := json.Unmarshal(envelope.Result, &batch); err != nil {
			return nil, &CloudflareDNSError{Operation: "list", Status: http.StatusOK, Kind: CloudflareDNSTransient}
		}
		if envelope.ResultInfo.TotalPages == 0 {
			if len(batch) != 0 {
				return nil, &CloudflareDNSError{Operation: "list", Status: http.StatusOK, Kind: CloudflareDNSTransient}
			}
			return records, nil
		}
		// Do not trust a remote filter to expand discovery beyond the requested A name.
		for _, record := range batch {
			if (record.Name == hostname || (!aOnly && strings.EqualFold(strings.TrimSuffix(record.Name, "."), hostname))) && (!aOnly || record.Type == "A") {
				records = append(records, record)
			}
		}
		if page == envelope.ResultInfo.TotalPages {
			return records, nil
		}
	}
}

type cloudflareDNSWrite struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

func (c *CloudflareDNSClient) write(ctx context.Context, operation, method, path string, record cloudflareDNSWrite) (CloudflareDNSRecord, error) {
	body, err := json.Marshal(record)
	if err != nil {
		return CloudflareDNSRecord{}, &CloudflareDNSError{Operation: operation, Kind: CloudflareDNSDurable}
	}
	envelope, err := c.request(ctx, operation, method, path, body)
	if err != nil {
		return CloudflareDNSRecord{}, err
	}
	var result CloudflareDNSRecord
	if err := json.Unmarshal(envelope.Result, &result); err != nil || result.ID == "" {
		return CloudflareDNSRecord{}, &CloudflareDNSError{Operation: operation, Status: http.StatusOK, Kind: CloudflareDNSTransient}
	}
	return result, nil
}

func (c *CloudflareDNSClient) Create(ctx context.Context, zoneID, name, content, comment string) (CloudflareDNSRecord, error) {
	return c.write(ctx, "create", http.MethodPost, cloudflareDNSPath(zoneID), cloudflareDNSWrite{
		Name: name, Type: "A", Content: content, TTL: 300, Proxied: false, Comment: comment,
	})
}

func (c *CloudflareDNSClient) Update(ctx context.Context, zoneID, recordID, name, content, comment string) (CloudflareDNSRecord, error) {
	return c.write(ctx, "update", http.MethodPut, cloudflareDNSPath(zoneID)+"/"+url.PathEscape(recordID), cloudflareDNSWrite{
		Name: name, Type: "A", Content: content, TTL: 300, Proxied: false, Comment: comment,
	})
}

func (c *CloudflareDNSClient) Delete(ctx context.Context, zoneID, recordID string) error {
	_, err := c.request(ctx, "delete", http.MethodDelete, cloudflareDNSPath(zoneID)+"/"+url.PathEscape(recordID), nil)
	return err
}
