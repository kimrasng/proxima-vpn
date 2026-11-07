package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

type preparedCandidate struct {
	Status        string `json:"status"`
	ReleaseID     string `json:"release_id"`
	Sequence      uint64 `json:"sequence"`
	PayloadSHA256 string `json:"payload_sha256"`
	To            Images `json:"to"`
	ExpiresAt     int64  `json:"expires_at"`
}

// Unlike Store.load, preparation must never convert or save recovery state.
// The caller owns the daemon's lifetime flock throughout this operation.
func readPreparationState(dir string) (State, error) {
	var state State
	if _, err := os.Lstat(filepath.Join(dir, "recovery-required")); !errors.Is(err, os.ErrNotExist) {
		return state, errBusy
	}
	path := filepath.Join(dir, "state.json")
	if err := trustedPath(path); err != nil {
		return state, err
	}
	f, err := os.Open(path)
	if err != nil {
		return state, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return state, err
	}
	// The existing 10,000-entry ledger can exceed the config-file limit.
	const maxJournalBytes = 32 * 1024 * 1024
	if !st.Mode().IsRegular() || st.Size() > maxJournalBytes {
		return state, errInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, maxJournalBytes+1))
	if err != nil {
		return state, err
	}
	if len(data) > maxJournalBytes || strictJSON(data, &state) != nil {
		return state, errInvalid
	}
	return state, preparationState(state)
}

func preparationState(state State) error {
	if state.validateSigned() != nil || !state.CurrentKnown || len(state.Consumed) >= 10000 {
		return errBusy
	}
	if op := state.Operation; op != nil && (op.Maintenance || (op.Phase != "succeeded" && op.Phase != "rolled_back")) {
		return errBusy
	}
	return nil
}

func preparationRelease(input releaseInput, a Approval, state State, now time.Time) (approvedRelease, error) {
	if err := preparationState(state); err != nil {
		return approvedRelease{}, err
	}
	if input.policy != state.Policy {
		return approvedRelease{}, errApproval
	}
	for _, id := range state.Consumed {
		if id == a.ID {
			return approvedRelease{}, errApproval
		}
	}
	return verifyRelease(input, a, releaseverify.Context{HighWater: state.HighWater, Current: releaseverify.Images(state.Current), Now: now})
}

// Pulling caches approved content only. It never consumes a release or changes
// a container; apply performs its own verification and durable acceptance.
func prepareImages(ctx context.Context, d Docker, input releaseInput, a Approval, state State, now func() time.Time) (preparedCandidate, error) {
	r, err := preparationRelease(input, a, state, now())
	if err != nil {
		return preparedCandidate{}, err
	}
	if err := d.verify(ctx, a.From, true); err != nil {
		return preparedCandidate{}, err
	}
	for _, ref := range []string{r.approval.To.API, r.approval.To.Web} {
		if _, err := d.executor.Run(ctx, dockerBinary, "image", "pull", ref); err != nil {
			return preparedCandidate{}, fmt.Errorf("pull approved image: %w", err)
		}
	}
	if err := d.preflight(ctx, r.approval); err != nil {
		return preparedCandidate{}, err
	}
	if _, err := preparationRelease(input, a, state, now()); err != nil {
		return preparedCandidate{}, err
	}
	m, digest := r.verified.Manifest(), r.verified.PayloadSHA256()
	return preparedCandidate{Status: "prepared", ReleaseID: m.ReleaseID, Sequence: m.Sequence,
		PayloadSHA256: hex.EncodeToString(digest[:]), To: a.To, ExpiresAt: m.ExpiresAt}, nil
}

// All paths and network authority are fixed. Root provisioning must not race
// this command. The lifetime flock is acquired by run before entering here.
func prepareHostRelease(ctx context.Context, cfg Config, tag string, out io.Writer) error {
	if !releaseTagPattern.MatchString(tag) {
		return errInvalid
	}
	state, err := readPreparationState(stateDir)
	if err != nil {
		return err
	}
	var policy releaseverify.Policy
	if err := readTrusted(releasePolicyPath, &policy); err != nil {
		return err
	}
	if policy != state.Policy {
		return errApproval
	}
	a, err := loadApproval(trustedApprovalFiles{})
	if err != nil {
		return err
	}
	if err := trustedPath(filepath.Dir(releasePath)); err != nil {
		return err
	}
	if _, err := os.Lstat(releasePath); err == nil {
		if err := trustedPath(releasePath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	client := newReleaseHTTPClient()
	defer client.CloseIdleConnections()
	data, err := fetchRelease(ctx, client, tag)
	if err != nil {
		return err
	}
	input := releaseInput{data: data, policy: policy}
	candidate, err := prepareImages(ctx, Docker{cfg, commandExecutor{stateDir}, stateDir}, input, a, state, time.Now)
	if err != nil {
		return err
	}
	// Reject policy/approval replacement during a potentially long image pull.
	var currentPolicy releaseverify.Policy
	if err := readTrusted(releasePolicyPath, &currentPolicy); err != nil {
		return err
	}
	currentApproval, err := loadApproval(trustedApprovalFiles{})
	if err != nil {
		return err
	}
	if currentPolicy != policy || currentApproval != a {
		return errApproval
	}
	if _, err := preparationRelease(input, a, state, time.Now()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := atomicWrite(releasePath, data); err != nil {
		return fmt.Errorf("candidate installation durability uncertain: %w", err)
	}
	return json.NewEncoder(out).Encode(candidate)
}
