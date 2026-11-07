package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

type preparationResponse struct {
	Status    string             `json:"status"`
	Tag       string             `json:"tag,omitempty"`
	Candidate *preparedCandidate `json:"candidate,omitempty"`
	Error     string             `json:"error,omitempty"`
}

func (c *Controller) preparationStatus() preparationResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.preparation.Status == "" {
		return preparationResponse{Status: "idle"}
	}
	return c.preparation
}

func (c *Controller) startPreparation(tag string) (preparationResponse, error) {
	if !releaseTagPattern.MatchString(tag) {
		return preparationResponse{}, errInvalid
	}
	if !c.mu.TryLock() {
		return preparationResponse{}, errBusy
	}
	defer c.mu.Unlock()
	p, err := c.authorizationPolicy()
	if err != nil || p.Mode != "publisher_signed" || c.policy == nil || c.fetch == nil || c.now == nil {
		return preparationResponse{}, errApproval
	}
	if c.active || c.blocked || preparationState(c.state) != nil {
		return preparationResponse{}, errBusy
	}
	if _, err := os.Lstat(filepath.Join(c.store.dir, "recovery-required")); !errors.Is(err, os.ErrNotExist) {
		return preparationResponse{}, errBusy
	}
	policy, err := c.policy()
	if err != nil || policy != c.state.Policy {
		return preparationResponse{}, errApproval
	}
	c.preparation = preparationResponse{Status: "preparing", Tag: tag}
	c.active, c.preparing = true, true
	c.done = make(chan struct{})
	state := c.state
	go c.runPreparation(tag, state, p)
	return c.preparation, nil
}

func (c *Controller) runPreparation(tag string, state State, authorization authorizationPolicy) {
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Minute)
	defer cancel()
	candidate, err := c.preparePublisher(ctx, tag, state, authorization)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.preparation = preparationResponse{Status: "failed", Tag: tag, Error: "preparation_failed"}
	if err == nil {
		c.preparation = preparationResponse{Status: "prepared", Tag: tag, Candidate: &candidate}
	}
	c.active, c.preparing = false, false
	close(c.done)
}

func (c *Controller) preparePublisher(ctx context.Context, tag string, state State, authorization authorizationPolicy) (preparedCandidate, error) {
	data, err := c.fetch(ctx, tag)
	if err != nil {
		return preparedCandidate{}, err
	}
	policy, err := c.policy()
	if err != nil {
		return preparedCandidate{}, err
	}
	currentAuthorization, err := c.authorizationPolicy()
	if err != nil || currentAuthorization != authorization {
		return preparedCandidate{}, errApproval
	}
	input := releaseInput{data: data, policy: policy}
	r, err := publisherRelease(input, state, authorization, releaseverify.Context{
		HighWater: state.HighWater, Current: releaseverify.Images(state.Current), Now: c.now()})
	if err != nil {
		return preparedCandidate{}, err
	}
	candidate, err := prepareImages(ctx, c.docker, input, r.approval, state, c.now)
	if err != nil {
		return preparedCandidate{}, err
	}
	currentPolicy, err := c.policy()
	if err != nil || currentPolicy != policy {
		return preparedCandidate{}, errApproval
	}
	currentAuthorization, err = c.authorizationPolicy()
	if err != nil || currentAuthorization != authorization {
		return preparedCandidate{}, errApproval
	}
	if _, err := preparationRelease(input, r.approval, state, c.now()); err != nil {
		return preparedCandidate{}, err
	}
	if err := ctx.Err(); err != nil {
		return preparedCandidate{}, err
	}
	if err := atomicWriteSynced(filepath.Join(c.store.dir, preparedEnvelopeName), data, c.store.sync); err != nil {
		// Rename may have succeeded; never expose an uncertain candidate in this
		// process. Restart reloads and re-verifies the protected envelope.
		c.mu.Lock()
		c.blocked = true
		c.mu.Unlock()
		return preparedCandidate{}, err
	}
	return candidate, nil
}
