package server

import (
	"errors"
	"io"
	"log"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/apernet/quic-go"
	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// History failures never replace the existing forwarding/enforcement result.
func historyError(err error) {
	if err != nil {
		log.Printf("connection history: %v", err)
	}
}

func historyPeer(addr string) *nodeprov.HistoryPeer {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil
	}
	return &nodeprov.HistoryPeer{IP: ap.Addr().Unmap().String(), Port: int(ap.Port())}
}

func historyEffectiveHost(addr string) *string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	}
	return &host
}

func historyFlow(o *connectionhistory.Observer, t *connectionhistory.Tunnel, addr, transport string, response bool) *connectionhistory.Flow {
	if o == nil || t == nil {
		return nil
	}
	info, err := historyFlowInfo(t, addr, transport, response)
	if err != nil {
		historyError(err)
		return nil
	}
	f, err := o.NewFlow(info)
	historyError(err)
	return f
}

// This constructor is pure so scoped UDP callers can use it under the observer
// gate. Errors are logged only after releasing that gate.
func historyFlowInfo(t *connectionhistory.Tunnel, addr, transport string, response bool) (connectionhistory.FlowInfo, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return connectionhistory.FlowInfo{}, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return connectionhistory.FlowInfo{}, err
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	}
	d := nodeprov.HistoryDestination{Transport: transport, RequestedHost: &host, RequestedPort: &port}
	quality, scope := "exact", "proxy_flow"
	if transport == "udp" {
		scope = "activity_window"
	}
	if response {
		d.RequestedHost, d.RequestedPort = nil, nil
		if p := historyPeer(addr); p != nil {
			d.ObservedIP, d.ObservedPort = &p.IP, &p.Port
		}
		quality = "observed"
	}
	return connectionhistory.FlowInfo{
		Tunnel: t, Destination: d, DestinationQuality: quality, Scope: scope,
	}, nil
}

func historyEnd(err error, fallback string) connectionhistory.End {
	reason := fallback
	var timeout net.Error
	var app *quic.ApplicationError
	var stream *quic.StreamError
	switch {
	case err == nil, errors.Is(err, io.EOF):
	case errors.Is(err, errDisconnect):
		reason = "policy_reject"
	case errors.As(err, &timeout) && timeout.Timeout():
		reason = "idle_timeout"
	case errors.As(err, &app) && app.ErrorCode == closeErrCodeOK:
		reason = "transport_close"
	case errors.As(err, &stream) && stream.ErrorCode == 0:
		reason = "eof"
	case fallback == "dial_error":
	default:
		reason = "io_error"
	}
	return connectionhistory.End{Reason: reason, Semantics: "local_owner_closed"}
}

// Close the existing I/O on first completion, then join the other direction.
// Unlike io.Copy this always observes the source, including (n > 0, err != nil).
// TrafficLogger still sees exactly its old reads and its return value still
// controls enforcement; its counters are not replaced by history counters.
func copyTwoWayHistory(id string, serverRw, remoteRw io.ReadWriter, l TrafficLogger, stats *StreamStats, f *connectionhistory.Flow, stop func()) error {
	results := make(chan error, 2)
	go func() {
		results <- copyBufferLog(serverRw, remoteRw, func(n uint64) bool {
			historyError(f.Observe(0, n))
			if l == nil {
				return true
			}
			stats.LastActiveTime.Store(time.Now())
			stats.Rx.Add(n)
			return l.LogTraffic(id, 0, n)
		})
	}()
	go func() {
		results <- copyBufferLog(remoteRw, serverRw, func(n uint64) bool {
			historyError(f.Observe(n, 0))
			if l == nil {
				return true
			}
			stats.LastActiveTime.Store(time.Now())
			stats.Tx.Add(n)
			return l.LogTraffic(id, n, 0)
		})
	}()
	first := <-results
	stop()
	second := <-results
	// A racing enforcement rejection must not be hidden by an earlier EOF.
	if errors.Is(first, errDisconnect) || errors.Is(second, errDisconnect) {
		return errDisconnect
	}
	return first
}
