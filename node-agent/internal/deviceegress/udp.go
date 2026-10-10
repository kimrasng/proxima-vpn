package deviceegress

import (
	"bytes"
	"context"
	"net"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

const (
	maxUDPDestinations = 64
	udpIdleTimeout     = 60 * time.Second
	maxUDPDatagram     = 65535
)

type udpAssociation struct {
	session *session
	ctx     context.Context
	cancel  context.CancelFunc
	local   *net.UDPConn
	mu      sync.Mutex
	source  *net.UDPAddr
	remotes map[string]*udpRemote
	wg      sync.WaitGroup
}

type udpRemote struct {
	conn     net.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	ip       net.IP
	port     int
	lastUsed time.Time // guarded by association.mu
}

// UDP frames carry no credentials. A wildcard source port therefore assumes
// trusted node-local clients: the first valid datagram claims the association.
// The dedicated OS-selected ephemeral port reduces blind scanning, but is not
// authentication against a hostile local process. Specified source tuples are
// constrained to the authenticated TCP peer and checked before source pinning.
func (c *session) associate(host string, port int) {
	if err := c.reserveBeforeSuccess(); err != nil {
		_ = c.reply(2, nil)
		return
	}
	peer := c.conn.RemoteAddr().(*net.TCPAddr)
	requestedIP := net.ParseIP(host)
	if requestedIP == nil || (!requestedIP.IsUnspecified() && !requestedIP.Equal(peer.IP)) {
		_ = c.reply(2, nil)
		return
	}
	local, err := net.ListenUDP("udp", &net.UDPAddr{IP: c.conn.LocalAddr().(*net.TCPAddr).IP})
	if err != nil {
		_ = c.reply(1, nil)
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	a := &udpAssociation{session: c, ctx: ctx, cancel: cancel, local: local, remotes: make(map[string]*udpRemote)}
	stop := context.AfterFunc(ctx, func() { _ = local.Close() })
	defer stop()
	defer func() {
		cancel()
		_ = local.Close()
		a.mu.Lock()
		for _, remote := range a.remotes {
			remote.cancel()
			_ = remote.conn.Close()
		}
		a.mu.Unlock()
		_ = c.conn.Close()
		a.wg.Wait()
	}()
	if err := c.reply(0, local.LocalAddr()); err != nil {
		return
	}
	_ = c.conn.SetDeadline(time.Time{})
	// The TCP connection is the association lifetime, never an egress stream.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		var control [1]byte
		_, _ = c.conn.Read(control[:])
		cancel()
	}()
	a.wg.Add(1)
	go a.expire()
	buffer := make([]byte, maxUDPDatagram)
	for {
		n, sender, err := local.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if !sender.IP.IsLoopback() || !sender.IP.Equal(peer.IP) || (port != 0 && sender.Port != port) {
			continue
		}
		a.mu.Lock()
		pinned := a.source == nil || (sender.IP.Equal(a.source.IP) && sender.Port == a.source.Port && sender.Zone == a.source.Zone)
		a.mu.Unlock()
		if !pinned || n < 4 || buffer[0] != 0 || buffer[1] != 0 || buffer[2] != 0 {
			continue // Non-zero FRAG is deliberately unsupported.
		}
		reader := bytes.NewReader(buffer[4:n])
		host, destinationPort, err := readAddress(reader, buffer[3])
		if err != nil {
			continue
		}
		resolveCtx, resolveCancel := context.WithTimeout(a.ctx, dialTimeout)
		address, err := resolveDestination(resolveCtx, host, destinationPort)
		resolveCancel()
		if err != nil {
			continue
		}
		// Only a structurally valid, permitted destination can claim a wildcard
		// source. Malformed, fragmented, and prohibited datagrams do not pin it.
		a.mu.Lock()
		if a.source == nil {
			a.source = sender
		}
		a.mu.Unlock()
		payload := buffer[n-reader.Len() : n]
		if err := a.send(address, payload); err != nil {
			// A permit error is fatal to the entire association (fail closed).
			return
		}
	}
}

func (a *udpAssociation) send(address string, payload []byte) error {
	ctx, cancel := context.WithTimeout(a.ctx, dialTimeout)
	defer cancel()
	a.mu.Lock()
	remote := a.remotes[address]
	full := len(a.remotes) >= maxUDPDestinations
	a.mu.Unlock()
	if remote == nil {
		if full {
			return nil
		}
		conn, err := a.session.server.dial(ctx, "udp", address)
		if err != nil {
			return nil
		}
		host, portText, _ := net.SplitHostPort(address)
		portNumber, _ := net.LookupPort("udp", portText)
		remoteCtx, remoteCancel := context.WithCancel(a.ctx)
		remote = &udpRemote{conn: conn, ctx: remoteCtx, cancel: remoteCancel, ip: net.ParseIP(host), port: portNumber, lastUsed: time.Now()}
		a.mu.Lock()
		a.remotes[address] = remote
		a.mu.Unlock()
		a.wg.Add(1)
		go a.receive(address, remote)
	}
	release, err := a.session.acquire(remote.ctx, devicebandwidth.Upload)
	if err != nil {
		return nil
	}
	defer release()
	if err := a.session.allowPayload(remote.ctx, devicebandwidth.Upload, len(payload)); err != nil {
		if a.ctx.Err() == nil && remote.ctx.Err() != nil {
			return nil // An idle destination expired while awaiting budget.
		}
		return err
	}
	_ = remote.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	n, err := remote.conn.Write(payload)
	if err != nil || n != len(payload) {
		a.remove(address, remote)
		return nil
	}
	a.mu.Lock()
	remote.lastUsed = time.Now()
	a.mu.Unlock()
	return nil
}

func (c *session) allowPayload(ctx context.Context, direction devicebandwidth.Direction, size int) error {
	for size > 0 {
		chunk := min(size, devicebandwidth.MaxChunk)
		if err := c.allow(ctx, direction, chunk); err != nil {
			return err
		}
		size -= chunk
	}
	return ctx.Err()
}

func (a *udpAssociation) receive(address string, remote *udpRemote) {
	defer a.wg.Done()
	defer a.remove(address, remote)
	stop := context.AfterFunc(remote.ctx, func() { _ = remote.conn.Close() })
	defer stop()
	buffer := make([]byte, maxUDPDatagram)
	for {
		n, err := remote.conn.Read(buffer)
		if err != nil {
			return
		}
		a.mu.Lock()
		remote.lastUsed = time.Now()
		sender := a.source
		a.mu.Unlock()
		release, err := a.session.acquire(remote.ctx, devicebandwidth.Download)
		if err != nil {
			return
		}
		if err := a.session.allowPayload(remote.ctx, devicebandwidth.Download, n); err != nil {
			release()
			if remote.ctx.Err() == nil {
				a.cancel()
			}
			return
		}
		frame := append([]byte{0, 0, 0}, encodeAddress(remote.ip, remote.port)...)
		frame = append(frame, buffer[:n]...)
		_ = a.local.SetWriteDeadline(time.Now().Add(writeTimeout))
		written, err := a.local.WriteToUDP(frame, sender)
		release()
		if err != nil || written != len(frame) {
			a.cancel()
			return
		}
	}
}

func (a *udpAssociation) remove(address string, remote *udpRemote) {
	remote.cancel()
	_ = remote.conn.Close()
	a.mu.Lock()
	if a.remotes[address] == remote {
		delete(a.remotes, address)
	}
	a.mu.Unlock()
}

func (a *udpAssociation) expire() {
	defer a.wg.Done()
	ticker := time.NewTicker(udpIdleTimeout / 2)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case now := <-ticker.C:
			a.mu.Lock()
			for address, remote := range a.remotes {
				if now.Sub(remote.lastUsed) >= udpIdleTimeout {
					remote.cancel()
					_ = remote.conn.Close()
					delete(a.remotes, address)
				}
			}
			a.mu.Unlock()
		}
	}
}
