package xray

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStageHistoryRuntimeVerifiesArtifactBeforeReplacement(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong_checksum", "wrong_commit", "missing_capability", "stock_binary"} {
		t.Run(scenario, func(t *testing.T) {
			// Given an installed binary and a manifest for a real executable probe.
			dir := t.TempDir()
			destination := filepath.Join(dir, "installed")
			original := []byte("installed runtime must survive staging")
			if err := os.WriteFile(destination, original, 0o755); err != nil {
				t.Fatal(err)
			}
			hello := map[string]any{
				"op": "hello", "schema_version": 1, "producer_id": "xray",
				"observer_boot_id": "11111111-1111-4111-8111-111111111111",
				"runtime_build":    "xray-a4a3dff5-history-v1",
				"capabilities": []string{
					"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp",
				},
				"first_available_sequence": "1", "last_sequence": "0", "enabled": false,
				"config_revision": "0", "collection_epoch": nil,
			}
			if scenario == "missing_capability" {
				hello["capabilities"] = []string{"auth_transport", "hot_toggle", "tcp_flow", "udp_destination"}
			}
			response, err := json.Marshal(hello)
			if err != nil {
				t.Fatal(err)
			}
			executable := []byte("#!/bin/sh\n[ \"$1\" = history-capabilities ] || exit 2\nprintf '%s\\n' '" + string(response) + "'\n")
			if scenario == "stock_binary" {
				executable = []byte("#!/bin/sh\nexit 2\n")
			}
			artifact := filepath.Join(dir, "artifact")
			if err := os.WriteFile(artifact, executable, 0o755); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(executable)
			checksum := hex.EncodeToString(digest[:])
			commit := "a4a3dff5b20c2faae567b742c172c7b95e75c083"
			if scenario == "wrong_checksum" {
				checksum = "0000000000000000000000000000000000000000000000000000000000000000"
			}
			if scenario == "wrong_commit" {
				commit = "0000000000000000000000000000000000000000"
			}
			manifest, err := json.Marshal(map[string]any{
				"schema_version": 1,
				"artifacts": []map[string]string{{
					"runtime": "xray", "commit": commit, "version": "v26.10.31",
					"build": "xray-a4a3dff5-history-v1", "os": runtime.GOOS,
					"arch": runtime.GOARCH, "path": "artifact", "sha256": checksum,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
				t.Fatal(err)
			}

			// When staging validates the pin, checksum and executable capabilities.
			staged, err := StageHistoryRuntime(t.Context(), manifestPath, "xray", destination, "v26.10.31")

			// Then only a qualified artifact is staged; installation is untouched.
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(staged)
				if err != nil || !bytes.Equal(data, executable) {
					t.Fatalf("staged bytes differ: %v", err)
				}
			} else if err == nil || staged != "" {
				t.Fatalf("unqualified runtime accepted: path=%q err=%v", staged, err)
			}
			data, err := os.ReadFile(destination)
			if err != nil || !bytes.Equal(data, original) {
				t.Fatalf("installed binary changed: %v", err)
			}
			staging, err := filepath.Glob(filepath.Join(dir, "xray-history-*"))
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if scenario == "valid" {
				want = 1
			}
			if len(staging) != want {
				t.Fatalf("staging files=%v, want %d", staging, want)
			}
		})
	}
}
