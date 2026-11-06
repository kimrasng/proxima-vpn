package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// ConnectionHistoryBindingSpec is private provisioning input, not an HTTP event
// or admin response. Generation is a lowercase SHA-256 fingerprint of the
// actual credential generation, never a raw password/key or the label alone.
// Binding.CredentialRef and Binding.BindingRevision must be zero on input.
type ConnectionHistoryBindingSpec struct {
	Binding    nodeprov.HistoryBinding
	Owner      ConnectionHistoryBindingOwner
	Generation string
}
type historyStoredBinding struct {
	binding    nodeprov.HistoryBinding
	owner      ConnectionHistoryBindingOwner
	generation *string
}

func historyBindingKey(b nodeprov.HistoryBinding) string {
	// JSON arrays avoid delimiter ambiguity in configured authentication labels.
	key, _ := json.Marshal([]string{b.Runtime, b.InboundTag, b.LabelKind, b.Label, b.Kind})
	return string(key)
}
func historySameOptional[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// ReconcileBindings atomically replaces the active private manifest. Unchanged
// generations retain their opaque refs; removed/rotated generations are revoked,
// never reassigned. One changed set increments the policy revision exactly once
// and preserves an ON epoch. Callers CAS the revision read from Config; a stale
// CAS returns 409 and does not revoke or issue anything.
func (s *ConnectionHistoryService) ReconcileBindings(ctx context.Context, node string, expected nodeprov.HistoryU64, desired []ConnectionHistoryBindingSpec) (ConnectionHistoryConfig, error) {
	var empty ConnectionHistoryConfig
	if desired == nil || len(desired) > 10000 {
		return empty, historyError(400, "invalid_manifest")
	}
	keys := make(map[string]bool, len(desired))
	for _, item := range desired {
		hash, err := hex.DecodeString(item.Generation)
		if err != nil || len(hash) != 32 || strings.ToLower(item.Generation) != item.Generation || item.Binding.CredentialRef != "" || item.Binding.BindingRevision != 0 {
			return empty, historyError(400, "invalid_generation")
		}
		b := item.Binding
		b.BindingRevision = 1
		b.CredentialRef, err = nodeprov.NewHistoryUUID()
		if err != nil {
			return empty, err
		}
		if err = b.Validate(); err != nil {
			return empty, historyError(400, "binding_invalid")
		}
		key := historyBindingKey(b)
		if keys[key] {
			return empty, historyError(400, "duplicate_binding")
		}
		keys[key] = true
		if b.Kind == "shared" && (item.Owner.UserID != nil || item.Owner.DeviceID != nil || item.Owner.UserSnapshot != nil || item.Owner.DeviceSnapshot != nil) {
			return empty, historyError(400, "shared_identity")
		}
		if b.Kind == "device" && (item.Owner.UserID == nil || item.Owner.DeviceID == nil) {
			return empty, historyError(400, "binding_owner_required")
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	if err = ensureHistoryControl(ctx, tx, node); err != nil {
		return empty, err
	}
	cfg, err := readHistoryConfig(ctx, tx, node)
	if err != nil {
		return cfg, err
	}
	if cfg.Revision != expected {
		return cfg, historyError(409, "revision_conflict")
	}
	rows, err := tx.Query(ctx, `SELECT manifest,user_id::text,device_id::text,user_snapshot,device_snapshot,generation FROM connection_log_bindings WHERE node_origin=$1 AND revoked_at IS NULL`, node)
	if err != nil {
		return cfg, err
	}
	active := make(map[string]historyStoredBinding)
	for rows.Next() {
		var old historyStoredBinding
		var raw []byte
		if err = rows.Scan(&raw, &old.owner.UserID, &old.owner.DeviceID, &old.owner.UserSnapshot, &old.owner.DeviceSnapshot, &old.generation); err != nil {
			rows.Close()
			return cfg, err
		}
		if err = json.Unmarshal(raw, &old.binding); err != nil {
			rows.Close()
			return cfg, err
		}
		active[historyBindingKey(old.binding)] = old
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return cfg, err
	}
	keep := make(map[string]bool, len(desired))
	var issue []ConnectionHistoryBindingSpec
	for _, item := range desired {
		if item.Binding.Kind == "device" {
			var valid bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id=$1 AND user_id=$2)`, item.Owner.DeviceID, item.Owner.UserID).Scan(&valid); err != nil {
				return cfg, err
			}
			if !valid {
				return cfg, historyError(400, "binding_owner_mismatch")
			}
		}
		key := historyBindingKey(item.Binding)
		old, exists := active[key]
		if exists && old.generation != nil && *old.generation == item.Generation &&
			historySameOptional(old.owner.UserID, item.Owner.UserID) && historySameOptional(old.owner.DeviceID, item.Owner.DeviceID) {
			keep[string(old.binding.CredentialRef)] = true
		} else {
			issue = append(issue, item)
		}
	}
	changed := len(issue) > 0 || len(keep) != len(active)
	if !changed {
		return cfg, tx.Commit(ctx)
	}
	if uint64(cfg.Revision) == ^uint64(0) {
		return cfg, historyError(409, "revision_exhausted")
	}
	cfg.Revision++
	for _, old := range active {
		if keep[string(old.binding.CredentialRef)] {
			continue
		}
		if _, err = tx.Exec(ctx, `UPDATE connection_log_bindings SET revoked_at=$3 WHERE node_origin=$1 AND credential_ref=$2`, node, string(old.binding.CredentialRef), s.now()); err != nil {
			return cfg, err
		}
	}
	for _, item := range issue {
		b := item.Binding
		b.BindingRevision = cfg.Revision
		b.CredentialRef, err = nodeprov.NewHistoryUUID()
		if err != nil {
			return cfg, err
		}
		raw, e := json.Marshal(b)
		if e != nil {
			return cfg, e
		}
		_, err = tx.Exec(ctx, `INSERT INTO connection_log_bindings(node_origin,credential_ref,binding_revision,manifest,generation,user_id,device_id,user_snapshot,device_snapshot,issued_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, node, string(b.CredentialRef), b.BindingRevision.String(), raw, item.Generation, item.Owner.UserID, item.Owner.DeviceID, item.Owner.UserSnapshot, item.Owner.DeviceSnapshot, s.now())
		if err != nil {
			return cfg, err
		}
	}
	if err = s.writeHistoryRevision(ctx, tx, node, cfg.Enabled, cfg.Revision, cfg.CollectionEpoch); err != nil {
		return cfg, err
	}
	cfg, err = readHistoryConfig(ctx, tx, node)
	if err != nil {
		return cfg, err
	}
	control, err := json.Marshal(nodeprov.HistoryApply{Op: "apply", ConfigRevision: cfg.Revision, CollectionEpoch: cfg.CollectionEpoch, Enabled: cfg.Enabled, Bindings: cfg.Bindings})
	if err != nil {
		return cfg, err
	}
	if len(control)+1 > nodeprov.HistoryMaxControlLine {
		return cfg, historyError(413, "manifest_too_large")
	}
	return cfg, tx.Commit(ctx)
}
