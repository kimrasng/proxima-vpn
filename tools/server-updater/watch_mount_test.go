package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBootstrapPreservesConfiguredReleaseWatchStorage(t *testing.T) {
	c, _, a := fixture(t)
	cfg := c.docker.config
	cfg.APIEnvironment["PROXIMA_RELEASE_WATCH_DIR"] = "/var/lib/proxima-release-watch"
	a.To = a.From
	data, err := a.bootstrap(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var spec composeSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	for _, mount := range spec.Services["api"].Volumes {
		if mount.Target == "/var/lib/proxima-release-watch" {
			if mount.Type != "bind" || mount.Source != mount.Target || mount.ReadOnly || mount.Bind == nil || mount.Bind.CreateHostPath || mount.Bind.Propagation != "rprivate" {
				t.Fatalf("unsafe watch storage mount: %+v", mount)
			}
			return
		}
	}
	t.Fatal("configured release watch loses its persistent storage during enrollment")
}

func TestConfiguredWatchMountVerifiedBeforeAnyServiceStops(t *testing.T) {
	for _, kind := range []string{"valid", "missing", "wrong_source", "read_only", "shared", "legacy_mode", "named_bind", "wrong_type"} {
		t.Run(kind, func(t *testing.T) {
			c, f, a := fixture(t)
			c.docker.config.APIEnvironment["PROXIMA_RELEASE_WATCH_DIR"] = "/var/lib/proxima-release-watch"
			f.config = c.docker.config
			f.install("api", a.From.API)
			f.install("web", a.From.Web)
			info := f.containers["api"]
			mount := containerMount{Type: "bind", Source: "/var/lib/proxima-release-watch", Destination: "/var/lib/proxima-release-watch", RW: true, Propagation: "rprivate"}
			switch kind {
			case "wrong_source":
				mount.Source = "/tmp/watch"
			case "read_only":
				mount.RW = false
			case "shared":
				mount.Propagation = "rshared"
			case "legacy_mode":
				mount.Mode = "rw,rprivate"
			case "named_bind":
				mount.Name = "other"
			case "wrong_type":
				mount.Type = "volume"
			}
			if kind != "missing" {
				info.Mounts = append(info.Mounts, mount)
			}
			f.containers["api"] = info
			err := c.docker.preflight(context.Background(), a)
			if (err == nil) != (kind == "valid") || f.stops != 0 || len(f.starts) != 0 || f.unsafe {
				t.Fatalf("kind=%s error=%v stops=%d starts=%v", kind, err, f.stops, f.starts)
			}
		})
	}
}

func TestReleaseWatchPathCannotExpandHostMountAuthority(t *testing.T) {
	c, _, _ := fixture(t)
	for _, path := range []string{"/tmp/watch", "/var/run/docker.sock", "/var/lib/proxima-release-watch/../other"} {
		c.docker.config.APIEnvironment["PROXIMA_RELEASE_WATCH_DIR"] = path
		if c.docker.config.validate() == nil {
			t.Fatalf("accepted caller-selected watch mount: %q", path)
		}
	}
}
