package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

const configPath = "/etc/proxima-updater/config.json"
const approvalPath = "/etc/proxima-updater/approved.json"
const stateDir = "/var/lib/proxima-updater"
const socketDir = "/run/proxima-updater"
const socketPath = socketDir + "/control.sock"

func run() error {
	render := flag.Bool("render-bootstrap", false, "print the approved source runtime for root-only enrollment")
	prepare := flag.String("prepare-release", "", "prepare a fixed-repository signed release tag while the daemon is stopped")
	flag.Parse()
	preparing := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "prepare-release" {
			preparing = true
		}
	})
	if flag.NArg() != 0 || os.Geteuid() != 0 || runtime.GOOS != "linux" ||
		(preparing && (*render || !releaseTagPattern.MatchString(*prepare))) {
		return errInvalid
	}
	syscall.Umask(0077)
	var cfg Config
	if err := readTrusted(configPath, &cfg); err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	if *render {
		a, err := loadApproval(trustedApprovalFiles{})
		if err != nil {
			return err
		}
		b, err := a.bootstrap(cfg)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	}
	for _, path := range []string{stateDir, socketDir, dockerBinary, composePluginPath, stateDir + "/docker-config"} {
		if err := trustedPath(path); err != nil {
			return err
		}
	}
	st, err := os.Stat(socketDir)
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || st.Mode().Perm() != 0750 || uint64(owner.Gid) != uint64(cfg.SocketGID) {
		return fmt.Errorf("%w: socket directory must be root:socket_gid 0750", errInvalid)
	}
	st, err = os.Stat(stateDir)
	if err != nil {
		return err
	}
	if st.Mode().Perm() != 0700 {
		return errInvalid
	}
	guard, err := lock(stateDir)
	if err != nil {
		return err
	}
	defer guard.Close()
	if preparing {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		ctx, stop := context.WithTimeout(ctx, 10*time.Minute)
		defer stop()
		return prepareHostRelease(ctx, cfg, *prepare, os.Stdout)
	}
	// A manually provisioned journal has the same root trust requirement as
	// policy and approval files. Missing state stays unenrolled, never baseline 0.
	if _, err := os.Lstat(stateDir + "/state.json"); err == nil {
		if err := trustedPath(stateDir + "/state.json"); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	store := Store{dir: stateDir}
	state, err := store.load()
	if err != nil && !errors.Is(err, errRecoveryPersistence) {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	c := &Controller{state: state, store: store, docker: Docker{cfg, commandExecutor{stateDir}, stateDir},
		readiness: newReadiness(), release: loadRelease, now: time.Now, approval: func() (Approval, error) {
			return loadApproval(trustedApprovalFiles{})
		}, ctx: ctx, blocked: err != nil}
	c.authorization = func() (authorizationPolicy, error) { return loadAuthorization(trustedAuthorizationFiles{}) }
	c.policy = func() (releaseverify.Policy, error) {
		var p releaseverify.Policy
		err := readTrusted(releasePolicyPath, &p)
		return p, err
	}
	client := newReleaseHTTPClient()
	defer client.CloseIdleConnections()
	c.fetch = func(ctx context.Context, tag string) ([]byte, error) { return fetchRelease(ctx, client, tag) }
	// Store.load converts interrupted operations to a durable recovery gate.
	// Contain any surviving managed pair before exposing the control socket.
	c.recover()
	defer func() {
		cancel()
		c.mu.Lock()
		done, active := c.done, c.active
		c.mu.Unlock()
		if active {
			<-done
		}
	}()
	listener, err := listenSocket(socketPath, cfg.SocketGID)
	if err != nil {
		return err
	}
	defer listener.Close()
	srv := newServer(c)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	select {
	case err := <-served:
		cancel()
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 6*time.Second)
		defer stop()
		if err := srv.Shutdown(shutdown); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		// Do not log config JSON, subprocess output, or environment values.
		fmt.Fprintln(os.Stderr, "proxima-updater:", err)
		os.Exit(1)
	}
}
