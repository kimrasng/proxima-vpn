package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

const authorizationPath = "/etc/proxima-updater/authorization-policy.json"

type authorizationPolicy struct {
	Mode          string `json:"mode"`
	AllowRollback bool   `json:"allow_rollback"`
}

type trustedAuthorizationFiles struct{}

func (trustedAuthorizationFiles) Open(name string) (fs.File, error) {
	if name != "authorization-policy.json" {
		return nil, errInvalid
	}
	if err := trustedPath(authorizationPath); err != nil {
		return nil, err
	}
	return os.Open(authorizationPath)
}

func loadAuthorization(files fs.FS) (authorizationPolicy, error) {
	p := authorizationPolicy{Mode: "manual"}
	f, err := files.Open("authorization-policy.json")
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1024 {
		return p, errInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(data) > 1024 || strictJSON(data, &p) != nil {
		return p, errInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields["mode"] == nil || bytes.Equal(bytes.TrimSpace(fields["mode"]), []byte("null")) {
		return p, errInvalid
	}
	for key := range fields {
		if key != "mode" && key != "allow_rollback" {
			return p, errInvalid
		}
	}
	switch p.Mode {
	case "manual":
		if p.AllowRollback {
			return p, errInvalid
		}
	case "publisher_signed":
	default:
		return p, errInvalid
	}
	return p, nil
}

func (c *Controller) authorizationPolicy() (authorizationPolicy, error) {
	if c.authorization == nil {
		return authorizationPolicy{Mode: "manual"}, nil
	}
	return c.authorization()
}

func (c *Controller) approvedPublisherRelease(p authorizationPolicy) (approvedRelease, error) {
	if c.policy == nil || c.now == nil {
		return approvedRelease{}, errApproval
	}
	if _, err := os.Lstat(filepath.Join(c.store.dir, "recovery-required")); !errors.Is(err, os.ErrNotExist) {
		return approvedRelease{}, errBusy
	}
	policy, err := c.policy()
	if err != nil {
		return approvedRelease{}, err
	}
	data, err := c.store.preparedEnvelope()
	if err != nil {
		return approvedRelease{}, err
	}
	return publisherRelease(releaseInput{data: data, policy: policy}, c.state, p, releaseverify.Context{
		HighWater: c.state.HighWater, Current: releaseverify.Images(c.state.Current), Now: c.now()})
}

// Signature authentication is not permission. Only this separately provisioned
// root policy delegates transition authorization to the pinned publisher.
func publisherRelease(input releaseInput, state State, policy authorizationPolicy, now releaseverify.Context) (approvedRelease, error) {
	if policy.Mode != "publisher_signed" || input.policy != state.Policy || preparationState(state) != nil {
		return approvedRelease{}, errApproval
	}
	v, err := releaseverify.NewVerifier(input.policy)
	if err != nil {
		return approvedRelease{}, err
	}
	verified, err := v.Verify(input.data, now)
	if err != nil {
		return approvedRelease{}, err
	}
	m := verified.Manifest()
	for _, id := range state.Consumed {
		if id == m.ReleaseID {
			return approvedRelease{}, errApproval
		}
	}
	a := Approval{ID: m.ReleaseID, From: Images(m.From), To: Images(m.To),
		RollbackCompatible: policy.AllowRollback && m.RollbackCompatible}
	return approvedRelease{approval: a, verified: verified}, nil
}
