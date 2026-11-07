package main

import (
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

const preparedEnvelopeName = "prepared-release.json"

// The daemon verifies the state directory is root-owned 0700 and its ancestors
// are root-owned and not group/world-writable before acquiring its lifetime flock.
// Children must retain that same owner,
// be regular private files, and never be symlinks. No request supplies a path.
func (s Store) preparedEnvelope() ([]byte, error) {
	path := filepath.Join(s.dir, preparedEnvelopeName)
	dir, err := os.Stat(s.dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	owner, ok := dir.Sys().(*syscall.Stat_t)
	fileOwner, fileOK := st.Sys().(*syscall.Stat_t)
	if !ok || !fileOK || owner.Uid != fileOwner.Uid || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > releaseverify.MaxEnvelopeSize {
		return nil, errInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return nil, errInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, releaseverify.MaxEnvelopeSize+1))
	if err != nil || len(data) > releaseverify.MaxEnvelopeSize {
		return nil, errInvalid
	}
	return data, nil
}
