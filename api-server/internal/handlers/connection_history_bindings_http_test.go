package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func historyGeneration(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
func TestConnectionHistoryHTTPBindingReconciliationRotationAndCAS(t *testing.T) {
	// Given: a generated device credential and shared inbound whose label stays
	// unchanged when its actual secret generation rotates.
	f := historyFixture(t)
	user, device := "retained-user", "retained-device"
	desired := []services.ConnectionHistoryBindingSpec{
		{Binding: nodeprov.HistoryBinding{Runtime: "xray", InboundTag: "vless-test", LabelKind: "xray_email", Label: "private-configured-label", Kind: "device"},
			Owner: services.ConnectionHistoryBindingOwner{UserID: &f.user, DeviceID: &f.device, UserSnapshot: &user, DeviceSnapshot: &device}, Generation: historyGeneration("device-generation-one")},
		{Binding: nodeprov.HistoryBinding{Runtime: "xray", InboundTag: "ss-shared", LabelKind: "shared_inbound", Label: "ss-shared", Kind: "shared"}, Generation: historyGeneration("shared-generation-one")},
	}
	cfg, err := f.svc.ReconcileBindings(context.Background(), f.node, 0, desired)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Revision != 1 || len(cfg.Bindings) != 2 {
		t.Fatalf("one atomic revision required: %+v", cfg)
	}
	var deviceRef, sharedRef nodeprov.HistoryBinding
	for _, binding := range cfg.Bindings {
		if binding.Kind == "shared" {
			sharedRef = binding
		} else {
			deviceRef = binding
		}
	}
	f.status(f.node)
	f.request("POST", "/api/v1/admin/nodes/connection-logging", services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node), ExpectedRevision: 1}}, Enabled: true}, "", 200)
	cfg, err = f.svc.ReconcileBindings(context.Background(), f.node, 2, desired)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Revision != 2 {
		t.Fatal("unchanged manifest bumped revision")
	}
	oldEpoch := *cfg.CollectionEpoch
	desired[1].Generation = historyGeneration("shared-generation-two")
	// When: the full derived manifest is reconciled with a changed generation.
	cfg, err = f.svc.ReconcileBindings(context.Background(), f.node, 2, desired)
	if err != nil {
		t.Fatal(err)
	}
	// Then: exactly one revision changes, only the shared ref rotates, epoch stays.
	if cfg.Revision != 3 || *cfg.CollectionEpoch != oldEpoch {
		t.Fatalf("policy changed incorrectly: %+v", cfg)
	}
	for _, binding := range cfg.Bindings {
		if binding.Kind == "device" && binding.CredentialRef != deviceRef.CredentialRef {
			t.Fatal("unchanged generation ref rotated")
		}
		if binding.Kind == "shared" && (binding.CredentialRef == sharedRef.CredentialRef || binding.BindingRevision != 3) {
			t.Fatal("shared password generation was reused")
		}
	}
	before := f.dbState()
	_, err = f.svc.ReconcileBindings(context.Background(), f.node, 2, desired)
	var conflict *services.ConnectionHistoryError
	if !errors.As(err, &conflict) || conflict.Status != 409 {
		t.Fatalf("stale CAS=%v", err)
	}
	if before != f.dbState() {
		t.Fatal("stale reconciliation mutated state")
	}
	f.config = cfg
	f.binding = deviceRef
	old := f.event(1, "flow_end", 1, 211, 431)
	old.ConfigRevision = 2
	old.Protocol = historyPtr("shadowsocks")
	old.InboundTag = historyPtr("ss-shared")
	old.Auth = nodeprov.HistoryAuth{Kind: "shared", CredentialRef: &sharedRef.CredentialRef, BindingRevision: &sharedRef.BindingRevision}
	old.Quality.Identity = "unknown"
	f.ingest(f.batch(old), 200)
	body := f.request("GET", "/api/v1/nodes/"+f.node+"/connection-history/config", nil, "history-key-0", 200)
	if bytes.Contains(body, []byte(`"generation"`)) || bytes.Contains(body, []byte(desired[1].Generation)) {
		t.Fatalf("generation leaked: %s", body)
	}
	cfg, err = f.svc.ReconcileBindings(context.Background(), f.node, 3, []services.ConnectionHistoryBindingSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Revision != 4 || len(cfg.Bindings) != 0 || *cfg.CollectionEpoch != oldEpoch {
		t.Fatalf("removal=%+v", cfg)
	}
	var active, retained int
	if err = f.db.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE revoked_at IS NULL),count(*) FROM connection_log_bindings WHERE node_origin=$1`, f.node).Scan(&active, &retained); err != nil || active != 0 || retained != 3 {
		t.Fatalf("bindings active=%d retained=%d err=%v", active, retained, err)
	}
}
