package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

// runMaxSequence prints the highest sequence found in the given envelope
// files, treating them as the release ledger. It fails closed on any
// unreadable, oversized or noncanonical envelope: a corrupted ledger must
// stop signing, never be averaged or skipped. Ledger entries are read
// unsigned; they are advisory. The durable monotonic backstop is each host's
// persisted high-water, which rejects any sequence at or below the accepted
// one regardless of what the ledger says.
func runMaxSequence(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("max-sequence: no envelope files given")
	}
	var max uint64
	for _, path := range args {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("ledger %s: %w", path, err)
		}
		sequence, err := envelopeSequence(data)
		if err != nil {
			return fmt.Errorf("ledger %s: %w", path, err)
		}
		if sequence > max {
			max = sequence
		}
	}
	fmt.Fprintln(stdout, max)
	return nil
}

// envelopeSequence decodes an envelope with the same byte-equality canonical
// discipline the verifier uses, without checking its signature.
func envelopeSequence(data []byte) (uint64, error) {
	if len(data) > releaseverify.MaxEnvelopeSize {
		return 0, errors.New("oversized envelope")
	}
	var envelope releaseverify.Envelope
	if err := canonicalDecode(data, &envelope); err != nil {
		return 0, err
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if err != nil || base64.StdEncoding.EncodeToString(payload) != envelope.Payload {
		return 0, errors.New("invalid payload encoding")
	}
	var m releaseverify.Manifest
	if err := canonicalDecode(payload, &m); err != nil {
		return 0, err
	}
	if m.Sequence < minSequence || m.Sequence > releaseverify.MaxSequence {
		return 0, errors.New("ledger sequence outside verifier range")
	}
	return m.Sequence, nil
}

// canonicalDecode accepts exactly one representation: the decoded value must
// re-encode to the identical bytes, rejecting unknown, duplicate, missing,
// case-aliased and trailing content. This mirrors releaseverify's private
// canonicalJSON discipline for ledger reading.
func canonicalDecode[T any](data []byte, dst *T) error {
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("noncanonical JSON: %w", err)
	}
	encoded, err := json.Marshal(dst)
	if err != nil || !bytes.Equal(data, encoded) {
		return errors.New("noncanonical JSON")
	}
	return nil
}
