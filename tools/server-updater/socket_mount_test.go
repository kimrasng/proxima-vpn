package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestPreflightRejectsSocketMountDriftWithoutDowntime(t *testing.T) {
	for _, kind := range []string{
		"missing", "wrong_source", "wrong_destination", "socket_inode", "writable",
		"wrong_type", "named_bind", "shared", "missing_propagation", "legacy_mode",
		"contradictory_mode", "extra_mode", "extra_bind", "docker_socket", "duplicate_bind",
		"duplicate_volume", "volume_replaced_by_bind", "web_bind", "missing_group", "wrong_group", "extra_group",
	} {
		t.Run(kind, func(t *testing.T) {
			c, f, a := fixture(t)
			info := f.containers["api"]
			switch kind {
			case "missing":
				info.Mounts = info.Mounts[:2]
			case "wrong_source":
				info.Mounts[2].Source = "/tmp/proxima-updater"
			case "wrong_destination":
				info.Mounts[2].Destination = "/run/other"
			case "socket_inode":
				info.Mounts[2].Source += "/control.sock"
			case "writable":
				info.Mounts[2].RW = true
			case "wrong_type":
				info.Mounts[2].Type = "volume"
			case "named_bind":
				info.Mounts[2].Name = "unexpected"
			case "shared":
				info.Mounts[2].Propagation = "rshared"
			case "missing_propagation":
				info.Mounts[2].Propagation = ""
			case "legacy_mode":
				info.Mounts[2].Mode = "ro,rprivate"
			case "contradictory_mode":
				info.Mounts[2].Mode = "rw"
			case "extra_mode":
				info.Mounts[2].Mode = "ro,z"
			case "extra_bind", "docker_socket":
				path := "/tmp/extra"
				if kind == "docker_socket" {
					path = "/var/run/docker.sock"
				}
				info.Mounts = append(info.Mounts, containerMount{Type: "bind", Source: path, Destination: path})
			case "duplicate_bind":
				info.Mounts[0] = info.Mounts[2]
			case "duplicate_volume":
				info.Mounts[1] = info.Mounts[0]
			case "volume_replaced_by_bind":
				info.Mounts[0].Type = "bind"
				info.Mounts[0].Source = "/tmp/data"
			case "web_bind":
				web := f.containers["web"]
				web.Mounts = []containerMount{info.Mounts[2]}
				f.containers["web"] = web
			case "missing_group":
				info.HostConfig.GroupAdd = nil
			case "wrong_group":
				info.HostConfig.GroupAdd = []string{"0"}
			case "extra_group":
				info.HostConfig.GroupAdd = append(info.HostConfig.GroupAdd, "0")
			}
			f.containers["api"] = info

			w := httptest.NewRecorder()
			c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/preflight", nil))
			var result preflightResponse
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.OK || result.Reason != "topology_mismatch" {
				t.Fatalf("unsafe preflight response: %d %s", w.Code, w.Body.String())
			}
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			s := waitOperation(t, c)
			if s.Operation.Phase != "rejected" || s.Operation.Error != "preflight_failed" || s.Operation.Maintenance || s.Operation.MigrationUncertain || f.stops != 0 || len(f.starts) != 0 || f.unsafe || !f.containers["api"].State.Running || !f.containers["web"].State.Running {
				t.Fatalf("unsafe preflight: %+v stops=%d starts=%v unsafe=%v", s.Operation, f.stops, f.starts, f.unsafe)
			}
		})
	}
}

func TestBootstrapMountsOnlyReadOnlyUpdaterDirectoryWithSocketGroup(t *testing.T) {
	c, _, a := fixture(t)
	a.To = a.From
	b, err := a.bootstrap(c.docker.config)
	if err != nil {
		t.Fatal(err)
	}
	var spec composeSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	api, web := spec.Services["api"], spec.Services["web"]
	want := []volumeMount{
		{Type: "volume", Source: "app", Target: "/app/data"},
		{Type: "volume", Source: "uploads", Target: "/app/uploads"},
		{Type: "bind", Source: "/run/proxima-updater", Target: "/run/proxima-updater", ReadOnly: true,
			Bind: &bindOptions{CreateHostPath: false, Propagation: "rprivate"}},
	}
	if !reflect.DeepEqual(api.Volumes, want) || len(web.Volumes) != 0 || !reflect.DeepEqual(api.GroupAdd, []string{strconv.Itoa(c.docker.config.SocketGID)}) || len(web.GroupAdd) != 0 {
		t.Fatalf("unexpected socket access: api=%+v web=%+v", api.Volumes, web.Volumes)
	}
	if len(spec.Volumes) != 2 || spec.Volumes["app"] != (external{true, c.docker.config.AppVolume}) || spec.Volumes["uploads"] != (external{true, c.docker.config.UploadsVolume}) || len(spec.Networks) != 1 || spec.Networks["existing"] != (external{true, c.docker.config.Network}) {
		t.Fatal("named volumes or network changed")
	}
	if !reflect.DeepEqual(api.Ports, []string{"127.0.0.1:2053:2053"}) || !reflect.DeepEqual(web.Ports, []string{"127.0.0.1:8080:8080"}) || !reflect.DeepEqual(api.Networks, []string{"existing"}) || !reflect.DeepEqual(web.Networks, []string{"existing"}) {
		t.Fatal("service network or loopback ports changed")
	}
	// Inspect the wire field: decoding into bool alone cannot distinguish a
	// missing create_host_path (Compose defaults true) from explicit false.
	var wire struct {
		Services map[string]struct {
			Volumes []struct {
				Bind *struct {
					CreateHostPath *bool `json:"create_host_path"`
				} `json:"bind"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	bind := wire.Services["api"].Volumes[2].Bind
	if bind == nil || bind.CreateHostPath == nil || *bind.CreateHostPath {
		t.Fatal("Compose may create an unprovisioned host directory")
	}
}

func TestCallerCannotConfigureBindMounts(t *testing.T) {
	c, f, _ := fixture(t)
	for _, field := range []string{`"binds":["/var/run/docker.sock:/var/run/docker.sock"]`, `"socket_dir":"/tmp/other"`} {
		data, err := json.Marshal(c.docker.config)
		if err != nil {
			t.Fatal(err)
		}
		var cfg Config
		if strictJSON(append(append(data[:len(data)-1], ','), []byte(field+"}")...), &cfg) == nil {
			t.Fatal("configuration accepted caller-defined mount")
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(`{"release_id":"release-one","maintenance":true,`+field+`}`))
		r.Header.Set("Content-Type", "application/json")
		c.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || c.snapshot().Operation != nil || f.stops != 0 || len(f.starts) != 0 || f.unsafe {
			t.Fatal("protocol accepted caller-defined mount")
		}
	}
}
