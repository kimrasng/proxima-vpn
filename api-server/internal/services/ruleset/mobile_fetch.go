package ruleset

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Reject special-purpose address space, including ranges considered global
// unicast by netip but unsuitable as public provider destinations. IPv6 is
// restricted to allocated global unicast, excluding transition mechanisms.
var mobileSpecialNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func mobilePublicIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range mobileSpecialNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func mobileHTTPSURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("mobile provider requires public HTTPS on port 443 without credentials or fragments")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !mobilePublicIP(ip) {
		return nil, fmt.Errorf("mobile provider address %s is not public", ip)
	}
	return u, nil
}

func mobileRedirect(req *http.Request, via []*http.Request) error {
	u, err := mobileHTTPSURL(req.URL.String())
	if err != nil {
		return err
	}
	if len(via) == 0 || len(via) > 5 || !strings.EqualFold(u.Hostname(), via[0].URL.Hostname()) {
		return fmt.Errorf("mobile provider permits at most 5 redirects on the original HTTPS origin")
	}
	return nil
}

type mobilePublicDialer struct {
	lookup func(context.Context, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func (d mobilePublicDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, fmt.Errorf("mobile provider dial requires port 443")
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = d.lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve mobile provider: %w", err)
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("mobile provider has no addresses")
	}
	// Validate the whole DNS answer before connecting, not just the first IP.
	for _, ip := range addresses {
		if !mobilePublicIP(ip) {
			return nil, fmt.Errorf("mobile provider resolved to non-public address %s", ip)
		}
	}
	// Pass numeric IPs, never the original hostname, to the actual dialer. TLS
	// still verifies the original request hostname through net/http.Transport.
	var lastErr error
	for _, ip := range addresses {
		conn, err := d.dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("connect mobile provider: %w", lastErr)
}

func newMobileHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: fetchTimeout}
	public := mobilePublicDialer{
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		dial: dialer.DialContext,
	}
	transport := &http.Transport{
		// Proxy remains nil: environment proxy settings must never bypass the
		// public-address dialer. No custom TLS dialer or certificate bypass.
		DialContext: public.DialContext, TLSHandshakeTimeout: fetchTimeout,
		ResponseHeaderTimeout: fetchTimeout, DisableKeepAlives: true,
	}
	return &http.Client{Transport: mobileTransportLimit{base: transport}, Timeout: fetchTimeout, CheckRedirect: mobileRedirect}
}

func fetchMobileProvider(ctx context.Context, rawURL, format string) (Contents, error) {
	return fetchMobileWithClient(ctx, newMobileHTTPClient(), mobileFetchSource{rawURL, format})
}

type mobileFetchSource struct{ url, format string }

func fetchMobileWithClient(ctx context.Context, client *http.Client, source mobileFetchSource) (Contents, error) {
	u, err := mobileHTTPSURL(source.url)
	if err != nil {
		return Contents{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Contents{}, err
	}
	req.Header.Set("User-Agent", "proxima-vpn-mobile")
	res, err := client.Do(req)
	if err != nil {
		return Contents{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return Contents{}, fmt.Errorf("mobile provider download: %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxMobileRemoteBytes+1))
	if err != nil {
		return Contents{}, err
	}
	if len(data) > maxMobileRemoteBytes {
		return Contents{}, fmt.Errorf("remote provider exceeds iOS limit of %d bytes", maxMobileRemoteBytes)
	}
	return Decode(data, source.format)
}
