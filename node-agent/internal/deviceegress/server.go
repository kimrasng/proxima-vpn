// Package deviceegress provides authenticated, centrally budgeted SOCKS5 egress.
package deviceegress

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

const (
	maxConnections   = 1024
	handshakeTimeout = 10 * time.Second
	dialTimeout      = 10 * time.Second
	writeTimeout     = 30 * time.Second
)

// PermitFunc must honor context cancellation. Errors deny forwarding.
type PermitFunc func(context.Context, string, devicebandwidth.Direction, int) (devicebandwidth.PermitResponse, error)

// DialFunc replaces only the outbound transport; destination validation still
// runs first. It must honor context cancellation and the supplied IP address.
type DialFunc func(context.Context, string, string) (net.Conn, error)

// Server owns its listener and all authenticated TCP and UDP associations.
// The server is single-use: Close prevents subsequent Start calls.
type Server struct {
	mu          sync.Mutex
	admit       func(context.Context, string) error
	permit      PermitFunc
	dial        DialFunc
	credentials map[string][32]byte
	revoked     map[string]struct{}
	// The agent marks state unavailable before exposing the listener if bootstrap fails.
	revocationKnown bool
	devices         map[string]*deviceState
	sessions        map[*session]struct{}
	listener        net.Listener
	ctx             context.Context
	cancel          context.CancelFunc
	closed          bool
	wg              sync.WaitGroup
}

type session struct {
	server        *Server
	conn          net.Conn
	ctx           context.Context
	cancel        context.CancelFunc
	uuid          string
	password      [32]byte
	authenticated bool // protected by server.mu
	admitted      bool // protected by server.mu; set only after a successful permit
	device        *deviceState
}

// At most one granted chunk (or one UDP datagram) per device and direction
// can be awaiting a socket write. All other sessions wait without a grant.
type deviceState struct {
	refs  int // protected by server.mu
	gates [2]chan struct{}
}

func (c *session) acquire(ctx context.Context, direction devicebandwidth.Direction) (func(), error) {
	index := 0
	if direction == devicebandwidth.Download {
		index = 1
	}
	gate := c.device.gates[index]
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, err
		}
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) SetAdmitter(admit func(context.Context, string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admit = admit
}

func New(permit PermitFunc) *Server {
	return NewWithDialer(permit, nil)
}

// NewWithDialer is an explicit transport seam for isolated tests. A nil dialer
// uses net.Dialer with a ten-second timeout. It cannot bypass address policy.
func NewWithDialer(permit PermitFunc, dial DialFunc) *Server {
	if dial == nil {
		dial = (&net.Dialer{Timeout: dialTimeout}).DialContext
	}
	return &Server{permit: permit, dial: dial, credentials: make(map[string][32]byte), revoked: make(map[string]struct{}), revocationKnown: true, devices: make(map[string]*deviceState), sessions: make(map[*session]struct{})}
}

// ReconcileRevokedUUIDs replaces the control plane's current revocation set.
// Revocation closes existing TCP streams and UDP associations, including idle
// ones. A later successful empty snapshot permits authentication again.
func (s *Server) ReconcileRevokedUUIDs(uuids []string) {
	next := make(map[string]struct{}, len(uuids))
	for _, uuid := range uuids {
		next[uuid] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = next
	s.revocationKnown = true
	for connection := range s.sessions {
		if connection.authenticated {
			if _, revoked := next[connection.uuid]; revoked {
				connection.close()
			}
		}
	}
}

// ActiveAdmittedUUIDs returns the distinct UUIDs with a live association that
// has received a successful central permit. Idle associations remain active.
func (s *Server) ActiveAdmittedUUIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := make(map[string]struct{})
	for connection := range s.sessions {
		if connection.admitted && connection.ctx.Err() == nil {
			active[connection.uuid] = struct{}{}
		}
	}
	uuids := make([]string, 0, len(active))
	for uuid := range active {
		uuids = append(uuids, uuid)
	}
	slices.Sort(uuids)
	return uuids
}

// RevocationUnavailable fails closed while the authoritative snapshot cannot
// be fetched. Poll recovery restores only UUIDs absent from the new snapshot.
func (s *Server) RevocationUnavailable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revocationKnown = false
	for connection := range s.sessions {
		connection.close()
	}
}

// SetCredentials atomically replaces the credential set. Invalid input leaves
// the old set intact; changed and removed credentials revoke live associations.
func (s *Server) SetCredentials(credentials []devicebandwidth.Credential) error {
	next := make(map[string][32]byte, len(credentials))
	for _, credential := range credentials {
		if len(credential.UUID) == 0 || len(credential.UUID) > 255 || len(credential.Password) == 0 || len(credential.Password) > 255 {
			return errors.New("SOCKS credentials must contain 1–255 byte usernames and passwords")
		}
		if _, exists := next[credential.UUID]; exists {
			return fmt.Errorf("duplicate device credential %q", credential.UUID)
		}
		next[credential.UUID] = sha256.Sum256([]byte(credential.Password))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	s.credentials = next
	for connection := range s.sessions {
		if !connection.authenticated {
			continue
		}
		password, exists := next[connection.uuid]
		if !exists || subtle.ConstantTimeCompare(password[:], connection.password[:]) != 1 {
			connection.close()
		}
	}
	return nil
}

// Start binds only a numeric loopback address and serves asynchronously.
func (s *Server) Start(ctx context.Context, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("SOCKS listener must bind a numeric loopback address")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.listener != nil {
		return errors.New("SOCKS server already started or closed")
	}
	if s.permit == nil {
		return errors.New("SOCKS server requires a permit callback")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	s.listener = listener
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go s.accept()
	go func() {
		<-s.ctx.Done()
		s.Close()
	}()
	return nil
}

// Addr returns the bound listener address, or nil before Start.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Close cancels permit waits, closes all sockets, and waits for handlers to exit.
func (s *Server) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.cancel != nil {
			s.cancel()
		}
		if s.listener != nil {
			s.listener.Close()
		}
		for connection := range s.sessions {
			connection.close()
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		peer, ok := conn.RemoteAddr().(*net.TCPAddr)
		if s.closed || len(s.sessions) >= maxConnections || !ok || !peer.IP.IsLoopback() {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		connection := &session{server: s, conn: conn, ctx: ctx, cancel: cancel}
		s.sessions[connection] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() {
				connection.close()
				s.mu.Lock()
				delete(s.sessions, connection)
				if connection.device != nil {
					connection.device.refs--
					if connection.device.refs == 0 {
						delete(s.devices, connection.uuid)
					}
				}
				s.mu.Unlock()
			}()
			connection.serve()
		}()
	}
}

func (c *session) close() {
	c.cancel()
	c.conn.Close()
}

func (c *session) serve() {
	c.conn.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := c.authenticate(); err != nil {
		return
	}
	var header [4]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil || header[0] != 5 || header[2] != 0 {
		return
	}
	host, port, err := readAddress(c.conn, header[3])
	if err != nil {
		c.reply(8, nil)
		return
	}
	switch header[1] {
	case 1:
		c.connect(host, port)
	case 3:
		c.associate(host, port)
	default:
		c.reply(7, nil)
	}
}

func (c *session) authenticate() error {
	var header [2]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil || header[0] != 5 || header[1] == 0 {
		return errors.New("invalid SOCKS greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(c.conn, methods); err != nil {
		return err
	}
	found := false
	for _, method := range methods {
		found = found || method == 2
	}
	if !found {
		writeAll(c.conn, []byte{5, 255})
		return errors.New("username/password authentication required")
	}
	if err := writeAll(c.conn, []byte{5, 2}); err != nil {
		return err
	}
	if _, err := io.ReadFull(c.conn, header[:]); err != nil || header[0] != 1 || header[1] == 0 {
		return errors.New("invalid authentication request")
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(c.conn, username); err != nil {
		return err
	}
	var length [1]byte
	if _, err := io.ReadFull(c.conn, length[:]); err != nil || length[0] == 0 {
		return errors.New("invalid password length")
	}
	password := make([]byte, int(length[0]))
	if _, err := io.ReadFull(c.conn, password); err != nil {
		return err
	}
	actual := sha256.Sum256(password)
	c.server.mu.Lock()
	expected, exists := c.server.credentials[string(username)]
	matches := subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
	_, revoked := c.server.revoked[string(username)]
	valid := exists && matches && !revoked && c.server.revocationKnown && c.ctx.Err() == nil
	if valid {
		c.uuid = string(username)
		c.password = actual
		c.authenticated = true
		c.device = c.server.devices[c.uuid]
		if c.device == nil {
			c.device = &deviceState{gates: [2]chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)}}
			c.server.devices[c.uuid] = c.device
		}
		c.device.refs++
	}
	c.server.mu.Unlock()
	if !valid {
		writeAll(c.conn, []byte{1, 1})
		return errors.New("authentication denied")
	}
	return writeAll(c.conn, []byte{1, 0})
}

func (c *session) allow(ctx context.Context, direction devicebandwidth.Direction, n int) error {
	if n == 0 {
		return ctx.Err()
	}
	if n < 0 || n > devicebandwidth.MaxChunk {
		return errors.New("invalid permit size")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		requestCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		response, err := c.server.permit(requestCtx, c.uuid, direction, n)
		deadlineErr := requestCtx.Err()
		cancel()
		if err != nil {
			return err
		}
		if deadlineErr != nil {
			return deadlineErr
		}
		if response.Allowed {
			c.server.mu.Lock()
			_, revoked := c.server.revoked[c.uuid]
			if ctx.Err() != nil || c.ctx.Err() != nil || c.server.closed || !c.server.revocationKnown || revoked {
				c.server.mu.Unlock()
				return context.Canceled
			}
			c.admitted = true
			c.server.mu.Unlock()
			return nil
		}
		if response.DeniedReason != "" {
			c.close()
			return fmt.Errorf("connection permit denied: %s", response.DeniedReason)
		}
		// Clamp before converting to duration, including malicious integer values.
		retry := response.RetryAfterMS
		if retry < 10 {
			retry = 10
		} else if retry > 1000 {
			retry = 1000
		}
		timer := time.NewTimer(time.Duration(retry) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *session) reply(code byte, address net.Addr) error {
	ip := net.IPv4zero
	port := 0
	switch addr := address.(type) {
	case *net.TCPAddr:
		ip, port = addr.IP, addr.Port
	case *net.UDPAddr:
		ip, port = addr.IP, addr.Port
	}
	return writeAll(c.conn, append([]byte{5, code, 0}, encodeAddress(ip, port)...))
}

func readAddress(reader io.Reader, addressType byte) (string, int, error) {
	var address []byte
	switch addressType {
	case 1:
		address = make([]byte, 4)
	case 4:
		address = make([]byte, 16)
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil || length[0] == 0 {
			return "", 0, errors.New("invalid destination hostname")
		}
		address = make([]byte, int(length[0]))
	default:
		return "", 0, errors.New("unsupported destination address type")
	}
	if _, err := io.ReadFull(reader, address); err != nil {
		return "", 0, err
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return "", 0, err
	}
	host := string(address)
	if addressType != 3 {
		host = net.IP(address).String()
	}
	return host, int(port[0])<<8 | int(port[1]), nil
}

func encodeAddress(ip net.IP, port int) []byte {
	var result []byte
	if ipv4 := ip.To4(); ipv4 != nil {
		result = append([]byte{1}, ipv4...)
	} else {
		result = append([]byte{4}, ip.To16()...)
	}
	return append(result, byte(port>>8), byte(port))
}

func writeAll(conn net.Conn, payload []byte) error {
	for len(payload) != 0 {
		n, err := conn.Write(payload)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

// resolveDestination validates every DNS result and dials a numeric address,
// avoiding a second lookup and DNS-rebinding bypass of destination policy.
func resolveDestination(ctx context.Context, host string, port int) (string, error) {
	if port <= 0 || port > 65535 {
		return "", errors.New("invalid destination port")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return "", errors.New("destination resolution failed")
	}
	local, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("enumerate node addresses: %w", err)
	}
	for _, address := range addresses {
		ip := address.IP
		if address.Zone != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return "", errors.New("destination is not public egress")
		}
		for _, addr := range local {
			if ipNet, ok := addr.(*net.IPNet); ok && ip.Equal(ipNet.IP) {
				return "", errors.New("destination is a node interface address")
			}
		}
	}
	return net.JoinHostPort(addresses[0].IP.String(), strconv.Itoa(port)), nil
}

func (c *session) reserveBeforeSuccess() error {
	c.server.mu.Lock()
	admit := c.server.admit
	c.server.mu.Unlock()
	if admit == nil {
		return nil
	} // isolated legacy tests; production always installs an admitter
	ctx, cancel := context.WithTimeout(c.ctx, dialTimeout)
	defer cancel()
	if err := admit(ctx, c.uuid); err != nil {
		return err
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	c.server.mu.Lock()
	_, revoked := c.server.revoked[c.uuid]
	if c.server.closed || !c.server.revocationKnown || revoked {
		c.server.mu.Unlock()
		return context.Canceled
	}
	c.admitted = true
	c.server.mu.Unlock()
	return nil
}

func (c *session) connect(host string, port int) {
	if err := c.reserveBeforeSuccess(); err != nil {
		c.reply(2, nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, dialTimeout)
	defer cancel()
	address, err := resolveDestination(ctx, host, port)
	if err != nil {
		c.reply(2, nil)
		return
	}
	remote, err := c.server.dial(ctx, "tcp", address)
	if err != nil {
		c.reply(5, nil)
		return
	}
	defer remote.Close()
	if err := c.reply(0, remote.LocalAddr()); err != nil {
		return
	}
	c.conn.SetDeadline(time.Time{})
	stop := context.AfterFunc(c.ctx, func() { remote.Close() })
	defer stop()
	results := make(chan error, 2)
	go func() { results <- c.copyChunks(remote, c.conn, devicebandwidth.Upload) }()
	go func() { results <- c.copyChunks(c.conn, remote, devicebandwidth.Download) }()
	if err := <-results; err != nil {
		c.close()
		remote.Close()
	}
	<-results
}

func (c *session) copyChunks(destination, source net.Conn, direction devicebandwidth.Direction) error {
	buffer := make([]byte, devicebandwidth.MaxChunk)
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			release, err := c.acquire(c.ctx, direction)
			if err != nil {
				return err
			}
			err = c.allow(c.ctx, direction, n)
			if err == nil {
				err = c.ctx.Err()
			}
			if err == nil {
				destination.SetWriteDeadline(time.Now().Add(writeTimeout))
				err = writeAll(destination, buffer[:n])
			}
			release()
			if err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if halfCloser, ok := destination.(interface{ CloseWrite() error }); ok {
					return halfCloser.CloseWrite()
				}
				return io.EOF
			}
			return readErr
		}
	}
}
