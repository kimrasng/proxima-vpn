// Package connectionlog durably collects runtime history independently of stats.
package connectionlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type API interface {
	GetConnectionHistoryConfig(context.Context) (client.ConnectionHistoryConfig, error)
	UploadConnectionHistory(context.Context, []byte) (client.ConnectionHistoryACK, error)
}

type Options struct {
	Directory string
	API       API
	Clock     func() time.Time
	Ticks     <-chan time.Time
}

type producerWorker struct {
	source Producer
	update chan struct{}
	err    string
}

type Manager struct {
	mu        sync.Mutex
	pollMu    sync.Mutex
	uploadMu  sync.Mutex
	api       API
	out       *outbox
	clock     func() time.Time
	ticks     <-chan time.Time
	workers   map[string]*producerWorker
	desired   *client.ConnectionHistoryConfig
	fetched   *nodeprov.HistoryTime
	pollErr   string
	uploadErr string
	running   bool
	changed   chan struct{}
}

func NewManager(options Options) (*Manager, error) {
	if options.API == nil || options.Directory == "" {
		return nil, fmt.Errorf("history API and private directory required")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	out, err := openOutbox(options.Directory, options.Clock)
	if err != nil {
		return nil, err
	}
	return &Manager{api: options.API, out: out, clock: options.Clock, ticks: options.Ticks,
		workers: map[string]*producerWorker{}, changed: make(chan struct{})}, nil
}

func (m *Manager) Register(source Producer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.notifyLocked()
	if source == nil || source.ID() == "" || source.ID() == "agent" || len(source.ID()) > 128 {
		return fmt.Errorf("invalid history producer")
	}
	if m.running || len(m.workers) >= maxProducers {
		return fmt.Errorf("register producers before Run (maximum %d)", maxProducers)
	}
	if _, exists := m.workers[source.ID()]; exists {
		return fmt.Errorf("duplicate history producer %q", source.ID())
	}
	m.workers[source.ID()] = &producerWorker{source: source, update: make(chan struct{}, 1), err: "export_unavailable"}
	return nil
}

func (m *Manager) Poll(ctx context.Context) error {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()
	cfg, err := m.api.GetConnectionHistoryConfig(ctx)
	if err == nil {
		err = cfg.Validate()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil && m.desired != nil {
		a, _ := json.Marshal(m.desired)
		b, _ := json.Marshal(cfg)
		if cfg.Revision < m.desired.Revision || (cfg.Revision == m.desired.Revision && !bytes.Equal(a, b)) {
			err = fmt.Errorf("history desired revision stale or conflicting")
		}
	}
	if err != nil {
		m.pollErr = "config_unavailable"
	} else {
		// Own the manifest; callers cannot mutate worker-visible desired content.
		data, marshalErr := json.Marshal(cfg)
		if marshalErr != nil {
			return marshalErr
		}
		var copy client.ConnectionHistoryConfig
		if err := json.Unmarshal(data, &copy); err != nil {
			return err
		}
		m.desired = &copy
		now := nodeprov.HistoryTimestamp(m.clock())
		m.fetched, m.pollErr = &now, ""
	}
	for _, w := range m.workers {
		select {
		case w.update <- struct{}{}:
		default:
		}
	}
	m.notifyLocked()
	return err
}

func (m *Manager) desiredApply() *nodeprov.HistoryApply {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.desired == nil {
		return nil
	}
	a := m.desired.Apply()
	return &a
}

func (m *Manager) workerError(w *producerWorker, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.err = ""
	if err != nil {
		w.err = err.Error()
	}
	m.notifyLocked()
}

func (m *Manager) notifyLocked() { close(m.changed); m.changed = make(chan struct{}) }

// Changes subscribes before a caller reads Status; each mutation closes the channel.
func (m *Manager) Changes() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.changed
}

func (m *Manager) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("history manager already running")
	}
	m.running = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.running = false; m.mu.Unlock() }()
	// A config outage never prevents resume or old-epoch upload.
	m.Poll(ctx)
	var wg sync.WaitGroup
	for _, w := range m.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				err := m.collect(ctx, w)
				m.workerError(w, err)
				select {
				case <-ctx.Done():
					return
				case <-w.update:
				}
			}
		}()
	}
	upload := make(chan struct{}, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-upload:
				m.UploadOnce(ctx)
			}
		}
	}()
	upload <- struct{}{}
	defer func() { cancel(); wg.Wait() }()
	ticks := m.ticks
	if ticks == nil {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ticks:
			if !ok {
				return fmt.Errorf("history tick subscription closed")
			}
			m.Poll(ctx)
			select {
			case upload <- struct{}{}:
			default:
			}
		}
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return fmt.Errorf("cancel and join history Run before Close")
	}
	return m.out.close()
}
