package handlers_test

import (
	"bytes"
	"testing"
)

func TestConnectionHistoryHTTPDestinationProtocolAndStatusFilters(t *testing.T) {
	// Given: different requested hosts, wildcard characters, protocols and states.
	f := historyFixture(t)
	f.enable()
	first := f.event(1, "flow_end", 1, 19, 31)
	second := f.event(2, "flow_update", 2, 41, 71)
	second.Protocol = historyPtr("trojan")
	second.Destination.RequestedHost = historyPtr("Target_%literal.example.test")
	third := f.event(3, "flow_end", 3, 97, 113)
	third.Destination.RequestedHost = historyPtr("Target-10.example.test")
	f.ingest(f.batch(first, second, third), 200)
	// When: exact/prefix host, protocol and state filters are sent over HTTP.
	exact := f.page("?destination=Target-1.example.test&protocol=vless&status=ended")
	prefix := f.page("?destination_prefix=Target-1")
	literal := f.page("?destination_prefix=Target_%25&protocol=trojan&status=observing")
	// Then: matching is literal, case-preserving and does not resolve destinations.
	if len(exact.Items) != 1 || *exact.Items[0].Event.FlowID != *first.FlowID {
		t.Fatalf("exact=%+v", exact)
	}
	if len(prefix.Items) != 2 {
		t.Fatalf("prefix=%+v", prefix)
	}
	if len(literal.Items) != 1 || *literal.Items[0].Event.FlowID != *second.FlowID {
		t.Fatalf("escaped prefix=%+v", literal)
	}
	if len(f.page("?destination=target-1.example.test").Items) != 0 {
		t.Fatal("requested host spelling was normalized")
	}
	for _, query := range []string{"?protocol=unsupported", "?status=online", "?destination=a&destination_prefix=b"} {
		body := f.request("GET", "/api/v1/admin/connection-history/flows"+query, nil, "", 400)
		if !bytes.Contains(body, []byte(`"error":`)) {
			t.Fatalf("filter error=%s", body)
		}
	}
	total := f.request("GET", "/api/v1/admin/connection-history/aggregate?destination=Target-1.example.test&protocol=vless&status=ended", nil, "", 200)
	if !bytes.Contains(total, []byte(`"upload":"19"`)) {
		t.Fatalf("aggregate filters=%s", total)
	}
}
