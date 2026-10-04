package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestCloudflareDNSListExact_whenMultiplePages(t *testing.T) {
	// Given
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/client/v4/zones/zone/dns_records" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		q := r.URL.Query()
		if q.Get("name.exact") != "vpn.example.com" || q.Get("type") != "A" || q.Get("match") != "all" {
			t.Errorf("unexpected filters: %v", q)
		}
		pages = append(pages, q.Get("page"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"errors":[],"messages":[],"result":[{"id":"id%s","name":"vpn.example.com","type":"A","content":"192.0.2.%s","ttl":300,"proxied":false,"comment":"owned"}],"result_info":{"page":%s,"per_page":1,"count":1,"total_count":2,"total_pages":2}}`, q.Get("page"), q.Get("page"), q.Get("page"))
	}))
	defer srv.Close()
	c := NewCloudflareDNSClient("secret")
	c.baseURL = srv.URL + "/client/v4"
	c.httpClient = srv.Client()

	// When
	records, err := c.ListExact(context.Background(), "zone", "vpn.example.com")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pages, []string{"1", "2"}) || len(records) != 2 {
		t.Fatalf("pages=%v records=%+v", pages, records)
	}
	if records[1] != (CloudflareDNSRecord{ID: "id2", Name: "vpn.example.com", Type: "A", Content: "192.0.2.2", TTL: 300, Proxied: false, Comment: "owned"}) {
		t.Fatalf("record fields lost: %+v", records[1])
	}
}

func TestCloudflareDNSListExact_whenNoRecords(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"errors":[],"messages":[],"result":[],"result_info":{"page":1,"per_page":100,"count":0,"total_count":0,"total_pages":0}}`)
	}))
	defer srv.Close()
	c := NewCloudflareDNSClient("secret")
	c.baseURL = srv.URL
	c.httpClient = srv.Client()

	// When
	records, err := c.ListExact(context.Background(), "zone", "absent.example.com")

	// Then
	if err != nil || len(records) != 0 {
		t.Fatalf("empty lookup: records=%v err=%v", records, err)
	}
}

func TestCloudflareDNSListExactAllTypes_whenForeignOnLaterPage(t *testing.T) {
	// Given a provider that paginates mixed types at the exact name.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("type") != "" || q.Get("name.exact") != "vpn.example.com" {
			t.Errorf("unexpected filter: %v", q)
		}
		typ := "A"
		if q.Get("page") == "2" {
			typ = "CNAME"
		}
		fmt.Fprintf(w, `{"success":true,"result":[{"id":"id%s","name":"vpn.example.com","type":"%s"}],"result_info":{"page":%s,"total_pages":2}}`, q.Get("page"), typ, q.Get("page"))
	}))
	defer srv.Close()
	c := NewCloudflareDNSClient("secret")
	c.baseURL, c.httpClient = srv.URL, srv.Client()

	// When
	records, err := c.ListExactAllTypes(t.Context(), "zone", "vpn.example.com")

	// Then
	if err != nil || len(records) != 2 || records[1].Type != "CNAME" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestCloudflareDNSListExactAllTypes_normalizesProviderNames(t *testing.T) {
	// Given an exact-name result with DNS case and root-dot spelling.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"result":[{"id":"foreign","name":"VPN.Example.COM.","type":"NS"}],"result_info":{"page":1,"total_pages":1}}`)
	}))
	defer srv.Close()
	c := NewCloudflareDNSClient("secret")
	c.baseURL, c.httpClient = srv.URL, srv.Client()
	// When
	records, err := c.ListExactAllTypes(t.Context(), "zone", "vpn.example.com")
	// Then
	if err != nil || len(records) != 1 || records[0].ID != "foreign" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestCloudflareDNSWrites_whenCreatingUpdatingAndDeleting(t *testing.T) {
	// Given
	tests := []struct {
		name, method, path string
		invoke             func(*CloudflareDNSClient) error
		body               bool
	}{
		{"create", http.MethodPost, "/client/v4/zones/zone/dns_records", func(c *CloudflareDNSClient) error {
			_, err := c.Create(context.Background(), "zone", "vpn.example.com", "192.0.2.1", "owned")
			return err
		}, true},
		{"update", http.MethodPut, "/client/v4/zones/zone/dns_records/record", func(c *CloudflareDNSClient) error {
			_, err := c.Update(context.Background(), "zone", "record", "vpn.example.com", "192.0.2.1", "owned")
			return err
		}, true},
		{"delete", http.MethodDelete, "/client/v4/zones/zone/dns_records/record", func(c *CloudflareDNSClient) error {
			return c.Delete(context.Background(), "zone", "record")
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path || r.Header.Get("Authorization") != "Bearer secret" {
					t.Errorf("unexpected request: %s %s %s", r.Method, r.URL, r.Header.Get("Authorization"))
				}
				if tt.body {
					if r.Header.Get("Content-Type") != "application/json" {
						t.Error("missing JSON content type")
					}
					var body CloudflareDNSRecord
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Name != "vpn.example.com" || body.Content != "192.0.2.1" || body.Type != "A" || body.TTL != 300 || body.Proxied || body.Comment != "owned" {
						t.Errorf("wrong full record body: %+v", body)
					}
				}
				fmt.Fprint(w, `{"success":true,"errors":[],"messages":[],"result":{"id":"record","name":"vpn.example.com","type":"A","content":"192.0.2.1","ttl":300,"proxied":false,"comment":"owned"}}`)
			}))
			defer srv.Close()
			c := NewCloudflareDNSClient("secret")
			c.baseURL = srv.URL + "/client/v4"
			c.httpClient = srv.Client()

			// When
			err := tt.invoke(c)

			// Then
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
