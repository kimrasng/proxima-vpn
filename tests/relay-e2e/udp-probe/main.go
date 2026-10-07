package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const timeout = 5 * time.Second

func servePacket(conn *net.UDPConn, marker string) {
	buf := make([]byte, 512)
	for {
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		_, _ = conn.WriteToUDP([]byte(marker+":"+string(buf[:n])), peer)
	}
}

func verify(data []byte, marker, nonce string) error {
	if string(data) != marker+":"+nonce {
		return errors.New("UDP marker or nonce mismatch")
	}
	return nil
}

func probe(socks, marker string) error {
	control, err := net.DialTimeout("tcp", socks, timeout)
	if err != nil {
		return fmt.Errorf("SOCKS connection: %w", err)
	}
	defer control.Close()
	if err := control.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	answer := make([]byte, 2)
	if _, err := io.ReadFull(control, answer); err != nil {
		return err
	}
	if answer[0] != 5 || answer[1] != 0 {
		return errors.New("SOCKS authentication refused")
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	relay := make([]byte, 10)
	if _, err := io.ReadFull(control, relay); err != nil {
		return err
	}
	if relay[0] != 5 || relay[1] != 0 || relay[3] != 1 {
		return errors.New("SOCKS UDP associate refused")
	}
	ip := net.IPv4(relay[4], relay[5], relay[6], relay[7])
	if ip.IsUnspecified() {
		ip = net.IPv4(127, 0, 0, 1)
	}
	udp, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: int(relay[8])<<8 | int(relay[9])})
	if err != nil {
		return err
	}
	defer udp.Close()
	if err := udp.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	nonce := hex.EncodeToString(random[:])
	// 198.51.100.10:9091 is a documentation address that only the relaye2e
	// node-agent build maps to the Exit's loopback UDP marker.
	request := append([]byte{0, 0, 0, 1, 198, 51, 100, 10, 0x23, 0x83}, nonce...)
	if _, err := udp.Write(request); err != nil {
		return err
	}
	response := make([]byte, 512)
	n, err := udp.Read(response)
	if err != nil {
		return fmt.Errorf("SOCKS UDP response: %w", err)
	}
	if n < 10 || response[0] != 0 || response[1] != 0 || response[2] != 0 || response[3] != 1 ||
		response[4] != 198 || response[5] != 51 || response[6] != 100 || response[7] != 10 ||
		response[8] != 0x23 || response[9] != 0x83 {
		return errors.New("invalid SOCKS UDP response")
	}
	return verify(response[10:n], marker, nonce)
}

func run(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: udp-probe server|probe ADDRESS MARKER")
	}
	switch args[0] {
	case "server":
		addr, err := net.ResolveUDPAddr("udp", args[1])
		if err != nil {
			return err
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			return err
		}
		defer conn.Close()
		servePacket(conn, args[2])
		return nil
	case "probe":
		return probe(args[1], args[2])
	default:
		return errors.New("unknown UDP probe mode")
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
