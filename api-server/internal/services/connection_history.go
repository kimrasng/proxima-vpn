package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

const (
	ConnectionHistoryRetention        = 30 * 24 * time.Hour
	ConnectionHistoryReplayAge        = 7 * 24 * time.Hour
	ConnectionHistoryReceiptRetention = 38 * 24 * time.Hour
	ConnectionHistoryMaxBatchBytes    = 1 << 20
	ConnectionHistoryMaxBatchEvents   = 1000
)

type ConnectionHistoryError struct {
	Status int               `json:"-"`
	Code   string            `json:"error"`
	Nodes  map[string]string `json:"nodes,omitempty"`
}

func (e *ConnectionHistoryError) Error() string { return e.Code }
func historyError(status int, code string) error {
	return &ConnectionHistoryError{Status: status, Code: code}
}

type ConnectionHistoryService struct {
	db  *pgxpool.Pool
	now func() time.Time
}

func NewConnectionHistoryService(db *pgxpool.Pool) *ConnectionHistoryService {
	return &ConnectionHistoryService{db: db, now: time.Now}
}

type ConnectionHistoryConfig struct {
	SchemaVersion   int                       `json:"schema_version"`
	Enabled         bool                      `json:"enabled"`
	Revision        nodeprov.HistoryU64       `json:"revision"`
	CollectionEpoch *nodeprov.HistoryUUID     `json:"collection_epoch"`
	Bindings        []nodeprov.HistoryBinding `json:"bindings"`
}

func ensureHistoryControl(ctx context.Context, tx pgx.Tx, node string) error {
	tag, err := tx.Exec(ctx, `INSERT INTO connection_log_controls(node_origin,node_id,node_snapshot)
 SELECT id,id,name FROM nodes WHERE id=$1 ON CONFLICT DO NOTHING`, node)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var live bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM nodes WHERE id=$1)`, node).Scan(&live); err != nil {
			return err
		}
		if !live {
			return historyError(404, "node_not_found")
		}
	}
	return nil
}

func readHistoryConfig(ctx context.Context, tx pgx.Tx, node string) (ConnectionHistoryConfig, error) {
	cfg := ConnectionHistoryConfig{SchemaVersion: 1, Bindings: []nodeprov.HistoryBinding{}}
	var rev string
	if err := tx.QueryRow(ctx, `SELECT enabled,revision::text,collection_epoch::text FROM connection_log_controls WHERE node_origin=$1 FOR UPDATE`, node).
		Scan(&cfg.Enabled, &rev, &cfg.CollectionEpoch); err != nil {
		return cfg, err
	}
	if err := json.Unmarshal([]byte(`"`+rev+`"`), &cfg.Revision); err != nil {
		return cfg, err
	}
	rows, err := tx.Query(ctx, `SELECT manifest FROM connection_log_bindings WHERE node_origin=$1 AND revoked_at IS NULL ORDER BY credential_ref`, node)
	if err != nil {
		return cfg, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var b nodeprov.HistoryBinding
		if err = rows.Scan(&raw); err != nil {
			return cfg, err
		}
		if err = json.Unmarshal(raw, &b); err != nil {
			return cfg, err
		}
		cfg.Bindings = append(cfg.Bindings, b)
	}
	return cfg, rows.Err()
}

func (s *ConnectionHistoryService) Config(ctx context.Context, node string) (ConnectionHistoryConfig, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ConnectionHistoryConfig{}, err
	}
	defer tx.Rollback(ctx)
	if err = ensureHistoryControl(ctx, tx, node); err != nil {
		return ConnectionHistoryConfig{}, err
	}
	cfg, err := readHistoryConfig(ctx, tx, node)
	if err != nil {
		return cfg, err
	}
	if _, err = tx.Exec(ctx, `UPDATE connection_log_controls SET last_config_fetch_at=$2 WHERE node_origin=$1`, node, s.now()); err != nil {
		return cfg, err
	}
	return cfg, tx.Commit(ctx)
}

// ConnectionHistoryBindingOwner contains display snapshots, never credentials.
type ConnectionHistoryBindingOwner struct {
	UserID         *string
	DeviceID       *string
	UserSnapshot   *string
	DeviceSnapshot *string
}

// IssueBinding allocates an immutable opaque generation and a new desired
// revision. Rotation revokes the previous ref explicitly; old owners retain it.
func (s *ConnectionHistoryService) IssueBinding(ctx context.Context, node string, expected nodeprov.HistoryU64, b nodeprov.HistoryBinding, owner ConnectionHistoryBindingOwner) (nodeprov.HistoryBinding, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return b, err
	}
	defer tx.Rollback(ctx)
	if err = ensureHistoryControl(ctx, tx, node); err != nil {
		return b, err
	}
	cfg, err := readHistoryConfig(ctx, tx, node)
	if err != nil {
		return b, err
	}
	if cfg.Revision != expected || uint64(expected) == ^uint64(0) {
		return b, historyError(409, "revision_conflict")
	}
	for _, active := range cfg.Bindings {
		if historyBindingKey(active) == historyBindingKey(b) {
			return b, historyError(409, "binding_already_active")
		}
	}
	b.CredentialRef, err = nodeprov.NewHistoryUUID()
	if err != nil {
		return b, err
	}
	b.BindingRevision = expected + 1
	if err = b.Validate(); err != nil {
		return b, historyError(400, "binding_invalid")
	}
	if b.Kind == "shared" && (owner.UserID != nil || owner.DeviceID != nil || owner.UserSnapshot != nil || owner.DeviceSnapshot != nil) {
		return b, historyError(400, "shared_identity")
	}
	if b.Kind == "device" && (owner.UserID == nil || owner.DeviceID == nil) {
		return b, historyError(400, "binding_owner_required")
	}
	if b.Kind == "device" {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id=$1 AND user_id=$2)`, owner.DeviceID, owner.UserID).Scan(&valid); err != nil {
			return b, err
		}
		if !valid {
			return b, historyError(400, "binding_owner_mismatch")
		}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return b, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO connection_log_bindings(node_origin,credential_ref,binding_revision,manifest,user_id,device_id,user_snapshot,device_snapshot,issued_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, node, string(b.CredentialRef), b.BindingRevision.String(), raw, owner.UserID, owner.DeviceID, owner.UserSnapshot, owner.DeviceSnapshot, s.now())
	if err != nil {
		return b, err
	}
	if err = s.writeHistoryRevision(ctx, tx, node, cfg.Enabled, b.BindingRevision, cfg.CollectionEpoch); err != nil {
		return b, err
	}
	return b, tx.Commit(ctx)
}

func (s *ConnectionHistoryService) writeHistoryRevision(ctx context.Context, tx pgx.Tx, node string, enabled bool, rev nodeprov.HistoryU64, epoch *nodeprov.HistoryUUID) error {
	_, err := tx.Exec(ctx, `UPDATE connection_log_controls SET enabled=$2,revision=$3,collection_epoch=$4,state='pending' WHERE node_origin=$1`, node, enabled, rev.String(), epoch)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO connection_log_revisions(node_origin,revision,epoch,enabled,issued_at) VALUES($1,$2,$3,$4,$5)`, node, rev.String(), epoch, enabled, s.now())
	return err
}

func (s *ConnectionHistoryService) RevokeBinding(ctx context.Context, node string, ref nodeprov.HistoryUUID, expected nodeprov.HistoryU64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cfg, err := readHistoryConfig(ctx, tx, node)
	if err != nil {
		return err
	}
	if cfg.Revision != expected || uint64(expected) == ^uint64(0) {
		return historyError(409, "revision_conflict")
	}
	tag, err := tx.Exec(ctx, `UPDATE connection_log_bindings SET revoked_at=$3 WHERE node_origin=$1 AND credential_ref=$2 AND revoked_at IS NULL`, node, string(ref), s.now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return historyError(404, "binding_not_found")
	}
	if err = s.writeHistoryRevision(ctx, tx, node, cfg.Enabled, expected+1, cfg.CollectionEpoch); err != nil {
		return fmt.Errorf("revoke binding revision: %w", err)
	}
	return tx.Commit(ctx)
}
