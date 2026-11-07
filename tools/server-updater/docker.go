package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var imageID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var errTopology = fmt.Errorf("deployment does not match managed runtime")

type containerMount struct {
	Type        string
	Name        string
	Source      string
	Destination string
	Mode        string
	RW          bool
	Propagation string
}

type containerInfo struct {
	ID     string `json:"Id"`
	Image  string
	Config struct {
		Image  string
		Labels map[string]string
	}
	State struct {
		Running    bool
		Restarting bool
		Paused     bool
	}
	Mounts     []containerMount
	HostConfig struct {
		Privileged    bool
		NetworkMode   string
		GroupAdd      []string
		RestartPolicy struct{ Name string }
		PortBindings  map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string
		}
	}
	NetworkSettings struct{ Networks map[string]json.RawMessage }
}

type Docker struct {
	config   Config
	executor Executor
	dir      string
}

func (d Docker) compose(ctx context.Context, args ...string) error {
	base := []string{"compose", "--project-name", d.config.Project, "--project-directory", d.dir,
		"--env-file", "/dev/null", "-f", filepath.Join(d.dir, "runtime.json")}
	_, err := d.executor.Run(ctx, dockerBinary, append(base, args...)...)
	if err != nil {
		// CLI failure does not prove that the daemon abandoned the mutation.
		return fmt.Errorf("%w: compose", errUncertain)
	}
	return nil
}

func (d Docker) writeRuntime(images Images) error {
	b, err := d.config.compose(images)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(d.dir, "runtime.json"), b)
}

func (d Docker) localImage(ctx context.Context, ref string) (string, error) {
	b, err := d.executor.Run(ctx, dockerBinary, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !imageID.MatchString(id) {
		return "", errTopology
	}
	return id, nil
}

func (d Docker) inspect(ctx context.Context, service string) (containerInfo, error) {
	var result containerInfo
	b, err := d.executor.Run(ctx, dockerBinary, "container", "ls", "--all", "--no-trunc", "--quiet",
		"--filter", "label=com.docker.compose.project="+d.config.Project,
		"--filter", "label=com.docker.compose.service="+service)
	if err != nil {
		return result, err
	}
	id := strings.TrimSpace(string(b))
	if !containerID.MatchString(id) {
		return result, errTopology
	}
	b, err = d.executor.Run(ctx, dockerBinary, "container", "inspect", id)
	if err != nil {
		return result, err
	}
	var list []containerInfo
	if err := json.Unmarshal(b, &list); err != nil {
		return result, err
	}
	if len(list) != 1 || list[0].ID != id {
		return result, errTopology
	}
	return list[0], nil
}

func (d Docker) verify(ctx context.Context, images Images, running bool) error {
	for service, ref := range map[string]string{"api": images.API, "web": images.Web} {
		c, err := d.inspect(ctx, service)
		if err != nil {
			return err
		}
		id, err := d.localImage(ctx, ref)
		if err != nil {
			return err
		}
		labels := c.Config.Labels
		if c.Config.Image != ref || c.Image != id || c.State.Running != running || c.State.Paused || c.State.Restarting ||
			labels["com.docker.compose.project"] != d.config.Project || labels["com.docker.compose.service"] != service ||
			labels[runtimeLabel] != d.config.fingerprint() || c.HostConfig.Privileged || c.HostConfig.RestartPolicy.Name != "unless-stopped" ||
			len(c.NetworkSettings.Networks) != 1 || c.NetworkSettings.Networks[d.config.Network] == nil {
			return errTopology
		}
		port := "2053"
		if service == "web" {
			port = "8080"
		}
		bindings := c.HostConfig.PortBindings[port+"/tcp"]
		if len(c.HostConfig.PortBindings) != 1 || len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" || bindings[0].HostPort != port {
			return errTopology
		}
		mounts := map[string]string{}
		wantMounts := 0
		if service == "api" {
			mounts = map[string]string{"/app/data": d.config.AppVolume, "/app/uploads": d.config.UploadsVolume}
			wantMounts = 3
			if d.config.APIEnvironment["PROXIMA_RELEASE_WATCH_DIR"] == releaseWatchDir {
				wantMounts++
			}
			if len(c.HostConfig.GroupAdd) != 1 || c.HostConfig.GroupAdd[0] != strconv.Itoa(d.config.SocketGID) {
				return errTopology
			}
		} else if len(c.HostConfig.GroupAdd) != 0 {
			return errTopology
		}
		if len(c.Mounts) != wantMounts {
			return errTopology
		}
		seen := make(map[string]bool)
		for _, m := range c.Mounts {
			if seen[m.Destination] {
				return errTopology
			}
			seen[m.Destination] = true
			if service == "api" && m.Destination == releaseWatchDir && d.config.APIEnvironment["PROXIMA_RELEASE_WATCH_DIR"] == releaseWatchDir {
				if m.Type != "bind" || m.Source != releaseWatchDir || m.Name != "" || m.Mode != "" || !m.RW || m.Propagation != "rprivate" {
					return errTopology
				}
				continue
			}
			if service == "api" && m.Destination == socketDir {
				// Compose's structured Mount API (create_host_path=false) has
				// empty Mode; RW and Propagation describe the effective mode.
				if m.Type != "bind" || m.Source != socketDir || m.Name != "" || m.Mode != "" || m.RW || m.Propagation != "rprivate" {
					return errTopology
				}
				continue
			}
			if m.Type != "volume" || mounts[m.Destination] != m.Name || m.Name == "" {
				return errTopology
			}
		}
	}
	return nil
}

func (d Docker) preflight(ctx context.Context, a Approval) error {
	for _, volume := range []string{d.config.AppVolume, d.config.UploadsVolume} {
		if _, err := d.executor.Run(ctx, dockerBinary, "volume", "inspect", volume); err != nil {
			return err
		}
	}
	if _, err := d.executor.Run(ctx, dockerBinary, "network", "inspect", d.config.Network); err != nil {
		return err
	}
	for _, ref := range []string{a.To.API, a.To.Web} {
		if _, err := d.localImage(ctx, ref); err != nil {
			return err
		}
	}
	return d.verify(ctx, a.From, true)
}

func (d Docker) stop(ctx context.Context) error {
	return d.compose(ctx, "stop", "--timeout", "20", "web", "api")
}

func (d Docker) start(ctx context.Context, service string) error {
	return d.compose(ctx, "up", "--detach", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", service)
}
