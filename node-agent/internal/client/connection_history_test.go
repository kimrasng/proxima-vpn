package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestConnectionHistoryConfig_RejectsMalformedWireObjects(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":1,"enabled":false,"revision":"0","collection_epoch":null,"bindings":[],"extra":true}`,
		`{"schema_version":1,"enabled":false,"enabled":true,"revision":"0","collection_epoch":null,"bindings":[]}`,
		`{"schema_version":1,"enabled":false,"revision":0,"collection_epoch":null,"bindings":[]}`,
		`{"schema_version":1,"revision":"0","collection_epoch":null,"bindings":[]}`,
		`{"schema_version":1,"enabled":null,"revision":"0","collection_epoch":null,"bindings":[]}`,
		`{"schema_version":1,"enabled":true,"revision":"1","collection_epoch":null,"bindings":[]}`,
	} {
		t.Run(body, func(t *testing.T) {
			// Given a real HTTP response with malformed desired policy.
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, body)
			}))
			defer s.Close()
			c := &APIClient{serverURL: s.URL, nodeID: "node", apiKey: "private", httpClient: s.Client()}
			// When decoding the independent history endpoint.
			_, err := c.GetConnectionHistoryConfig(context.Background())
			// Then invalid policy cannot replace the previously applied state.
			if err == nil {
				t.Fatal("malformed history config accepted")
			}
		})
	}
}

func TestConnectionHistory_RefusesRedirectWithoutForwardingNodeCredential(t *testing.T) {
	// Given a redirect to another real HTTP server.
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(true)
		json.NewEncoder(w).Encode(ConnectionHistoryConfig{SchemaVersion: 1})
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c := &APIClient{serverURL: redirect.URL, nodeID: "node", apiKey: "private", httpClient: redirect.Client()}
	// When fetching history policy.
	_, err := c.GetConnectionHistoryConfig(context.Background())
	// Then redirects fail rather than forwarding a custom credential header.
	if err == nil || leaked.Load() {
		t.Fatalf("history credential followed redirect: %v", err)
	}
}

func TestConnectionHistoryACK_OnlyMatchingCommittedOrExpiredReceiptsSucceed(t *testing.T) {
	// Given one immutable batch identity.
	b := ConnectionHistoryBatch{BatchID: "11111111-1111-4111-8111-111111111111", PayloadSHA256: "digest"}
	for _, status := range []string{"committed", "expired", "queued", ""} {
		t.Run(status, func(t *testing.T) {
			// When verifying a server receipt.
			a := ConnectionHistoryACK{BatchID: b.BatchID, PayloadSHA256: b.PayloadSHA256, Status: status}
			err := a.Verify(b)
			// Then only durable or expired matching receipts permit reclamation.
			if (err == nil) != (status == "committed" || status == "expired") {
				t.Fatalf("incorrect receipt disposition: %q %v", status, err)
			}
			a.BatchID = "22222222-2222-4222-8222-222222222222"
			if a.Verify(b) == nil {
				t.Fatal("wrong batch ID acknowledged")
			}
		})
	}
}
