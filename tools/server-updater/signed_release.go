package main

import (
	"io"
	"os"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

const releasePolicyPath = "/etc/proxima-updater/release-policy.json"
const releasePath = "/etc/proxima-updater/release.json"

type approvedRelease struct {
	approval Approval
	verified releaseverify.Verified
}

// The caller supplies trusted state under the controller mutex/lifetime flock.
func verifyApprovedRelease(data []byte, state releaseverify.Context) (approvedRelease, error) {
	var policy releaseverify.Policy
	if err := readTrusted(releasePolicyPath, &policy); err != nil {
		return approvedRelease{}, err
	}
	approval, err := loadApproval(trustedApprovalFiles{})
	if err != nil {
		return approvedRelease{}, err
	}
	return verifyRelease(releaseInput{data, policy}, approval, state)
}

type releaseInput struct {
	data   []byte
	policy releaseverify.Policy
}

// Only this fixed, protected local path supplies the daemon's envelope.
func loadRelease() (releaseInput, error) {
	var input releaseInput
	if err := readTrusted(releasePolicyPath, &input.policy); err != nil {
		return input, err
	}
	if err := trustedPath(releasePath); err != nil {
		return input, err
	}
	f, err := os.Open(releasePath)
	if err != nil {
		return input, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return input, err
	}
	if !st.Mode().IsRegular() || st.Size() > releaseverify.MaxEnvelopeSize {
		return input, errInvalid
	}
	input.data, err = io.ReadAll(io.LimitReader(f, releaseverify.MaxEnvelopeSize+1))
	if err != nil {
		return input, err
	}
	if len(input.data) > releaseverify.MaxEnvelopeSize {
		return input, errInvalid
	}
	return input, nil
}

func verifyRelease(input releaseInput, approval Approval, state releaseverify.Context) (approvedRelease, error) {
	verifier, err := releaseverify.NewVerifier(input.policy)
	if err != nil {
		return approvedRelease{}, err
	}
	verified, err := verifier.Verify(input.data, state)
	if err != nil {
		return approvedRelease{}, err
	}
	if err := bindReleaseApproval(verified, approval); err != nil {
		return approvedRelease{}, err
	}
	return approvedRelease{approval: approval, verified: verified}, nil
}

func bindReleaseApproval(v releaseverify.Verified, a Approval) error {
	return v.Authorize(releaseverify.Approval{
		ID: a.ID, From: releaseverify.Images{API: a.From.API, Web: a.From.Web},
		To:                 releaseverify.Images{API: a.To.API, Web: a.To.Web},
		RollbackCompatible: a.RollbackCompatible,
	})
}
