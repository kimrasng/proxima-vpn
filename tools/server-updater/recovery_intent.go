package main

import (
	"errors"
	"os"
	"path/filepath"
)

func (c *Controller) finish(phase, code string) error {
	if err := c.phase(phase, false, true, code); err != nil {
		return err
	}
	// Removal can succeed before its directory sync fails. Any later cleanup
	// must establish a fresh durable marker before touching Docker again.
	c.recoveryRequired = false
	return c.store.finishRecoveryIntent()
}

// This independent write-ahead marker outlives terminal state renames. It is
// removed only after the terminal journal is durable and no mutation remains.
func (s Store) requireRecovery() error {
	path := filepath.Join(s.dir, "recovery-required")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return s.sync(s.dir)
}

func (s Store) finishRecoveryIntent() error {
	if err := os.Remove(filepath.Join(s.dir, "recovery-required")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.sync(s.dir)
}
