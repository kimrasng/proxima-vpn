//go:build linux

package connectionlog

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

type wgLinuxCapture struct {
	file  *os.File
	raw   syscall.RawConn
	index int
}

func wgCaptureSupported() bool { return true }

func wgKernelRelease() (string, error) {
	var name unix.Utsname
	if err := unix.Uname(&name); err != nil {
		return "", err
	}
	var release []byte
	for _, c := range name.Release {
		if c == 0 {
			break
		}
		release = append(release, byte(c))
	}
	return string(release), nil
}

func openWGCapture(index int) (wgCapture, error) {
	// Native-endian representation of network-order ETH_P_ALL.
	protocol := int(binary.NativeEndian.Uint16([]byte{0, unix.ETH_P_ALL}))
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, protocol)
	if err != nil {
		return nil, fmt.Errorf("open AF_PACKET (CAP_NET_RAW required): %w", err)
	}
	fail := func(err error) (wgCapture, error) { return nil, errors.Join(err, unix.Close(fd)) }
	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: uint16(protocol), Ifindex: index}); err != nil {
		return fail(fmt.Errorf("bind packet interface: %w", err))
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 4*1024*1024); err != nil {
		return fail(err)
	}
	f := os.NewFile(uintptr(fd), "wireguard-packet")
	raw, err := f.SyscallConn()
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &wgLinuxCapture{file: f, raw: raw, index: index}, nil
}

func (c *wgLinuxCapture) receive(buf []byte) (wgReceived, error) {
	var n, flags int
	var from unix.Sockaddr
	var recvErr error
	err := c.raw.Read(func(fd uintptr) bool {
		n, _, flags, from, recvErr = unix.Recvmsg(int(fd), buf, nil, unix.MSG_TRUNC)
		if errors.Is(recvErr, unix.EINTR) || errors.Is(recvErr, unix.EAGAIN) {
			return false
		}
		return true
	})
	if err != nil {
		return wgReceived{}, err
	}
	if recvErr != nil {
		return wgReceived{}, recvErr
	}
	addr, ok := from.(*unix.SockaddrLinklayer)
	if !ok || addr.Ifindex != c.index {
		return wgReceived{}, fmt.Errorf("packet interface metadata mismatch")
	}
	// WG is point-to-point: only local incoming and outgoing copies qualify.
	if addr.Pkttype != unix.PACKET_HOST && addr.Pkttype != unix.PACKET_OUTGOING {
		return wgReceived{truncated: true}, nil
	}
	truncated := flags&unix.MSG_TRUNC != 0 || n > len(buf)
	return wgReceived{data: buf[:min(n, len(buf))], outgoing: addr.Pkttype == unix.PACKET_OUTGOING, truncated: truncated}, nil
}

func (c *wgLinuxCapture) statistics() (uint64, uint64, error) {
	var stats *unix.TpacketStats
	var statErr error
	err := c.raw.Control(func(fd uintptr) {
		stats, statErr = unix.GetsockoptTpacketStats(int(fd), unix.SOL_PACKET, unix.PACKET_STATISTICS)
	})
	if err != nil {
		return 0, 0, err
	}
	if statErr != nil {
		return 0, 0, statErr
	}
	return uint64(stats.Packets), uint64(stats.Drops), nil
}

func (c *wgLinuxCapture) close() error { return c.file.Close() }
