package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

type Operation struct {
	Approval           Approval `json:"approval"`
	Phase              string   `json:"phase"`
	Maintenance        bool     `json:"maintenance"`
	MigrationUncertain bool     `json:"migration_uncertain"`
	Error              string   `json:"error,omitempty"`
	Sequence           uint64   `json:"sequence"`
	PayloadSHA256      string   `json:"payload_sha256"`
}

type AcceptedRelease struct {
	Approval      Approval `json:"approval"`
	Sequence      uint64   `json:"sequence"`
	PayloadSHA256 string   `json:"payload_sha256"`
}

type State struct {
	Version      int                  `json:"version"`
	Consumed     []string             `json:"consumed"`
	Operation    *Operation           `json:"operation"`
	Product      string               `json:"product"`
	Policy       releaseverify.Policy `json:"policy"`
	Current      Images               `json:"current"`
	CurrentKnown bool                 `json:"current_known"`
	HighWater    uint64               `json:"high_water"`
	Accepted     []AcceptedRelease    `json:"accepted"`
}

// Version 1 is readable only for status/manual migration, never signed apply.
func (s State) validateSigned() error {
	if s.Version != 2 || s.Product != "proxima-vpn" || s.HighWater == 0 || s.HighWater > releaseverify.MaxSequence || len(s.Consumed) > 10000 || len(s.Accepted) != len(s.Consumed) {
		return errInvalid
	}
	if _, err := releaseverify.NewVerifier(s.Policy); err != nil {
		return err
	}
	validPair := func(images Images) bool {
		return imagePattern.MatchString(images.API) && imagePattern.MatchString(images.Web) &&
			strings.HasPrefix(images.API, s.Policy.APIRepository+"@sha256:") && strings.HasPrefix(images.Web, s.Policy.WebRepository+"@sha256:")
	}
	if !validPair(s.Current) {
		return errInvalid
	}
	seen := make(map[string]bool)
	var last uint64
	for i, a := range s.Accepted {
		if a.Approval.validate() != nil || !validPair(a.Approval.From) || !validPair(a.Approval.To) || a.Approval.ID != s.Consumed[i] || seen[a.Approval.ID] || a.Sequence <= last || a.Sequence > s.HighWater || !imageID.MatchString("sha256:"+a.PayloadSHA256) {
			return errInvalid
		}
		seen[a.Approval.ID], last = true, a.Sequence
	}
	if len(s.Accepted) > 0 && last != s.HighWater {
		return errInvalid
	}
	if s.Operation == nil {
		// Explicit root enrollment/reconciliation may clear the operation,
		// but must retain every acceptance and the high-water.
		return nil
	}
	op := s.Operation
	if len(s.Accepted) == 0 || s.Accepted[len(s.Accepted)-1] != (AcceptedRelease{op.Approval, op.Sequence, op.PayloadSHA256}) {
		return errInvalid
	}
	switch op.Phase {
	case "accepted":
		if !s.CurrentKnown || s.Current != op.Approval.From {
			return errInvalid
		}
	case "succeeded":
		if !s.CurrentKnown || s.Current != op.Approval.To {
			return errInvalid
		}
	case "rolled_back":
		if !s.CurrentKnown || s.Current != op.Approval.From {
			return errInvalid
		}
	case "rejected":
		if s.Current != op.Approval.From {
			return errInvalid
		}
	case "stopping", "applying", "rolling_back", "manual_recovery":
		if s.CurrentKnown {
			return errInvalid
		}
	default:
		return errInvalid
	}
	return nil
}

func terminal(phase string) bool {
	switch phase {
	case "succeeded", "rolled_back", "rejected", "manual_recovery":
		return true
	default:
		return false
	}
}

func atomicWrite(path string, data []byte) error {
	return atomicWriteSynced(path, data, syncDirectory)
}

func atomicWriteSynced(path string, data []byte, syncDir func(string) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".updater-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

type Store struct {
	dir     string
	syncDir func(string) error
}

func (s Store) sync(path string) error {
	if s.syncDir != nil {
		return s.syncDir(path)
	}
	return syncDirectory(path)
}

var errRecoveryPersistence = errors.New("interrupted recovery journal write failed")

func (s Store) save(state State) error {
	if state.Version == 2 {
		if err := state.validateSigned(); err != nil {
			return err
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicWriteSynced(filepath.Join(s.dir, "state.json"), data, s.sync)
}

func (s Store) load() (State, error) {
	state := State{Version: 1, Consumed: []string{}}
	data, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := strictJSON(data, &state); err != nil {
		return state, err
	}
	if (state.Version != 1 && state.Version != 2) || len(state.Consumed) > 10000 {
		return state, errInvalid
	}
	if state.Version == 2 {
		if err := state.validateSigned(); err != nil {
			return state, err
		}
	}
	_, intentErr := os.Lstat(filepath.Join(s.dir, "recovery-required"))
	if intentErr != nil && !errors.Is(intentErr, os.ErrNotExist) {
		return state, intentErr
	}
	if intentErr == nil && state.Operation == nil {
		return state, errInvalid
	}
	if state.Operation != nil {
		if err := state.Operation.Approval.validate(); err != nil {
			return state, err
		}
		if !terminal(state.Operation.Phase) || (intentErr == nil && state.Operation.Phase != "manual_recovery") {
			state.Operation.Phase = "manual_recovery"
			state.Operation.Maintenance = true
			state.Operation.MigrationUncertain = true
			state.Operation.Error = "interrupted_operation"
			state.CurrentKnown = false
			if err := s.save(state); err != nil {
				return state, errors.Join(errRecoveryPersistence, err)
			}
		}
	}
	return state, nil
}

// The lock is held for the daemon lifetime, including unresolved operations.
func lock(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "controller.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
