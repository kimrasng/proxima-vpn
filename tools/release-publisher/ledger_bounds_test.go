package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

func TestLedgerRejectsSequenceOutsideVerifierRange(t *testing.T) {
	for _, sequence := range []uint64{0, 1, releaseverify.MaxSequence + 1, 1<<64 - 1} {
		payload, err := json.Marshal(releaseverify.Manifest{Sequence: sequence})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(releaseverify.Envelope{Payload: base64.StdEncoding.EncodeToString(payload)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := envelopeSequence(data); err == nil {
			t.Fatalf("accepted out-of-range ledger sequence %d", sequence)
		}
	}
}
