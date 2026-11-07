package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUpdateAndRollbackOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failure    error
		compatible bool
		phase      string
		target     bool
	}{
		{"success", nil, true, "succeeded", true},
		{"compatible command failure", errors.New("create failed"), true, "manual_recovery", false},
		{"compatible readiness failure", errReadiness, true, "rolled_back", false},
		{"incompatible failure", errors.New("create failed"), false, "manual_recovery", false},
		{"ambiguous timeout", errUncertain, true, "manual_recovery", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, a := fixture(t)
			a.RollbackCompatible = tc.compatible
			c.approval = func() (Approval, error) { return a, nil }
			if errors.Is(tc.failure, errReadiness) {
				c.readiness = readyFunc(func(context.Context, string) error {
					if f.containers["api"].Config.Image == a.To.API {
						return errReadiness
					}
					return nil
				})
			} else if tc.failure != nil {
				f.failRef, f.failErr = a.To.API, tc.failure
			}
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			s := waitOperation(t, c)
			if s.Operation.Phase != tc.phase || !s.Operation.MigrationUncertain || f.unsafe {
				t.Fatalf("outcome: %+v unsafe=%v", s.Operation, f.unsafe)
			}
			if tc.phase == "rolled_back" || tc.target {
				want := a.From
				if tc.target {
					want = a.To
				}
				if err := c.docker.verify(context.Background(), want, true); err != nil {
					t.Fatal(err)
				}
				if s.Operation.Maintenance {
					t.Fatal("maintenance left active")
				}
			} else if len(f.starts) != 1 || !s.Operation.Maintenance {
				t.Fatal("unsafe retry or missing recovery gate")
			}
			persisted, err := c.store.load()
			if err != nil || persisted.Operation.Phase != tc.phase {
				t.Fatalf("durability: %v %+v", err, persisted)
			}
		})
	}
}

func TestPreflightRejectsUnmanagedOrMissingImagesWithoutDowntime(t *testing.T) {
	for _, kind := range []string{"unpinned", "unmanaged", "missing", "wrong_volume", "duplicate", "wrong_port"} {
		t.Run(kind, func(t *testing.T) {
			c, f, a := fixture(t)
			info := f.containers["api"]
			switch kind {
			case "unpinned":
				info.Config.Image = "api:latest"
			case "unmanaged":
				delete(info.Config.Labels, runtimeLabel)
			case "missing":
				delete(f.images, a.To.API)
			case "wrong_volume":
				info.Mounts[0].Name = "empty_new_volume"
			case "duplicate":
				info.ID += "\n" + strings.Repeat("e", 64)
			case "wrong_port":
				info.HostConfig.PortBindings["2053/tcp"][0].HostIP = "0.0.0.0"
			}
			f.containers["api"] = info
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			s := waitOperation(t, c)
			if s.Operation.Phase != "rejected" || len(f.starts) != 0 || !f.containers["api"].State.Running || s.Operation.MigrationUncertain {
				t.Fatalf("unsafe preflight: %+v", s.Operation)
			}
		})
	}
}

func TestReadinessFailureRollsBackExactPair(t *testing.T) {
	c, f, a := fixture(t)
	c.readiness = readyFunc(func(_ context.Context, service string) error {
		if service == "web" && f.containers["web"].Config.Image == a.To.Web {
			return errReadiness
		}
		return nil
	})
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	s := waitOperation(t, c)
	if s.Operation.Phase != "rolled_back" || f.containers["api"].Config.Image != a.From.API || f.containers["web"].Config.Image != a.From.Web {
		t.Fatalf("rollback: %+v", s.Operation)
	}
}
