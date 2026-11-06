package connectionhistory

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Server owns its socket and goroutines, but not the private containing
// directory. Close joins all work; context cancellation also shuts it down.
type Server struct {
	observer *Observer
	listener *net.UnixListener
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	err      error
}

// Listen creates a private Unix stream endpoint. Runtime entrypoints pass
// os.Getenv("PROXIMA_HISTORY_SOCKET") once at startup. Existing sockets are never
// unlinked or taken over. The directory must be private and owned by this UID.
func (o *Observer) Listen(ctx context.Context, path string) (*Server, error) {
	if path == "" {
		return nil, fmt.Errorf("history socket: empty path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("history directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("history directory stat: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || (stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0) {
		return nil, fmt.Errorf("history directory must be private and service-owned")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("history listen: %w", err)
	}
	listener.SetUnlinkOnClose(true)
	if err := os.Chmod(path, 0600); err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Server{observer: o, listener: listener, cancel: cancel, done: make(chan struct{})}
	go s.run(ctx)
	return s, nil
}

func (s *Server) run(ctx context.Context) {
	defer close(s.done)
	// Accept and active socket reads are interrupted by context cancellation.
	stop := context.AfterFunc(ctx, func() {
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.recordError(err)
		}
	})
	defer stop()
	tickCtx, cancelTick := context.WithCancel(ctx)
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		ticker := time.NewTicker(UpdateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-tickCtx.Done():
				return
			case <-ticker.C:
				if err := s.observer.Snapshot(); err != nil {
					s.recordError(err)
				}
			}
		}
	}()
	var activeCancel context.CancelFunc
	var activeDone chan struct{}
	defer func() {
		cancelTick()
		<-tickDone
		if activeCancel != nil {
			activeCancel()
			<-activeDone
		}
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.recordError(err)
		}
	}()
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil {
				s.recordError(err)
			}
			return
		}
		if activeCancel != nil {
			activeCancel()
			<-activeDone
		}
		clientCtx, cancel := context.WithCancel(ctx)
		activeCancel, activeDone = cancel, make(chan struct{})
		go func(done chan struct{}) {
			defer close(done)
			s.serveClient(clientCtx, conn)
		}(activeDone)
	}
}
func (s *Server) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = errors.Join(s.err, err)
}
func (s *Server) Close() error {
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
