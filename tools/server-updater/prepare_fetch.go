package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

const releaseDownloadBase = "https://github.com/kimrasng/proxima-vpn/releases/download/"

var releaseTagPattern = regexp.MustCompile(`^v[0-9][A-Za-z0-9._-]{0,126}$`)
var releaseAssetPathPattern = regexp.MustCompile(`^/github-production-release-asset/[0-9]+/[A-Za-z0-9_-]+$`)
var errReleaseDownload = errors.New("release download rejected or unavailable")

func newReleaseHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  10 * time.Second,
			MaxResponseHeaderBytes: 16 * 1024,
			DisableCompression:     true,
			DisableKeepAlives:      true,
		},
		CheckRedirect: releaseRedirect,
	}
}

func releaseRedirect(req *http.Request, via []*http.Request) error {
	u := req.URL
	if len(via) != 1 || req.Method != http.MethodGet || u.Scheme != "https" ||
		u.Host != "release-assets.githubusercontent.com" || u.User != nil || u.Fragment != "" ||
		u.RawPath != "" || u.Opaque != "" || !releaseAssetPathPattern.MatchString(u.Path) {
		return errReleaseDownload
	}
	// Only a redirect originating at our exact repository/asset path is valid.
	first := via[0].URL
	if first.Scheme != "https" || first.Host != "github.com" || first.User != nil ||
		first.RawQuery != "" || first.Fragment != "" || first.RawPath != "" {
		return errReleaseDownload
	}
	const prefix = "/kimrasng/proxima-vpn/releases/download/"
	const suffix = "/release-envelope.json"
	path := first.Path
	if len(path) <= len(prefix)+len(suffix) || path[:len(prefix)] != prefix ||
		path[len(path)-len(suffix):] != suffix || !releaseTagPattern.MatchString(path[len(prefix):len(path)-len(suffix)]) {
		return errReleaseDownload
	}
	// Signed CDN query parameters are preserved, never logged. No credentials,
	// cookies or Referer are forwarded even if the client implementation changes.
	req.Header = http.Header{"Accept": {"application/octet-stream"}, "Accept-Encoding": {"identity"}}
	return nil
}

func fetchRelease(ctx context.Context, client *http.Client, tag string) ([]byte, error) {
	if !releaseTagPattern.MatchString(tag) {
		return nil, errInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseDownloadBase+tag+"/release-envelope.json", nil)
	if err != nil {
		return nil, errReleaseDownload
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("Accept-Encoding", "identity")
	res, err := client.Do(req)
	if err != nil {
		// url.Error includes the CDN signed URL: do not return it to logs.
		return nil, errReleaseDownload
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Encoding") != "" || res.ContentLength > releaseverify.MaxEnvelopeSize {
		return nil, errReleaseDownload
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, releaseverify.MaxEnvelopeSize+1))
	if err != nil || len(data) > releaseverify.MaxEnvelopeSize {
		return nil, errReleaseDownload
	}
	return data, nil
}
