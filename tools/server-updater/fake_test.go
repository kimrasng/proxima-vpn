package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeExecutor implements the Docker allowlist surface in memory. Every
// command outside the fixed surface sets unsafe, and every test asserts
// unsafe == false, so a regression toward broader subprocess authority fails
// the suite even when the controller's reported outcome stays green.
type fakeExecutor struct {
	config     Config
	dir        string
	containers map[string]containerInfo
	images     map[string]string
	starts     []string
	stops      int
	failRef    string
	failErr    error
	stopErr    error
	unsafe     bool
}

type readyFunc func(context.Context, string) error

func (f readyFunc) Wait(ctx context.Context, service string) error { return f(ctx, service) }

func fixture(t *testing.T) (*Controller, *fakeExecutor, Approval) {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{Project: "proxima", Network: "proxima_default", AppVolume: "proxima_app_data",
		UploadsVolume: "proxima_uploads_data", SocketGID: 1001,
		APIEnvironment: map[string]string{"DATABASE_URL": "postgres://local/db", "REDIS_URL": "redis://redis", "JWT_SECRET": "secret$literal"}}
	ref := func(service, hex string) string {
		return "ghcr.io/proxima/" + service + "@sha256:" + strings.Repeat(hex, 64)
	}
	a := Approval{ID: "release-one", From: Images{ref("api", "a"), ref("web", "b")},
		To: Images{ref("api", "c"), ref("web", "d")}, RollbackCompatible: true}
	fake := &fakeExecutor{config: cfg, dir: dir, containers: map[string]containerInfo{}, images: map[string]string{}}
	for i, ref := range []string{a.From.API, a.From.Web, a.To.API, a.To.Web} {
		fake.images[ref] = "sha256:" + strings.Repeat(string(rune('1'+i)), 64)
	}
	fake.install("api", a.From.API)
	fake.install("web", a.From.Web)
	c := &Controller{state: State{Version: 1, Consumed: []string{}}, store: Store{dir: dir},
		docker: Docker{cfg, fake, dir}, readiness: readyFunc(func(context.Context, string) error { return nil }),
		approval: func() (Approval, error) { return a, nil }, ctx: context.Background()}
	configureSignedFixture(t, c, a)
	return c, fake, a
}

func (f *fakeExecutor) install(service, ref string) {
	var c containerInfo
	char, port := "a", "2053"
	if service == "web" {
		char, port = "b", "8080"
	}
	c.ID, c.Image, c.Config.Image = strings.Repeat(char, 64), f.images[ref], ref
	c.Config.Labels = map[string]string{"com.docker.compose.project": f.config.Project,
		"com.docker.compose.service": service, runtimeLabel: f.config.fingerprint()}
	c.State.Running = true
	c.HostConfig.RestartPolicy.Name = "unless-stopped"
	c.HostConfig.PortBindings = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string
	}{
		port + "/tcp": {{HostIP: "127.0.0.1", HostPort: port}},
	}
	c.NetworkSettings.Networks = map[string]json.RawMessage{f.config.Network: json.RawMessage(`{}`)}
	if service == "api" {
		c.HostConfig.GroupAdd = []string{strconv.Itoa(f.config.SocketGID)}
		c.Mounts = []containerMount{
			{Type: "volume", Name: f.config.AppVolume, Destination: "/app/data", RW: true},
			{Type: "volume", Name: f.config.UploadsVolume, Destination: "/app/uploads", RW: true},
			{Type: "bind", Source: "/run/proxima-updater", Destination: "/run/proxima-updater", Propagation: "rprivate"},
		}
	}
	f.containers[service] = c
}

func (f *fakeExecutor) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, errUncertain
	}
	if binary != dockerBinary {
		f.unsafe = true
		return nil, errors.New("forbidden binary")
	}
	if args[0] == "compose" {
		cargs := args[1:]
		if len(cargs) < 9 {
			f.unsafe = true
			return nil, errors.New("bad compose")
		}
		for _, a := range cargs {
			if a == "down" || a == "--remove-orphans" || a == "--renew-anon-volumes" || a == "build" || a == "pull" {
				f.unsafe = true
			}
		}
		switch cargs[8] {
		case "stop":
			f.stops++
			if strings.Join(cargs[9:], " ") != "--timeout 20 web api" {
				f.unsafe = true
			}
			if f.stopErr != nil {
				return nil, f.stopErr
			}
			for service, c := range f.containers {
				c.State.Running = false
				f.containers[service] = c
			}
			return nil, nil
		case "up":
			service := cargs[len(cargs)-1]
			if strings.Join(cargs[9:len(cargs)-1], " ") != "--detach --no-deps --no-build --pull never --force-recreate" || (service != "api" && service != "web") {
				f.unsafe = true
				return nil, errInvalid
			}
			data, err := os.ReadFile(filepath.Join(f.dir, "runtime.json"))
			if err != nil {
				return nil, err
			}
			var spec composeSpec
			if err := json.Unmarshal(data, &spec); err != nil {
				return nil, err
			}
			if len(spec.Services) != 2 || len(spec.Volumes) != 2 || !spec.Volumes["app"].External || !spec.Volumes["uploads"].External || !spec.Networks["existing"].External || spec.Services[service].Restart != "unless-stopped" {
				f.unsafe = true
				return nil, errInvalid
			}
			ref := spec.Services[service].Image
			f.starts = append(f.starts, ref)
			if ref == f.failRef {
				return nil, f.failErr
			}
			f.install(service, ref)
			// Reflect the rendered mounts and groups rather than silently
			// repairing them from the fixture's known-good enrollment.
			c := f.containers[service]
			c.HostConfig.GroupAdd = spec.Services[service].GroupAdd
			c.Mounts = nil
			for _, v := range spec.Services[service].Volumes {
				m := containerMount{Type: v.Type, Source: v.Source, Destination: v.Target, RW: !v.ReadOnly}
				if v.Type == "volume" {
					m.Name = spec.Volumes[v.Source].Name
				}
				if v.Bind != nil {
					m.Propagation = v.Bind.Propagation
				}
				c.Mounts = append(c.Mounts, m)
			}
			f.containers[service] = c
			return nil, nil
		}
		f.unsafe = true
		return nil, errors.New("forbidden compose command")
	}
	switch args[0] + " " + args[1] {
	case "container stop":
		if len(args) != 5 || args[2] != "--timeout" || args[3] != "20" || !containerID.MatchString(args[4]) {
			f.unsafe = true
			return nil, errors.New("forbidden stop")
		}
		for service, c := range f.containers {
			if c.ID == args[4] {
				if f.stopErr != nil {
					return nil, f.stopErr
				}
				c.State.Running, c.State.Restarting, c.State.Paused = false, false, false
				f.containers[service] = c
				return nil, nil
			}
		}
		return nil, errors.New("container missing")
	case "image inspect":
		id, ok := f.images[args[len(args)-1]]
		if !ok {
			return nil, errors.New("image missing")
		}
		return []byte(id), nil
	case "volume inspect", "network inspect":
		return []byte(`[]`), nil
	case "container ls":
		service := strings.TrimPrefix(args[len(args)-1], "label=com.docker.compose.service=")
		return []byte(f.containers[service].ID), nil
	case "container inspect":
		for _, c := range f.containers {
			if c.ID == args[2] {
				return json.Marshal([]containerInfo{c})
			}
		}
	}
	f.unsafe = true
	return nil, errors.New("forbidden command")
}

func waitOperation(t *testing.T, c *Controller) State {
	t.Helper()
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("operation never finished")
	}
	return c.snapshot()
}
