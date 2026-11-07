package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

func TestRootAuthorizationPolicyRequiresExplicitOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       authorizationPolicy
		invalid    bool
	}{
		{"absent", "", authorizationPolicy{Mode: "manual"}, false},
		{"manual", `{"mode":"manual"}`, authorizationPolicy{Mode: "manual"}, false},
		{"publisher", `{"mode":"publisher_signed"}`, authorizationPolicy{Mode: "publisher_signed"}, false},
		{"rollback", `{"mode":"publisher_signed","allow_rollback":true}`, authorizationPolicy{Mode: "publisher_signed", AllowRollback: true}, false},
		{"missing mode", `{}`, authorizationPolicy{}, true},
		{"null mode", `{"mode":null}`, authorizationPolicy{}, true},
		{"case alias", `{"Mode":"publisher_signed"}`, authorizationPolicy{}, true},
		{"case collision", `{"mode":"manual","Mode":"publisher_signed"}`, authorizationPolicy{}, true},
		{"unknown mode", `{"mode":"automatic"}`, authorizationPolicy{}, true},
		{"key injection", `{"mode":"publisher_signed","public_key":"attacker"}`, authorizationPolicy{}, true},
		{"duplicate", `{"mode":"manual","mode":"publisher_signed"}`, authorizationPolicy{}, true},
		{"trailing", `{"mode":"publisher_signed"}{}`, authorizationPolicy{}, true},
		{"manual rollback", `{"mode":"manual","allow_rollback":true}`, authorizationPolicy{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{}
			if tc.data != "" {
				files["authorization-policy.json"] = &fstest.MapFile{Data: []byte(tc.data), Mode: 0600}
			}
			got, err := loadAuthorization(files)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid policy accepted")
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("policy: %+v %v", got, err)
			}
		})
	}
}

func TestPublisherRejectsInvalidAuthorityBeforePull(t *testing.T) {
	for _, kind := range []string{"signature", "repository", "current", "high water", "expired", "future", "policy mismatch", "revoked during fetch"} {
		t.Run(kind, func(t *testing.T) {
			c, e, a := publisherFixture(t)
			input, _ := signedFixtureRelease(t, a, 2)
			switch kind {
			case "signature":
				input.data = []byte(`{"payload":"invalid","signature":"invalid"}`)
			case "repository":
				a.To.API = "evil.example/api@sha256:" + strings.Repeat("c", 64)
				input, _ = signedFixtureRelease(t, a, 2)
			case "current":
				a.From.Web = a.To.Web
				input, _ = signedFixtureRelease(t, a, 2)
			case "high water":
				input, _ = signedFixtureRelease(t, a, 1)
			case "expired":
				c.now = func() time.Time { return time.Unix(1800003600, 0) }
			case "future":
				c.now = func() time.Time { return time.Unix(1799999999, 0) }
			}
			c.fetch = func(context.Context, string) ([]byte, error) {
				if kind == "revoked during fetch" {
					c.authorization = func() (authorizationPolicy, error) { return authorizationPolicy{Mode: "manual"}, nil }
				}
				if kind == "policy mismatch" {
					c.policy = func() (releaseverify.Policy, error) {
						p := input.policy
						p.Channel = "beta"
						return p, nil
					}
				}
				return input.data, nil
			}
			before := c.snapshot()
			if _, err := c.startPreparation("v1.2.3"); err != nil {
				t.Fatal(err)
			}
			waitOperation(t, c)
			if c.preparationStatus().Status != "failed" || len(e.pulled) != 0 || !reflect.DeepEqual(before, c.snapshot()) {
				t.Fatal("invalid authority pulled or changed state")
			}
			if _, err := os.Lstat(filepath.Join(c.store.dir, preparedEnvelopeName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid candidate installed", err)
			}
		})
	}
}

func TestPublisherPreparationRechecksAuthorityAndDurability(t *testing.T) {
	for _, kind := range []string{"revoked", "policy", "expired", "pull failed", "runtime drift", "canceled", "fsync failed"} {
		t.Run(kind, func(t *testing.T) {
			c, e, a := publisherFixture(t)
			input, _ := signedFixtureRelease(t, a, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.ctx = ctx
			e.afterPull = func() {
				switch kind {
				case "revoked":
					c.authorization = func() (authorizationPolicy, error) { return authorizationPolicy{Mode: "manual"}, nil }
				case "policy":
					c.policy = func() (releaseverify.Policy, error) {
						p := input.policy
						p.Channel = "beta"
						return p, nil
					}
				case "expired":
					c.now = func() time.Time { return time.Unix(1800003600, 0) }
				case "runtime drift":
					e.base.install("api", a.To.API)
				case "canceled":
					cancel()
				}
			}
			if kind == "pull failed" {
				e.fail = a.To.Web
			}
			if kind == "fsync failed" {
				c.store.syncDir = func(string) error { return errors.New("fsync failed") }
			}
			if _, err := c.startPreparation("v1.2.3"); err != nil {
				t.Fatal(err)
			}
			waitOperation(t, c)
			if c.preparationStatus().Status != "failed" || e.base.stops != 0 || len(e.base.starts) != 0 || c.snapshot().HighWater != 1 {
				t.Fatal("failed preparation reported success or mutated runtime")
			}
			if kind == "fsync failed" {
				if _, err := c.acceptCandidate(a.ID, strings.Repeat("a", 64)); !errors.Is(err, errBusy) {
					t.Fatal("uncertain persistence did not latch", err)
				}
			} else if _, err := os.Stat(filepath.Join(c.store.dir, preparedEnvelopeName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed candidate installed", err)
			}
		})
	}
}

func TestPublisherRollbackRequiresSeparateRootPermission(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[allowed], func(t *testing.T) {
			c, e, a := publisherFixture(t)
			c.authorization = func() (authorizationPolicy, error) {
				return authorizationPolicy{Mode: "publisher_signed", AllowRollback: allowed}, nil
			}
			candidate := completePreparation(t, c)
			c.readiness = readyFunc(func(context.Context, string) error {
				if e.base.containers["api"].Config.Image == a.To.API {
					return errReadiness
				}
				return nil
			})
			if _, err := c.acceptCandidate(a.ID, candidate.PayloadSHA256); err != nil {
				t.Fatal(err)
			}
			s := waitOperation(t, c)
			want := "manual_recovery"
			if allowed {
				want = "rolled_back"
			}
			if s.Operation.Phase != want || s.HighWater != 2 || (!allowed && len(e.base.starts) != 1) {
				t.Fatalf("rollback authority bypass: %+v", s.Operation)
			}
		})
	}
}
