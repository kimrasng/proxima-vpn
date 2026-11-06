package connectionlog

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type Producer interface {
	ID() string
	Dial(context.Context) (net.Conn, error)
}

type sourceLine struct {
	line []byte
	err  error
}

func (m *Manager) collect(ctx context.Context, w *producerWorker) error {
	conn, err := w.source.Dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	reader := bufio.NewReaderSize(conn, nodeprov.HistoryMaxControlLine)
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return err
	}
	control, err := nodeprov.DecodeHistoryControl(line)
	if err != nil {
		return err
	}
	hello, ok := control.(nodeprov.HistoryHello)
	if !ok || hello.ProducerID != w.source.ID() {
		return fmt.Errorf("history hello producer mismatch")
	}
	if !slices.Contains(hello.Capabilities, "hot_toggle") {
		return fmt.Errorf("unsupported")
	}
	through, err := m.out.hello(hello)
	if err != nil {
		return err
	}
	if through > hello.LastSequence {
		return fmt.Errorf("history hello regressed behind durable cursor")
	}
	if err := sendControl(conn, nodeprov.HistoryResume{Op: "resume",
		ObserverBootID: hello.ObserverBootID, AfterSequence: through}); err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	lines := make(chan sourceLine)
	done := make(chan struct{})
	readCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(done)
		for {
			line, err := reader.ReadSlice('\n')
			select {
			case lines <- sourceLine{append([]byte(nil), line...), err}:
			case <-readCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { cancel(); conn.Close(); <-done }()
	var sent *nodeprov.HistoryApply
	var waiting bool
	apply := func() error {
		next := m.desiredApply()
		if next != nil {
			runtime := strings.SplitN(w.source.ID(), ":", 2)[0]
			if typed, ok := w.source.(interface{ HistoryRuntime() string }); ok {
				runtime = typed.HistoryRuntime()
			}
			bindings := make([]nodeprov.HistoryBinding, 0)
			for _, binding := range next.Bindings {
				if binding.Runtime == runtime {
					bindings = append(bindings, binding)
				}
			}
			next.Bindings = bindings
		}
		if next == nil || waiting || (sent != nil && reflect.DeepEqual(*sent, *next)) {
			return nil
		}
		if err := sendControl(conn, *next); err != nil {
			return err
		}
		sent, waiting = next, true
		return nil
	}
	if err := apply(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.update:
			if err := apply(); err != nil {
				return err
			}
		case in := <-lines:
			if in.err != nil {
				if len(in.line) > 0 {
					return m.sourceDecodeError(hello.ProducerID, in.err)
				}
				return in.err
			}
			var tag struct {
				Op string `json:"op"`
			}
			if err := json.Unmarshal(in.line, &tag); err != nil {
				return m.sourceDecodeError(hello.ProducerID, err)
			}
			if tag.Op == "" {
				e, err := nodeprov.DecodeHistoryEvent(in.line)
				if err != nil {
					return m.sourceDecodeError(hello.ProducerID, err)
				}
				if e.ProducerID != hello.ProducerID || e.ObserverBootID != hello.ObserverBootID {
					return fmt.Errorf("history event producer mismatch")
				}
				if err := m.out.accept(e); err != nil {
					return err
				}
				if err := sendControl(conn, nodeprov.HistoryAck{Op: "ack",
					ObserverBootID: hello.ObserverBootID, ThroughSequence: e.Sequence}); err != nil {
					return err
				}
				m.mu.Lock()
				m.notifyLocked()
				m.mu.Unlock()
				continue
			}
			frame, err := nodeprov.DecodeHistoryControl(in.line)
			if err != nil {
				return err
			}
			switch v := frame.(type) {
			case nodeprov.HistoryApplied:
				if sent == nil || !waiting || v.ConfigRevision != sent.ConfigRevision ||
					v.Enabled != sent.Enabled || !reflect.DeepEqual(v.CollectionEpoch, sent.CollectionEpoch) {
					return fmt.Errorf("unsolicited history applied barrier")
				}
				if err := m.persistApplied(hello.ProducerID, v); err != nil {
					return err
				}
				m.workerError(w, nil)
				waiting = false
				if err := apply(); err != nil {
					return err
				}
			case nodeprov.HistoryError:
				return &v
			default:
				return fmt.Errorf("unexpected history control frame")
			}
		}
	}
}

func (m *Manager) sourceDecodeError(id string, err error) error {
	m.out.mu.Lock()
	defer m.out.mu.Unlock()
	m.out.noteLoss(id, m.out.disk.Sources[id], "decode_error")
	return errors.Join(err, m.out.saveState())
}

func (m *Manager) persistApplied(id string, a nodeprov.HistoryApplied) error {
	m.out.mu.Lock()
	defer m.out.mu.Unlock()
	cp := m.out.disk.Sources[id]
	if a.BarrierSequence > cp.Through {
		return fmt.Errorf("history applied barrier is not durable")
	}
	prior := cp
	cp.Applied = &a
	now := nodeprov.HistoryTimestamp(m.clock())
	cp.AppliedAt = &now
	if !a.Enabled {
		cp.GapCode = ""
	}
	cp.Hello.ConfigRevision, cp.Hello.Enabled, cp.Hello.CollectionEpoch = a.ConfigRevision, a.Enabled, a.CollectionEpoch
	m.out.disk.Sources[id] = cp
	if err := m.out.saveState(); err != nil {
		m.out.disk.Sources[id] = prior
		return err
	}
	return nil
}
