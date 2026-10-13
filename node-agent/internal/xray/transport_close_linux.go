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

// CloseTransportPeer asks the kernel to destroy exactly one established TCP
// tuple in the current network namespace. This requires CAP_NET_ADMIN and
// kernel SOCK_DESTROY support. A tuple can be reused; callers must establish
// authorization separately, and this operation does not prove device identity.
func CloseTransportPeer(peer TransportPeer) error {
	request, err := transportDestroyRequest(peer)
	if err != nil {
		return err
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return fmt.Errorf("close transport peer: socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("close transport peer: bind: %w", err)
	}
	if err := unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("close transport peer: request: %w", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	buf := make([]byte, 4096)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("close transport peer: acknowledgement deadline exceeded")
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, int((remaining+time.Millisecond-1)/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("close transport peer: poll: %w", err)
		}
		if n == 0 {
			continue
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return errors.New("close transport peer: netlink poll failure")
		}
		n, _, flags, from, err := unix.Recvmsg(fd, buf, nil, 0)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return fmt.Errorf("close transport peer: receive: %w", err)
		}
		sender, ok := from.(*unix.SockaddrNetlink)
		if !ok || sender.Pid != 0 || flags&unix.MSG_TRUNC != 0 {
			return errors.New("close transport peer: invalid acknowledgement sender or truncation")
		}
		return parseTransportDestroyAck(buf[:n])
	}
}

// ProbeTransportClose reports whether this host can destroy established TCP
// sockets, without closing anything real. It asks the kernel to destroy a
// tuple inside TEST-NET-1 (RFC 5737), which no real connection can use, and
// reads the answer: ENOENT means the kernel has SOCK_DESTROY and this process
// may use it (nothing matched, as intended); EOPNOTSUPP means the kernel lacks
// CONFIG_INET_DIAG_DESTROY; EPERM means CAP_NET_ADMIN is missing.
func ProbeTransportClose() error {
	return classifyTransportCloseProbe(CloseTransportPeer(TransportPeer{
		Local:  netip.MustParseAddrPort("192.0.2.1:1"),
		Remote: netip.MustParseAddrPort("192.0.2.2:1"),
	}))
}

func classifyTransportCloseProbe(err error) error {
	switch {
	case err == nil, errors.Is(err, unix.ENOENT):
		return nil
	case errors.Is(err, unix.EOPNOTSUPP):
		return errors.New("kernel lacks CONFIG_INET_DIAG_DESTROY, so over-limit connections cannot be closed on this host")
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES):
		return errors.New("CAP_NET_ADMIN is required to close over-limit connections")
	default:
		return fmt.Errorf("could not verify connection closing: %w", err)
	}
}

func transportDestroyRequest(peer TransportPeer) ([]byte, error) {
	local, remote := peer.Local.Addr().Unmap(), peer.Remote.Addr().Unmap()
	if !peer.Local.IsValid() || !peer.Remote.IsValid() || peer.Local.Port() == 0 || peer.Remote.Port() == 0 || local.IsUnspecified() || remote.IsUnspecified() || local.Is4() != remote.Is4() {
		return nil, errors.New("close transport peer: invalid exact TCP tuple")
	}
	// Scoped addresses need an interface identifier, not just address bytes.
	// Refuse them rather than risk targeting a socket on another interface.
	if local.Zone() != "" || remote.Zone() != "" || local.IsLinkLocalUnicast() || remote.IsLinkLocalUnicast() {
		return nil, errors.New("close transport peer: scoped addresses unsupported")
	}
	request := make([]byte, 72)
	binary.NativeEndian.PutUint32(request[:4], uint32(len(request)))
	binary.NativeEndian.PutUint16(request[4:6], 21) // SOCK_DESTROY
	binary.NativeEndian.PutUint16(request[6:8], unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	binary.NativeEndian.PutUint32(request[8:12], 1)
	request[16], request[17] = unix.AF_INET6, unix.IPPROTO_TCP
	binary.NativeEndian.PutUint32(request[20:24], 1<<1) // TCP_ESTABLISHED
	binary.BigEndian.PutUint16(request[24:26], peer.Local.Port())
	binary.BigEndian.PutUint16(request[26:28], peer.Remote.Port())
	putAddress := func(dst []byte, addr netip.Addr) {
		if addr.Is4() {
			v := addr.As4()
			copy(dst, v[:])
		} else {
			v := addr.As16()
			copy(dst, v[:])
		}
	}
	if local.Is4() {
		request[16] = unix.AF_INET
	}
	putAddress(request[28:44], local)
	putAddress(request[44:60], remote)
	binary.NativeEndian.PutUint32(request[64:68], ^uint32(0))
	binary.NativeEndian.PutUint32(request[68:72], ^uint32(0))
	return request, nil
}

func parseTransportDestroyAck(data []byte) error {
	if len(data) < 36 {
		return errors.New("close transport peer: truncated acknowledgement")
	}
	n := uint64(binary.NativeEndian.Uint32(data[:4]))
	if n < 36 || n > uint64(len(data)) || uint64(transportAlign(int(n))) != uint64(len(data)) || binary.NativeEndian.Uint16(data[4:6]) != unix.NLMSG_ERROR || binary.NativeEndian.Uint32(data[8:12]) != 1 || binary.NativeEndian.Uint16(data[6:8])&unix.NLM_F_DUMP_INTR != 0 {
		return errors.New("close transport peer: malformed acknowledgement")
	}
	code := int32(binary.NativeEndian.Uint32(data[16:20]))
	if code < 0 {
		return fmt.Errorf("close transport peer: kernel rejected SOCK_DESTROY: %w", unix.Errno(-int64(code)))
	}
	if code != 0 {
		return errors.New("close transport peer: invalid acknowledgement status")
	}
	if binary.NativeEndian.Uint16(data[24:26]) != 21 || binary.NativeEndian.Uint32(data[28:32]) != 1 {
		return errors.New("close transport peer: acknowledgement for unexpected request")
	}
	return nil
}
