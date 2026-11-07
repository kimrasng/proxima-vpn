package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	rv "github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

func TestSignedReleaseRequiresExactRootApproval(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*Approval)
		compatible bool
		want       error
	}{
		{"exact", func(*Approval) {}, true, nil},
		{"root forbids rollback", func(a *Approval) { a.RollbackCompatible = false }, true, nil},
		{"both forbid rollback", func(a *Approval) { a.RollbackCompatible = false }, false, nil},
		{"publisher forbids rollback", func(*Approval) {}, false, rv.ErrApproval},
		{"different id", func(a *Approval) { a.ID = "another-release" }, true, rv.ErrApproval},
		{"different source API", func(a *Approval) { a.From.API = a.To.API }, true, rv.ErrApproval},
		{"different source web", func(a *Approval) { a.From.Web = a.To.Web }, true, rv.ErrApproval},
		{"different target API", func(a *Approval) { a.To.API = a.From.API }, true, rv.ErrApproval},
		{"different target web", func(a *Approval) { a.To.Web = a.From.Web }, true, rv.ErrApproval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{19}, 32))
			p := rv.Policy{PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), Channel: "stable",
				APIRepository: "ghcr.io/proxima/api", WebRepository: "ghcr.io/proxima/web", MinimumSequence: 2}
			from := Images{API: p.APIRepository + "@sha256:" + strings.Repeat("a", 64), Web: p.WebRepository + "@sha256:" + strings.Repeat("b", 64)}
			to := Images{API: p.APIRepository + "@sha256:" + strings.Repeat("c", 64), Web: p.WebRepository + "@sha256:" + strings.Repeat("d", 64)}
			a := Approval{ID: "release-two", From: from, To: to, RollbackCompatible: true}
			m := rv.Manifest{Schema: 1, Product: "proxima-vpn", Channel: "stable", ReleaseID: a.ID, Sequence: 2,
				IssuedAt: 1800000000, ExpiresAt: 1800003600, From: rv.Images(from), To: rv.Images(to), RollbackCompatible: tc.compatible}
			payload, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			sig := ed25519.Sign(key, append([]byte(rv.SignatureDomain), payload...))
			data, err := json.Marshal(rv.Envelope{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(sig)})
			if err != nil {
				t.Fatal(err)
			}
			v, err := rv.NewVerifier(p)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := v.Verify(data, rv.Context{HighWater: 1, Current: m.From, Now: time.Unix(1800000100, 0)})
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(&a)
			if err := bindReleaseApproval(verified, a); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}
