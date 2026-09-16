package main

import (
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

// A config shaped like the ones GenerateConfig emits: the stats api inbound,
// one VLESS Reality inbound with two clients, and non-VLESS inbounds whose
// clients must not be mistaken for incrementally-manageable users.
const configWithTwoVlessUsers = `{
  "inbounds": [
    {"listen":"127.0.0.1","port":10085,"protocol":"dokodemo-door","tag":"api","settings":{"address":"127.0.0.1"}},
    {"port":8443,"protocol":"vless","tag":"vless-in","settings":{"clients":[
      {"id":"uuid-a","email":"uuid-a@proxima","flow":"xtls-rprx-vision","level":0},
      {"id":"uuid-b","email":"uuid-b@proxima","flow":"xtls-rprx-vision","level":0}
    ],"decryption":"none"}},
    {"port":8444,"protocol":"vmess","tag":"vmess-in","settings":{"clients":[
      {"id":"uuid-a","email":"uuid-a@proxima-vmess","level":0}
    ]}},
    {"port":8446,"protocol":"shadowsocks","tag":"ss-in","settings":{"method":"2022-blake3-aes-128-gcm","password":"pw"}}
  ]
}`

// Regression: newNodeState used to start with an empty user set, so the first
// incremental sync re-added users the running Xray already had. Xray rejects a
// duplicate email ("User ... already exists"), which failed the sync and forced
// the restart that live sync exists to avoid.
func TestNewNodeStateSeedsUsersFromConfig(t *testing.T) {
	state := newNodeState([]byte(configWithTwoVlessUsers))

	users := state.UsersSnapshot()
	if len(users) != 2 {
		t.Fatalf("expected 2 seeded users, got %d: %v", len(users), users)
	}

	want := xray.VLESSUser{UUID: "uuid-a", Email: "uuid-a@proxima", Flow: "xtls-rprx-vision", Level: 0}
	got, ok := users[userKey{InboundTag: "vless-in", Email: "uuid-a@proxima"}]
	if !ok {
		t.Fatalf("vless-in/uuid-a@proxima missing from seeded set: %v", users)
	}
	if got != want {
		t.Errorf("seeded user mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// Only VLESS clients are manageable over AlterInbound, so a vmess client under
// the same UUID must not enter the set - syncUsers would try to remove it from
// an inbound that cannot accept the operation.
func TestNewNodeStateIgnoresNonVlessClients(t *testing.T) {
	state := newNodeState([]byte(configWithTwoVlessUsers))

	for key := range state.UsersSnapshot() {
		if key.InboundTag != "vless-in" {
			t.Errorf("non-vless inbound %q leaked into the user set", key.InboundTag)
		}
	}
}

// The seeded set must not be mistaken for a set already reconciled against a
// digest: usersHash stays empty so the first poll still diffs it.
func TestNewNodeStateLeavesUsersHashEmpty(t *testing.T) {
	state := newNodeState([]byte(configWithTwoVlessUsers))

	if h := state.UsersHash(); h != "" {
		t.Errorf("expected an empty users hash on a fresh state, got %q", h)
	}
	if h := state.StructureHash(); h != "" {
		t.Errorf("expected an empty structure hash on a fresh state, got %q", h)
	}
}

func TestVlessUsersOfConfigToleratesGarbage(t *testing.T) {
	for name, cfg := range map[string]string{
		"invalid json":  `{not json`,
		"no inbounds":   `{"log":{"loglevel":"warning"}}`,
		"empty clients": `{"inbounds":[{"protocol":"vless","tag":"t","settings":{"clients":[]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if users := vlessUsersOfConfig([]byte(cfg)); len(users) != 0 {
				t.Errorf("expected no users, got %v", users)
			}
		})
	}
}

// A digest whose user set matches what was seeded from the config must read as
// "nothing to do", which is what keeps a steady-state node from churning.
func TestSeededStateMatchesEquivalentDigest(t *testing.T) {
	state := newNodeState([]byte(configWithTwoVlessUsers))

	digest := client.ConfigDigest{
		Users: []client.ConfigDigestUser{
			{InboundTag: "vless-in", UUID: "uuid-a", Email: "uuid-a@proxima", Flow: "xtls-rprx-vision", Level: 0},
			{InboundTag: "vless-in", UUID: "uuid-b", Email: "uuid-b@proxima", Flow: "xtls-rprx-vision", Level: 0},
		},
	}

	seeded := state.UsersSnapshot()
	fromDigest := usersFromDigest(digest)

	if len(seeded) != len(fromDigest) {
		t.Fatalf("size mismatch: seeded %d, digest %d", len(seeded), len(fromDigest))
	}
	for key, want := range fromDigest {
		got, ok := seeded[key]
		if !ok {
			t.Errorf("%v present in digest but not in the seeded set", key)
			continue
		}
		if got != want {
			t.Errorf("%v differs:\n seeded %+v\n digest %+v", key, got, want)
		}
	}
}

// Regression: a fresh agent starts from a config, not a digest, so it has no
// structure hash - and usersOnlyChange reads an empty one as "unknown" and
// demands a restart. Until the hash is learned, every startup's first user
// change restarted Xray.
func TestStructureHashLearnedFromMatchingDigestEnablesIncrementalSync(t *testing.T) {
	state := newNodeState([]byte(configWithTwoVlessUsers))

	if usersOnlyChange(state, client.ConfigDigest{
		StructureHash: "structure-1",
		UsersHash:     "users-2",
	}) {
		t.Fatal("an unknown local structure hash must not be treated as a users-only change")
	}

	state.setStructureHash("structure-1")

	if !usersOnlyChange(state, client.ConfigDigest{
		StructureHash: "structure-1",
		UsersHash:     "users-2",
	}) {
		t.Error("a user change under an unchanged structure must take the incremental path")
	}

	if usersOnlyChange(state, client.ConfigDigest{
		StructureHash: "structure-2",
		UsersHash:     "users-2",
	}) {
		t.Error("a structural change must still force a restart")
	}
}

func TestSetStructureHashLeavesConfigAndUsersAlone(t *testing.T) {
	state := newNodeState([]byte(configWithTwoVlessUsers))
	hashBefore := state.ConfigHash()
	usersBefore := len(state.UsersSnapshot())

	state.setStructureHash("structure-1")

	if got := state.ConfigHash(); got != hashBefore {
		t.Errorf("config hash changed: %q -> %q", hashBefore, got)
	}
	if got := len(state.UsersSnapshot()); got != usersBefore {
		t.Errorf("user count changed: %d -> %d", usersBefore, got)
	}
	if got := state.StructureHash(); got != "structure-1" {
		t.Errorf("structure hash = %q, want %q", got, "structure-1")
	}
}
