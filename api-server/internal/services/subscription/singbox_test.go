package subscription

import (
	"encoding/json"
	"testing"
)

func TestGenerateSingboxHysteriaUserpass(t *testing.T) {
	// Given an eligible Hysteria node already selected by provisioning.
	const uuid = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	nodes := []NodeInfo{
		{Name: "hy2", Protocol: "hysteria2", IP: "127.0.0.1", Port: 443},
		{Name: "trojan", Protocol: "trojan_tls", IP: "127.0.0.1", Port: 444},
		{Name: "wg", Protocol: "wireguard"},
	}
	// When the actual public sing-box generator renders the device profile.
	data, err := GenerateSingbox(nodes, uuid)
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		Outbounds []struct {
			Type     string `json:"type"`
			Password string `json:"password"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatal(err)
	}
	// Then Hysteria authenticates with user:password; other formats stay intact.
	found := false
	for _, outbound := range profile.Outbounds {
		switch outbound.Type {
		case "hysteria2":
			found = true
			if outbound.Password != uuid+":"+uuid {
				t.Fatalf("Hysteria credential does not match server userpass")
			}
		case "trojan":
			if outbound.Password != uuid {
				t.Fatal("Trojan credential changed")
			}
		case "wireguard":
			t.Fatal("WireGuard was unexpectedly admitted")
		}
	}
	if !found {
		t.Fatal("eligible Hysteria node missing")
	}
}

func TestGenerateSingboxDoesNotInventIneligibleNodes(t *testing.T) {
	// Given provisioning excluded all disabled/ineligible Hysteria nodes.
	data, err := GenerateSingbox(nil, "unused")
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		Outbounds []struct {
			Type string `json:"type"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatal(err)
	}
	for _, outbound := range profile.Outbounds {
		if outbound.Type == "hysteria2" {
			t.Fatal("generated a Hysteria node absent from eligible input")
		}
	}
}
