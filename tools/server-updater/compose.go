package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

const runtimeLabel = "io.proxima.updater.runtime"

type volumeMount struct {
	Type     string       `json:"type"`
	Source   string       `json:"source"`
	Target   string       `json:"target"`
	ReadOnly bool         `json:"read_only,omitempty"`
	Bind     *bindOptions `json:"bind,omitempty"`
}

type bindOptions struct {
	CreateHostPath bool   `json:"create_host_path"`
	Propagation    string `json:"propagation"`
}

type serviceSpec struct {
	Image       string            `json:"image"`
	PullPolicy  string            `json:"pull_policy"`
	Restart     string            `json:"restart"`
	Environment map[string]string `json:"environment,omitempty"`
	Ports       []string          `json:"ports"`
	Volumes     []volumeMount     `json:"volumes,omitempty"`
	Networks    []string          `json:"networks"`
	Labels      map[string]string `json:"labels"`
	SecurityOpt []string          `json:"security_opt"`
	GroupAdd    []string          `json:"group_add,omitempty"`
}

type external struct {
	External bool   `json:"external"`
	Name     string `json:"name"`
}

type composeSpec struct {
	Services map[string]serviceSpec `json:"services"`
	Volumes  map[string]external    `json:"volumes"`
	Networks map[string]external    `json:"networks"`
}

func (c Config) fingerprint() string {
	b, _ := json.Marshal(c) // Config contains only JSON-supported value types.
	h := sha256.Sum256(append([]byte("proxima-runtime-v2\n"), b...))
	return hex.EncodeToString(h[:])
}

func (c Config) compose(images Images) ([]byte, error) {
	// Compose interpolates even JSON values. Escape dollars in approved env data.
	env := make(map[string]string, len(c.APIEnvironment))
	for k, v := range c.APIEnvironment {
		env[k] = strings.ReplaceAll(v, "$", "$$")
	}
	base := serviceSpec{PullPolicy: "never", Restart: "unless-stopped", Networks: []string{"existing"},
		Labels: map[string]string{runtimeLabel: c.fingerprint()}, SecurityOpt: []string{"no-new-privileges:true"}}
	api, web := base, base
	api.Image, web.Image = images.API, images.Web
	api.Environment = env
	api.Ports, web.Ports = []string{"127.0.0.1:2053:2053"}, []string{"127.0.0.1:8080:8080"}
	api.GroupAdd = []string{strconv.Itoa(c.SocketGID)}
	api.Volumes = []volumeMount{
		{Type: "volume", Source: "app", Target: "/app/data"},
		{Type: "volume", Source: "uploads", Target: "/app/uploads"},
		// Bind the directory, not the socket inode: reconnect after daemon restart.
		{Type: "bind", Source: socketDir, Target: socketDir, ReadOnly: true,
			Bind: &bindOptions{CreateHostPath: false, Propagation: "rprivate"}},
	}
	if c.APIEnvironment["PROXIMA_RELEASE_WATCH_DIR"] == releaseWatchDir {
		api.Volumes = append(api.Volumes, volumeMount{Type: "bind", Source: releaseWatchDir, Target: releaseWatchDir,
			Bind: &bindOptions{CreateHostPath: false, Propagation: "rprivate"}})
	}
	return json.Marshal(composeSpec{
		Services: map[string]serviceSpec{"api": api, "web": web},
		Volumes:  map[string]external{"app": {true, c.AppVolume}, "uploads": {true, c.UploadsVolume}},
		Networks: map[string]external{"existing": {true, c.Network}},
	})
}
