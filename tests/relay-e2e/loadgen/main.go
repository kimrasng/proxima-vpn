// Command loadgen is the traffic source and sink of the relay load benchmark.
//
// On each Exit it serves a TCP and a UDP endpoint behind the test-only fixture
// address, and on the client it drives load through the local Xray SOCKS
// listeners, so every byte takes the production path: client Xray, Entry
// nftables DNAT, Exit Xray, device egress. It uses the standard library only,
// so the test image builds it without a module, like udp-probe.
//
//	loadgen serve TCP_ADDR UDP_ADDR
//	loadgen bench [flags]
//
// bench prints one JSON document. Threshold misses are recorded in its
// "failures" list rather than turned into an exit status, so the caller keeps
// the measurements; only a run that measured nothing exits non-zero.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const mib = 1 << 20

// block is the payload unit written by both ends. Its content is irrelevant
// (the tunnel encrypts it); it is random so nothing along the path could
// compress it.
var block = func() []byte {
	b := make([]byte, 64<<10)
	_, _ = rand.Read(b)
	return b
}()

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loadgen serve TCP_ADDR UDP_ADDR | loadgen bench [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "bench":
		err = bench(os.Args[2:])
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}
}

// ---- server ---------------------------------------------------------------

// serve answers one command per TCP connection - "down N" streams N bytes,
// "up N" reads N bytes and acknowledges, "ping" lines are echoed as "pong" -
// and echoes every UDP datagram unchanged.
func serve(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: loadgen serve TCP_ADDR UDP_ADDR")
	}
	listener, err := net.Listen("tcp", args[0])
	if err != nil {
		return err
	}
	packets, err := net.ListenPacket("udp", args[1])
	if err != nil {
		return err
	}
	go func() {
		buf := make([]byte, 65535)
		for {
			n, peer, err := packets.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = packets.WriteTo(buf[:n], peer)
		}
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) >= 1 && fields[0] == "GET":
			// A minimal HTTP exchange: what a browser's first bytes look
			// like to the Exit's protocol sniffer.
			for {
				header, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if header == "\r\n" || header == "\n" {
					break
				}
			}
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\nConnection: close\r\n\r\npong"))
			return
		case len(fields) == 1 && fields[0] == "ping":
			if _, err := conn.Write([]byte("pong\n")); err != nil {
				return
			}
		case len(fields) == 2 && fields[0] == "down":
			n, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || n < 0 {
				return
			}
			_ = writeN(conn, n)
			return
		case len(fields) == 2 && fields[0] == "up":
			n, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || n < 0 {
				return
			}
			got, _ := io.CopyN(io.Discard, reader, n)
			_, _ = fmt.Fprintf(conn, "ok %d\n", got)
			return
		default:
			return
		}
	}
}

func writeN(w io.Writer, n int64) error {
	for n > 0 {
		chunk := block
		if int64(len(chunk)) > n {
			chunk = chunk[:n]
		}
		written, err := w.Write(chunk)
		n -= int64(written)
		if err != nil {
			return err
		}
	}
	return nil
}

// ---- client ---------------------------------------------------------------

type percentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

type udpResult struct {
	Sent        int         `json:"sent"`
	Received    int         `json:"received"`
	LossPercent float64     `json:"loss_percent"`
	OfferedPPS  int         `json:"offered_pps"`
	Size        int         `json:"datagram_bytes"`
	RTT         percentiles `json:"rtt_ms"`
}

type routeResult struct {
	TCPDownMbps float64 `json:"tcp_down_mbps"`
	TCPUpMbps   float64 `json:"tcp_up_mbps"`
	// FirstResponse is a new tunnel connection to its first answer when the
	// client opens with an HTTP request: the Reality handshake through the
	// Entry, the Exit's admission and egress dial and one round trip.
	// OpaqueFirstResponse is the same with a first message the Exit's
	// protocol sniffer (http, tls) cannot classify, which it holds until its
	// sniffing timeout. RTT is a round trip on an established connection.
	FirstResponse       percentiles `json:"first_response_ms"`
	OpaqueFirstResponse percentiles `json:"opaque_first_response_ms"`
	RTT                 percentiles `json:"rtt_ms"`
	UDP                 udpResult   `json:"udp"`
	// Application bytes that crossed the tunnel for this route, for checking
	// the Entry's route counters (which see the same bytes plus overhead).
	PayloadUpBytes   int64 `json:"payload_up_bytes"`
	PayloadDownBytes int64 `json:"payload_down_bytes"`

	up, down atomic.Int64
}

type concurrentResult struct {
	StreamsPerRoute int     `json:"streams_per_route"`
	Streams         int     `json:"streams"`
	Completed       int     `json:"completed"`
	StreamMiB       int     `json:"stream_mib"`
	Seconds         float64 `json:"seconds"`
	AggregateMbps   float64 `json:"aggregate_down_mbps"`
}

type benchResult struct {
	Config     map[string]any          `json:"config"`
	Routes     map[string]*routeResult `json:"routes"`
	Concurrent concurrentResult        `json:"concurrent"`
	Failures   []string                `json:"failures"`

	mu sync.Mutex
}

func (r *benchResult) fail(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	r.Failures = append(r.Failures, msg)
	fmt.Fprintln(os.Stderr, "loadgen:", msg)
}

type route struct {
	name  string
	socks string
}

func bench(args []string) error {
	flags := flag.NewFlagSet("bench", flag.ContinueOnError)
	socksList := flags.String("socks", "", "comma-separated NAME=HOST:PORT SOCKS listeners, one per route")
	tcpTarget := flags.String("tcp", "", "TCP sink address as seen by the Exit")
	udpTarget := flags.String("udp", "", "UDP echo address as seen by the Exit")
	downMiB := flags.Int("down-mib", 64, "single-stream download size per route")
	upMiB := flags.Int("up-mib", 64, "single-stream upload size per route")
	streams := flags.Int("streams", 8, "concurrent download streams per route")
	streamMiB := flags.Int("stream-mib", 16, "size of each concurrent stream")
	setups := flags.Int("setups", 30, "new connections timed per route")
	pings := flags.Int("pings", 200, "round trips timed per route on one connection")
	udpCount := flags.Int("udp-count", 2000, "UDP datagrams per route")
	udpSize := flags.Int("udp-size", 1200, "UDP datagram payload bytes")
	udpPPS := flags.Int("udp-pps", 1000, "UDP offered rate per route")
	minMbps := flags.Float64("min-tcp-mbps", 5, "fail below this single-stream TCP rate")
	maxLoss := flags.Float64("max-udp-loss", 5, "fail above this UDP loss percentage")
	if err := flags.Parse(args); err != nil {
		return err
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp4", *tcpTarget)
	if err != nil {
		return fmt.Errorf("-tcp: %w", err)
	}
	udpAddr, err := net.ResolveUDPAddr("udp4", *udpTarget)
	if err != nil {
		return fmt.Errorf("-udp: %w", err)
	}
	var routes []route
	for _, item := range strings.Split(*socksList, ",") {
		name, addr, ok := strings.Cut(item, "=")
		if !ok || name == "" || addr == "" {
			return fmt.Errorf("-socks entry %q is not NAME=HOST:PORT", item)
		}
		routes = append(routes, route{name, addr})
	}
	if *udpSize < 16 || *udpSize > 1400 {
		return errors.New("-udp-size must be 16..1400")
	}

	result := &benchResult{
		Config: map[string]any{
			"down_mib": *downMiB, "up_mib": *upMiB, "streams_per_route": *streams, "stream_mib": *streamMiB,
			"setups": *setups, "pings": *pings, "udp_count": *udpCount, "udp_size": *udpSize, "udp_pps": *udpPPS,
			"min_tcp_mbps": *minMbps, "max_udp_loss_percent": *maxLoss,
		},
		Routes:   map[string]*routeResult{},
		Failures: []string{},
	}
	for _, r := range routes {
		result.Routes[r.name] = &routeResult{}
	}

	// Sequential per-route phases first, so each figure is one route alone.
	for _, r := range routes {
		out := result.Routes[r.name]
		if mbps, err := download(r, out, tcpAddr, int64(*downMiB)*mib); err != nil {
			result.fail("%s tcp download: %v", r.name, err)
		} else {
			out.TCPDownMbps = mbps
		}
		if mbps, err := upload(r, out, tcpAddr, int64(*upMiB)*mib); err != nil {
			result.fail("%s tcp upload: %v", r.name, err)
		} else {
			out.TCPUpMbps = mbps
		}
		if first, err := firstResponses(r, out, tcpAddr, *setups, true); err != nil {
			result.fail("%s connection setup: %v", r.name, err)
		} else {
			out.FirstResponse = first
		}
		if first, err := firstResponses(r, out, tcpAddr, *setups/3+1, false); err != nil {
			result.fail("%s opaque connection setup: %v", r.name, err)
		} else {
			out.OpaqueFirstResponse = first
		}
		if rtt, err := roundTrips(r, out, tcpAddr, *pings); err != nil {
			result.fail("%s round trips: %v", r.name, err)
		} else {
			out.RTT = rtt
		}
		if udp, err := udpLoad(r, out, udpAddr, *udpCount, *udpSize, *udpPPS); err != nil {
			result.fail("%s udp: %v", r.name, err)
		} else {
			out.UDP = udp
		}
	}

	// Then every route at once: the Entry forwards all streams together.
	result.Concurrent = concurrent(result, routes, tcpAddr, *streams, *streamMiB)

	for _, r := range routes {
		out := result.Routes[r.name]
		out.PayloadUpBytes, out.PayloadDownBytes = out.up.Load(), out.down.Load()
		if out.TCPDownMbps < *minMbps || out.TCPUpMbps < *minMbps {
			result.fail("%s single-stream TCP below %.1f Mbps (down %.1f, up %.1f)", r.name, *minMbps, out.TCPDownMbps, out.TCPUpMbps)
		}
		if out.UDP.Sent > 0 && out.UDP.LossPercent > *maxLoss {
			result.fail("%s UDP loss %.2f%% above %.2f%%", r.name, out.UDP.LossPercent, *maxLoss)
		}
	}
	if result.Concurrent.Completed != result.Concurrent.Streams {
		result.fail("%d of %d concurrent streams completed", result.Concurrent.Completed, result.Concurrent.Streams)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

// countingConn tallies the application bytes of one route's tunnel traffic.
type countingConn struct {
	net.Conn
	route *routeResult
}

func (c countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.route.down.Add(int64(n))
	return n, err
}

func (c countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.route.up.Add(int64(n))
	return n, err
}

// socksConnect opens a CONNECT through the route's SOCKS listener. Xray
// answers the SOCKS request before its outbound exists, so callers time the
// first application response, not this handshake.
func socksConnect(r route, out *routeResult, target *net.TCPAddr) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", r.socks, 5*time.Second)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := socksGreet(conn); err != nil {
		conn.Close()
		return nil, err
	}
	ip := target.IP.To4()
	request := []byte{5, 1, 0, 1, ip[0], ip[1], ip[2], ip[3], byte(target.Port >> 8), byte(target.Port)}
	if _, err := conn.Write(request); err != nil {
		conn.Close()
		return nil, err
	}
	if err := socksReply(conn); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return countingConn{conn, out}, nil
}

func socksGreet(conn net.Conn) error {
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	answer := make([]byte, 2)
	if _, err := io.ReadFull(conn, answer); err != nil {
		return err
	}
	if answer[0] != 5 || answer[1] != 0 {
		return errors.New("SOCKS authentication refused")
	}
	return nil
}

// socksReply reads a SOCKS reply and returns the bound address it carries.
func socksReply(conn net.Conn) error {
	_, err := socksReplyAddr(conn)
	return err
}

func socksReplyAddr(conn net.Conn) (*net.UDPAddr, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return nil, err
	}
	if head[0] != 5 || head[1] != 0 {
		return nil, fmt.Errorf("SOCKS request refused (reply %d)", head[1])
	}
	var host []byte
	switch head[3] {
	case 1:
		host = make([]byte, 4)
	case 4:
		host = make([]byte, 16)
	case 3:
		size := make([]byte, 1)
		if _, err := io.ReadFull(conn, size); err != nil {
			return nil, err
		}
		host = make([]byte, size[0])
	default:
		return nil, errors.New("SOCKS reply with unknown address type")
	}
	if _, err := io.ReadFull(conn, host); err != nil {
		return nil, err
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return nil, err
	}
	addr := &net.UDPAddr{Port: int(binary.BigEndian.Uint16(port))}
	if head[3] != 3 {
		addr.IP = net.IP(host)
	}
	return addr, nil
}

func mbps(bytes int64, elapsed time.Duration) float64 {
	return float64(bytes) * 8 / elapsed.Seconds() / 1e6
}

func download(r route, out *routeResult, target *net.TCPAddr, size int64) (float64, error) {
	conn, err := socksConnect(r, out, target)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	start := time.Now()
	if _, err := fmt.Fprintf(conn, "down %d\n", size); err != nil {
		return 0, err
	}
	got, err := io.CopyN(io.Discard, conn, size)
	if err != nil {
		return 0, fmt.Errorf("received %d of %d bytes: %w", got, size, err)
	}
	return mbps(size, time.Since(start)), nil
}

func upload(r route, out *routeResult, target *net.TCPAddr, size int64) (float64, error) {
	conn, err := socksConnect(r, out, target)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	start := time.Now()
	if _, err := fmt.Fprintf(conn, "up %d\n", size); err != nil {
		return 0, err
	}
	if err := writeN(conn, size); err != nil {
		return 0, err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("no acknowledgement: %w", err)
	}
	if strings.TrimSpace(line) != fmt.Sprintf("ok %d", size) {
		return 0, fmt.Errorf("sink acknowledged %q", strings.TrimSpace(line))
	}
	return mbps(size, time.Since(start)), nil
}

func ping(conn net.Conn, reader *bufio.Reader) error {
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		return err
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if line != "pong\n" {
		return fmt.Errorf("unexpected answer %q", line)
	}
	return nil
}

// httpExchange opens with an HTTP request. Its Host must be the real target:
// the Exit's sniffer (destOverride http) re-routes the connection to it.
func httpExchange(conn net.Conn, reader *bufio.Reader, target *net.TCPAddr) error {
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", target); err != nil {
		return err
	}
	status, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(status, "HTTP/1.1 200") {
		return fmt.Errorf("unexpected status %q", status)
	}
	return nil
}

func firstResponses(r route, out *routeResult, target *net.TCPAddr, count int, http bool) (percentiles, error) {
	samples := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		start := time.Now()
		conn, err := socksConnect(r, out, target)
		if err != nil {
			return percentiles{}, err
		}
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if http {
			err = httpExchange(conn, bufio.NewReader(conn), target)
		} else {
			err = ping(conn, bufio.NewReader(conn))
		}
		conn.Close()
		if err != nil {
			return percentiles{}, err
		}
		samples = append(samples, ms(time.Since(start)))
	}
	return summarize(samples), nil
}

func roundTrips(r route, out *routeResult, target *net.TCPAddr, count int) (percentiles, error) {
	conn, err := socksConnect(r, out, target)
	if err != nil {
		return percentiles{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	reader := bufio.NewReader(conn)
	if err := ping(conn, reader); err != nil { // warm-up: opens the tunnel
		return percentiles{}, err
	}
	samples := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		start := time.Now()
		if err := ping(conn, reader); err != nil {
			return percentiles{}, err
		}
		samples = append(samples, ms(time.Since(start)))
	}
	return summarize(samples), nil
}

// udpLoad paces datagrams through a SOCKS UDP association to the Exit's echo
// endpoint and times each echo by the sequence number it carries.
func udpLoad(r route, out *routeResult, target *net.UDPAddr, count, size, pps int) (udpResult, error) {
	res := udpResult{OfferedPPS: pps, Size: size}
	control, err := net.DialTimeout("tcp", r.socks, 5*time.Second)
	if err != nil {
		return res, err
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(10 * time.Second))
	if err := socksGreet(control); err != nil {
		return res, err
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return res, err
	}
	relay, err := socksReplyAddr(control)
	if err != nil {
		return res, err
	}
	_ = control.SetDeadline(time.Time{})
	if relay.IP == nil || relay.IP.IsUnspecified() {
		relay.IP = net.IPv4(127, 0, 0, 1)
	}
	conn, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		return res, err
	}
	defer conn.Close()

	ip := target.IP.To4()
	header := []byte{0, 0, 0, 1, ip[0], ip[1], ip[2], ip[3], byte(target.Port >> 8), byte(target.Port)}
	sentAt := make([]int64, count)
	seen := make([]bool, count)
	var mu sync.Mutex
	var samples []float64
	received := 0
	epoch := time.Now()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			if n < len(header)+16 {
				continue
			}
			payload := buf[len(header):n]
			seq := binary.BigEndian.Uint64(payload)
			if seq >= uint64(count) {
				continue
			}
			now := time.Since(epoch).Nanoseconds()
			out.down.Add(int64(len(payload)))
			mu.Lock()
			if !seen[seq] {
				seen[seq] = true
				received++
				samples = append(samples, float64(now-sentAt[seq])/1e6)
			}
			finished := received == count
			mu.Unlock()
			if finished {
				return
			}
		}
	}()

	packet := make([]byte, len(header)+size)
	copy(packet, header)
	copy(packet[len(header)+16:], block)
	interval := time.Second / time.Duration(pps)
	for i := 0; i < count; i++ {
		if wait := time.Duration(i)*interval - time.Since(epoch); wait > 0 {
			time.Sleep(wait)
		}
		now := time.Since(epoch).Nanoseconds()
		mu.Lock()
		sentAt[i] = now
		mu.Unlock()
		binary.BigEndian.PutUint64(packet[len(header):], uint64(i))
		binary.BigEndian.PutUint64(packet[len(header)+8:], uint64(now))
		if _, err := conn.Write(packet); err != nil {
			return res, err
		}
		out.up.Add(int64(size))
		res.Sent++
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	res.Received = received
	res.LossPercent = 100 * float64(res.Sent-received) / float64(res.Sent)
	res.RTT = summarize(samples)
	return res, nil
}

func concurrent(result *benchResult, routes []route, target *net.TCPAddr, perRoute, streamMiB int) concurrentResult {
	res := concurrentResult{StreamsPerRoute: perRoute, Streams: perRoute * len(routes), StreamMiB: streamMiB}
	size := int64(streamMiB) * mib
	var wg sync.WaitGroup
	var completed atomic.Int64
	start := time.Now()
	for _, r := range routes {
		for i := 0; i < perRoute; i++ {
			wg.Add(1)
			go func(r route) {
				defer wg.Done()
				if _, err := download(r, result.Routes[r.name], target, size); err != nil {
					result.fail("%s concurrent stream: %v", r.name, err)
					return
				}
				completed.Add(1)
			}(r)
		}
	}
	wg.Wait()
	elapsed := time.Since(start)
	res.Completed = int(completed.Load())
	res.Seconds = round(elapsed.Seconds())
	res.AggregateMbps = round(mbps(int64(res.Completed)*size, elapsed))
	return res
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func round(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func summarize(samples []float64) percentiles {
	if len(samples) == 0 {
		return percentiles{}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	at := func(q float64) float64 { return round(sorted[int(q*float64(len(sorted)-1)+0.5)]) }
	return percentiles{P50: at(0.5), P95: at(0.95), Max: round(sorted[len(sorted)-1])}
}
