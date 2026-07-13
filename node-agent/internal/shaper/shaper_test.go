package shaper

import (
	"strings"
	"testing"
)

func TestTiersFromConfig(t *testing.T) {
	config := []byte(`{
		"inbounds": [
			{"port": 443, "tag": "vless-reality"},
			{"port": 20050, "tag": "vless-reality-limit-50"},
			{"port": 20010, "tag": "vless-reality-limit-10"},
			{"port": 10085, "tag": "api"}
		]
	}`)

	tiers := TiersFromConfig(config)
	if len(tiers) != 2 {
		t.Fatalf("expected 2 tiers, got %d", len(tiers))
	}
	// Sorted by port.
	if tiers[0].Port != 20010 || tiers[0].Mbps != 10 {
		t.Errorf("unexpected first tier: %+v", tiers[0])
	}
	if tiers[1].Port != 20050 || tiers[1].Mbps != 50 {
		t.Errorf("unexpected second tier: %+v", tiers[1])
	}
}

func TestTiersFromConfigNone(t *testing.T) {
	config := []byte(`{"inbounds": [{"port": 443, "tag": "vless-reality"}]}`)
	if tiers := TiersFromConfig(config); len(tiers) != 0 {
		t.Fatalf("expected no tiers, got %d", len(tiers))
	}
}

func TestApplyBuildsTcCommands(t *testing.T) {
	var cmds [][]string
	orig := runCmd
	runCmd = func(name string, args ...string) error {
		cmds = append(cmds, append([]string{name}, args...))
		return nil
	}
	defer func() { runCmd = orig }()

	tiers := []Tier{{Port: 20010, Mbps: 10}, {Port: 20050, Mbps: 50}}
	if err := Apply("eth0", tiers); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	joined := make([]string, len(cmds))
	for i, c := range cmds {
		joined[i] = strings.Join(c, " ")
	}
	all := strings.Join(joined, "\n")

	// Root qdisc reset + creation.
	if !strings.Contains(all, "tc qdisc del dev eth0 root") {
		t.Errorf("expected root qdisc delete, got:\n%s", all)
	}
	if !strings.Contains(all, "tc qdisc add dev eth0 root handle 1: htb default 30") {
		t.Errorf("expected root qdisc add, got:\n%s", all)
	}
	// Per-tier rate classes.
	if !strings.Contains(all, "rate 10mbit ceil 10mbit") {
		t.Errorf("expected 10mbit class, got:\n%s", all)
	}
	if !strings.Contains(all, "rate 50mbit ceil 50mbit") {
		t.Errorf("expected 50mbit class, got:\n%s", all)
	}
	// Filters matching source ports.
	if !strings.Contains(all, "match ip sport 20010 0xffff") {
		t.Errorf("expected filter for port 20010, got:\n%s", all)
	}
	if !strings.Contains(all, "match ip sport 20050 0xffff") {
		t.Errorf("expected filter for port 20050, got:\n%s", all)
	}
}

func TestApplyNoTiersOnlyResets(t *testing.T) {
	var cmds [][]string
	orig := runCmd
	runCmd = func(name string, args ...string) error {
		cmds = append(cmds, append([]string{name}, args...))
		return nil
	}
	defer func() { runCmd = orig }()

	if err := Apply("eth0", nil); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected only the reset command, got %d commands", len(cmds))
	}
	if strings.Join(cmds[0], " ") != "tc qdisc del dev eth0 root" {
		t.Errorf("unexpected reset command: %v", cmds[0])
	}
}
