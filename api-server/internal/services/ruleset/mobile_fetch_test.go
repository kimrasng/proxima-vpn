package ruleset

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestMobilePublicIP(t *testing.T) {
	for _, raw := range []string{"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254", "172.16.0.1", "192.168.1.1", "192.0.0.8", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "255.255.255.255", "::", "::1", "fc00::1", "fe80::1", "ff02::1", "2001:db8::1", "2001::1", "2002::1", "3fff::1", "64:ff9b::7f00:1", "::ffff:127.0.0.1"} {
		if mobilePublicIP(netip.MustParseAddr(raw)) {
			t.Errorf("accepted special IP %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !mobilePublicIP(netip.MustParseAddr(raw)) {
			t.Errorf("rejected public IP %s", raw)
		}
	}
}

func TestMobileDialPinsDNSAndRejectsMixedAnswers(t *testing.T) {
	lookups, calls := 0, 0
	dialErr := errors.New("test dial")
	d := mobilePublicDialer{
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			lookups++
			if lookups == 1 {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
		},
		dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
			calls++
			if address != "8.8.8.8:443" {
				t.Fatalf("unpinned dial: %s", address)
			}
			return nil, dialErr
		},
	}
	if _, err := d.DialContext(context.Background(), "tcp", "custom.example:443"); !errors.Is(err, dialErr) {
		t.Fatalf("unexpected first error: %v", err)
	}
	if _, err := d.DialContext(context.Background(), "tcp", "custom.example:443"); err == nil || !strings.Contains(err.Error(), "non-public") || calls != 1 || lookups != 2 {
		t.Fatalf("rebinding/mixed answer accepted: %v calls=%d lookups=%d", err, calls, lookups)
	}
}

func TestMobileRedirectPolicy(t *testing.T) {
	for _, tc := range []struct {
		target string
		hops   int
		ok     bool
	}{
		{"https://custom.example/b", 5, true},
		{"https://CUSTOM.example:443/b", 1, true},
		{"https://custom.example/b", 6, false},
		{"https://other.example/b", 1, false},
		{"http://custom.example/b", 1, false},
		{"https://custom.example:8443/b", 1, false},
		{"https://user@custom.example/b", 1, false},
		{"https://127.0.0.1/b", 1, false},
	} {
		u, err := url.Parse(tc.target)
		if err != nil {
			t.Fatal(err)
		}
		original, err := http.NewRequest(http.MethodGet, "https://custom.example/a", nil)
		if err != nil {
			t.Fatal(err)
		}
		via := make([]*http.Request, tc.hops)
		for i := range via {
			via[i] = original
		}
		if err := mobileRedirect(&http.Request{URL: u}, via); (err == nil) != tc.ok {
			t.Errorf("%s hops=%d: %v", tc.target, tc.hops, err)
		}
	}
}

func TestMobileCustomHTTPSFetchAndRedirects(t *testing.T) {
	client := newMobileHTTPClient()
	transport := client.Transport.(mobileTransportLimit).base.(*http.Transport)
	if transport.Proxy != nil || transport.DialTLSContext != nil || transport.TLSClientConfig != nil {
		t.Fatal("transport bypasses public dial or TLS verification")
	}
	client.Transport = mobileTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/start" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"/rules.list"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("+.custom.example\n")), Request: req}, nil
	})
	got, err := fetchMobileWithClient(context.Background(), client, mobileFetchSource{"https://custom.example/start", "text"})
	if err != nil || len(got.Entries) != 1 || got.Entries[0] != "+.custom.example" {
		t.Fatalf("custom HTTPS result: %+v %v", got, err)
	}
}
