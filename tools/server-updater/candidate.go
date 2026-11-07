package main

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// candidateResponse is an observation, never authorization or a reservation.
// Keep it separate from the CLI receipt and the signed envelope/journal.
type candidateResponse struct {
	Status             string         `json:"status"`
	ReleaseID          string         `json:"release_id"`
	Sequence           uint64         `json:"sequence"`
	From               imagesResponse `json:"from"`
	To                 imagesResponse `json:"to"`
	RollbackCompatible bool           `json:"rollback_compatible"`
	ExpiresAt          int64          `json:"expires_at"`
	PayloadSHA256      string         `json:"payload_sha256,omitempty"`
}

func (c *Controller) candidate(ctx context.Context) (candidateResponse, error) {
	// Do not queue observations behind another preflight or an acceptance.
	if !c.mu.TryLock() {
		return candidateResponse{}, errBusy
	}
	defer c.mu.Unlock()
	if c.active || c.blocked || preparationState(c.state) != nil {
		return candidateResponse{}, errBusy
	}
	if _, err := os.Lstat(filepath.Join(c.store.dir, "recovery-required")); !errors.Is(err, os.ErrNotExist) {
		return candidateResponse{}, errBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return candidateResponse{}, err
	}
	r, err := c.approvedRelease()
	if err != nil || slices.Contains(c.state.Consumed, r.approval.ID) {
		return candidateResponse{}, errApproval
	}
	if err := c.docker.preflight(ctx, r.approval); err != nil {
		return candidateResponse{}, err
	}
	// Inspection can cross expiry or root provisioning. Recheck the complete
	// signed identity, not merely the public subset of manifest fields.
	current, err := c.approvedRelease()
	if err != nil || current.approval != r.approval || current.verified.PayloadSHA256() != r.verified.PayloadSHA256() {
		return candidateResponse{}, errApproval
	}
	if err := ctx.Err(); err != nil {
		return candidateResponse{}, err
	}
	m, a := r.verified.Manifest(), r.approval
	var payloadHash string
	policy, err := c.authorizationPolicy()
	if err != nil {
		return candidateResponse{}, errApproval
	}
	if policy.Mode == "publisher_signed" {
		digest := r.verified.PayloadSHA256()
		payloadHash = hex.EncodeToString(digest[:])
	}
	return candidateResponse{Status: "prepared", ReleaseID: a.ID, Sequence: m.Sequence,
		From: imagesResponse{API: a.From.API, Web: a.From.Web}, To: imagesResponse{API: a.To.API, Web: a.To.Web},
		RollbackCompatible: a.RollbackCompatible, ExpiresAt: m.ExpiresAt, PayloadSHA256: payloadHash}, nil
}
