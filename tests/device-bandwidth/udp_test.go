package bandwidth_test

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// The UDP echo listener and its test socket are loopback-only. The remote SOCKS
// destination is still documentation-only and must go through an injected dial.
func udpEcho(t *testing.T) string {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = c.WriteToUDP(buf[:n], addr)
		}
	}()
	return c.LocalAddr().String()
}

func udpFrame(payload []byte) []byte {
	frame := []byte{0, 0, 0, 1}
	frame = append(frame, net.ParseIP(payloadHost).To4()...)
	frame = binary.BigEndian.AppendUint16(frame, payloadPort)
	return append(frame, payload...)
}

func udpPayload(frame []byte) ([]byte, error) {
	if len(frame) < 4 || frame[0] != 0 || frame[1] != 0 || frame[2] != 0 {
		return nil, fmt.Errorf("invalid or fragmented SOCKS UDP reply")
	}
	header := 0
	switch frame[3] {
	case 1:
		header = 10
	case 4:
		header = 22
	case 3:
		if len(frame) < 5 {
			return nil, fmt.Errorf("short domain reply")
		}
		header = 7 + int(frame[4])
	default:
		return nil, fmt.Errorf("unexpected SOCKS UDP address type")
	}
	if len(frame) < header {
		return nil, fmt.Errorf("short SOCKS UDP reply")
	}
	return frame[header:], nil
}

func udpProbe(t *testing.T, proxy, username, password string, window time.Duration, rate, burst int64) trafficResult {
	t.Helper()
	control, relay := socksConnect(t, proxy, username, password, 3, "0.0.0.0", 0)
	defer control.Close()
	if relay.IP.IsUnspecified() {
		relay.IP = net.ParseIP("127.0.0.1")
	}
	if !relay.IP.IsLoopback() {
		t.Fatalf("test relay must remain loopback: %v", relay)
	}
	conn, err := net.DialUDP("udp4", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	end := time.Now().Add(window)
	_ = conn.SetReadDeadline(end)
	payload := make([]byte, 1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	frame := udpFrame(payload)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for now := range ticker.C {
			if now.After(end) {
				return
			}
			_, _ = conn.Write(frame)
		}
	}()
	start := time.Now()
	var count int64
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if e, ok := err.(net.Error); !ok || !e.Timeout() {
				t.Error(err)
			}
			break
		}
		reply, err := udpPayload(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		if string(reply) != string(payload) {
			t.Fatal("UDP echo payload mismatch")
		}
		count += int64(len(reply))
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	result := trafficResult{Bytes: count, WindowSeconds: elapsed, RateBytesPerSecond: rate, BurstAllowance: burst, CeilingBytes: int64(float64(rate)*elapsed) + burst}
	if result.Bytes == 0 {
		t.Fatal("no UDP datagram completed")
	}
	if result.Bytes < int64(float64(rate)*elapsed*0.65) {
		t.Errorf("UDP throughput stalled or below configured rate floor: %+v", result)
	}
	if result.Bytes > result.CeilingBytes {
		t.Errorf("UDP exceeded the per-direction ceiling: %+v", result)
	}
	return result
}
