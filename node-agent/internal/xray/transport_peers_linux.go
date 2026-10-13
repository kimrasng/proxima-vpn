//go:build linux

package xray

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"golang.org/x/sys/unix"
)

const (
	transportDiagMessage = 20 // SOCK_DIAG_BY_FAMILY
	transportDiagInfo    = 2  // INET_DIAG_INFO
	transportDiagSize    = 72 // struct inet_diag_msg
	transportMaxBytes    = 64 << 20
	transportMaxPeers    = 262144
)

// TransportPeers reads established TCP sockets in the current network namespace.
// Only exact local-port matches are returned; loopback endpoints are excluded.
// The dump is observational, not atomic, and does not establish socket ownership.
// Any incomplete dump or missing TCP_INFO on a matching socket fails the call.
func TransportPeers(ports []int) ([]TransportPeer, error) {
	set, err := transportPortSet(ports)
	if err != nil {
		return nil, err
	}
	peers := make([]TransportPeer, 0)
	if len(set) == 0 {
		return peers, nil
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return nil, fmt.Errorf("transport peers: netlink socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, fmt.Errorf("transport peers: netlink bind: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 1<<20)
	total := 0
	for i, family := range []byte{unix.AF_INET, unix.AF_INET6} {
		seq := uint32(i + 1)
		request := make([]byte, 16+56) // nlmsghdr + inet_diag_req_v2
		binary.NativeEndian.PutUint32(request[0:4], uint32(len(request)))
		binary.NativeEndian.PutUint16(request[4:6], transportDiagMessage)
		binary.NativeEndian.PutUint16(request[6:8], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
		binary.NativeEndian.PutUint32(request[8:12], seq)
		request[16] = family
		request[17] = unix.IPPROTO_TCP
		request[18] = 1 << (transportDiagInfo - 1)
		binary.NativeEndian.PutUint32(request[20:24], 1<<1) // TCP_ESTABLISHED
		// INET_DIAG_NOCOOKIE: no particular socket cookie is requested.
		binary.NativeEndian.PutUint32(request[64:68], ^uint32(0))
		binary.NativeEndian.PutUint32(request[68:72], ^uint32(0))
		if err := unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
			return nil, fmt.Errorf("transport peers: netlink request: %w", err)
		}
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil, errors.New("transport peers: netlink dump deadline exceeded")
			}
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			n, err := unix.Poll(poll, int((remaining+time.Millisecond-1)/time.Millisecond))
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("transport peers: netlink poll: %w", err)
			}
			if n == 0 {
				continue
			}
			if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
				return nil, errors.New("transport peers: netlink poll failure")
			}
			n, _, flags, from, err := unix.Recvmsg(fd, buf, nil, 0)
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("transport peers: netlink receive: %w", err)
			}
			sender, ok := from.(*unix.SockaddrNetlink)
			if !ok || sender.Pid != 0 || flags&unix.MSG_TRUNC != 0 || n == 0 {
				return nil, errors.New("transport peers: invalid or truncated netlink datagram")
			}
			total += n
			if total > transportMaxBytes {
				return nil, errors.New("transport peers: netlink dump exceeds byte limit")
			}
			batch, done, err := parseTransportDump(buf[:n], seq, family, set)
			if err != nil {
				return nil, err
			}
			if len(peers)+len(batch) > transportMaxPeers {
				return nil, errors.New("transport peers: netlink dump exceeds peer limit")
			}
			peers = append(peers, batch...)
			if done {
				break
			}
		}
	}
	return peers, nil
}

func transportAlign(n int) int { return (n + 3) &^ 3 }

func parseTransportDump(data []byte, seq uint32, family byte, ports map[uint16]bool) ([]TransportPeer, bool, error) {
	peers := make([]TransportPeer, 0)
	done := false
	for len(data) > 0 {
		if done || len(data) < 16 {
			return nil, false, errors.New("transport peers: truncated or trailing netlink header")
		}
		length := uint64(binary.NativeEndian.Uint32(data[:4]))
		if length < 16 || length > uint64(len(data)) || length > transportMaxBytes {
			return nil, false, errors.New("transport peers: invalid netlink length")
		}
		n := int(length)
		flags := binary.NativeEndian.Uint16(data[6:8])
		if binary.NativeEndian.Uint32(data[8:12]) != seq || flags&unix.NLM_F_DUMP_INTR != 0 {
			return nil, false, errors.New("transport peers: unexpected sequence or interrupted dump")
		}
		payload := data[16:n]
		switch binary.NativeEndian.Uint16(data[4:6]) {
		case unix.NLMSG_DONE:
			if len(payload) != 0 && len(payload) != 4 {
				return nil, false, errors.New("transport peers: malformed dump completion")
			}
			if len(payload) == 4 && binary.NativeEndian.Uint32(payload) != 0 {
				return nil, false, fmt.Errorf("transport peers: dump completion error %d", int32(binary.NativeEndian.Uint32(payload)))
			}
			done = true
		case unix.NLMSG_ERROR:
			if len(payload) < 20 {
				return nil, false, errors.New("transport peers: truncated netlink error")
			}
			code := int32(binary.NativeEndian.Uint32(payload))
			if code != 0 {
				return nil, false, fmt.Errorf("transport peers: netlink error %d", code)
			}
		case transportDiagMessage:
			peer, keep, err := parseTransportDiag(payload, family, ports)
			if err != nil {
				return nil, false, err
			}
			if keep {
				peers = append(peers, peer)
			}
		default:
			return nil, false, errors.New("transport peers: unexpected netlink message type")
		}
		aligned := transportAlign(n)
		if aligned > len(data) {
			return nil, false, errors.New("transport peers: truncated netlink padding")
		}
		data = data[aligned:]
	}
	return peers, done, nil
}

func parseTransportDiag(data []byte, family byte, ports map[uint16]bool) (TransportPeer, bool, error) {
	var peer TransportPeer
	if len(data) < transportDiagSize || data[0] != family || (family != unix.AF_INET && family != unix.AF_INET6) || data[1] != 1 {
		return peer, false, errors.New("transport peers: malformed established inet_diag message")
	}
	localPort := binary.BigEndian.Uint16(data[4:6])
	remotePort := binary.BigEndian.Uint16(data[6:8])
	var local, remote netip.Addr
	if family == unix.AF_INET {
		local = netip.AddrFrom4([4]byte(data[8:12]))
		remote = netip.AddrFrom4([4]byte(data[24:28]))
	} else {
		local = netip.AddrFrom16([16]byte(data[8:24])).Unmap()
		remote = netip.AddrFrom16([16]byte(data[24:40])).Unmap()
	}
	keep := ports[localPort] && !local.IsLoopback() && !remote.IsLoopback()
	var info []byte
	for attrs := data[transportDiagSize:]; len(attrs) > 0; {
		if len(attrs) < 4 {
			return peer, false, errors.New("transport peers: truncated inet_diag attribute")
		}
		n := int(binary.NativeEndian.Uint16(attrs[:2]))
		if n < 4 || n > len(attrs) || transportAlign(n) > len(attrs) {
			return peer, false, errors.New("transport peers: invalid inet_diag attribute length")
		}
		if binary.NativeEndian.Uint16(attrs[2:4]) == transportDiagInfo {
			if info != nil || n < 4+60 {
				return peer, false, errors.New("transport peers: invalid TCP_INFO attribute")
			}
			info = attrs[4:n]
		}
		attrs = attrs[transportAlign(n):]
	}
	if !keep {
		return peer, false, nil
	}
	if info == nil {
		return peer, false, errors.New("transport peers: matching socket missing TCP_INFO")
	}
	if localPort == 0 || remotePort == 0 || local.IsUnspecified() || remote.IsUnspecified() {
		return peer, false, errors.New("transport peers: invalid established socket endpoints")
	}
	peer.Local = netip.AddrPortFrom(local, localPort)
	peer.Remote = netip.AddrPortFrom(remote, remotePort)
	peer.Idle = time.Duration(binary.NativeEndian.Uint32(info[56:60])) * time.Millisecond
	return peer, true, nil
}
