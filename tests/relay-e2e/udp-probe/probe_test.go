package main

import (
	"net"
	"testing"
	"time"
)

func TestMarkerRoundTrip_whenNonceMatches(t *testing.T) {
	// Given
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go servePacket(conn, "EXIT_A")

	// When
	client, err := net.DialUDP("udp", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	_, err = client.Write([]byte("nonce"))
	if err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 128)
	n, err := client.Read(response)

	// Then
	if err != nil || string(response[:n]) != "EXIT_A:nonce" {
		t.Fatalf("response %q: %v", response[:n], err)
	}
}

func TestVerify_whenMarkerOrNonceDiffers(t *testing.T) {
	// Given
	for _, reply := range []string{"EXIT_B:nonce", "EXIT_A:other", "EXIT_A"} {
		// When
		err := verify([]byte(reply), "EXIT_A", "nonce")
		// Then
		if err == nil {
			t.Fatalf("accepted %q", reply)
		}
	}
}
