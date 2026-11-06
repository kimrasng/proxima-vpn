package connectionlog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// ID and Dial implement lane A's Producer without depending on its concrete
// manager. Replay, wire validation and owner state remain in the shared package.
func (w *WireGuardObserver) ID() string { return "wireguard:" + w.options.Interface }

func (w *WireGuardObserver) Dial(ctx context.Context) (net.Conn, error) {
	w.producerMu.Lock()
	defer w.producerMu.Unlock()
	w.op.Lock()
	closed := w.closed
	w.op.Unlock()
	if closed {
		return nil, ErrWireGuardClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.producer != nil {
		w.producer.cancel()
		<-w.producer.done
	}
	client, server := net.Pipe()
	ctx, cancel := context.WithCancel(ctx)
	s := &wgProducerSession{owner: w, conn: server, cancel: cancel, done: make(chan struct{}), commands: make(chan nodeprov.HistoryControl, 64)}
	w.producer = s
	go s.run(ctx)
	return client, nil
}

func (w *WireGuardObserver) closeProducer() {
	w.producerMu.Lock()
	defer w.producerMu.Unlock()
	if w.producer != nil {
		w.producer.cancel()
		<-w.producer.done
		w.producer = nil
	}
}

func (w *WireGuardObserver) notifyProducer() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

type wgProducerSession struct {
	owner    *WireGuardObserver
	conn     net.Conn
	cancel   context.CancelFunc
	done     chan struct{}
	commands chan nodeprov.HistoryControl
	delivery sync.Mutex
	cursor   nodeprov.HistoryU64
	inflight nodeprov.HistoryU64
	written  chan struct{}
}

func (s *wgProducerSession) run(ctx context.Context) {
	defer close(s.done)
	defer s.cancel()
	stop := context.AfterFunc(ctx, func() { s.conn.Close() })
	defer stop()
	defer s.conn.Close()
	readerDone := make(chan struct{})
	go func() { defer close(readerDone); defer s.cancel(); s.read(ctx) }()
	defer func() { s.cancel(); s.conn.Close(); <-readerDone }()
	if err := s.control(s.owner.Hello()); err != nil {
		return
	}
	resumed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.owner.wake:
			if resumed {
				if err := s.flush(); err != nil {
					return
				}
			}
		case command := <-s.commands:
			switch v := command.(type) {
			case nodeprov.HistoryResume:
				s.delivery.Lock()
				s.cursor = v.AfterSequence
				s.delivery.Unlock()
				resumed = true
				if err := s.flush(); err != nil {
					return
				}
			case nodeprov.HistoryApplied:
				// Events through this barrier are written BEFORE applied.
				if err := s.flush(); err != nil {
					return
				}
				if err := s.control(v); err != nil {
					return
				}
			case nodeprov.HistoryError:
				if err := s.control(v); err != nil {
					return
				}
			default:
				return
			}
		}
	}
}

func (s *wgProducerSession) flush() error {
	s.delivery.Lock()
	cursor := s.cursor
	s.delivery.Unlock()
	frames, err := s.owner.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: s.owner.Hello().ObserverBootID, AfterSequence: cursor})
	if err != nil {
		return err
	}
	for _, line := range frames {
		e, err := nodeprov.DecodeHistoryEvent(line)
		if err != nil {
			return err
		}
		s.delivery.Lock()
		s.inflight, s.written = e.Sequence, make(chan struct{})
		done := s.written
		s.delivery.Unlock()
		err = s.write(line)
		s.delivery.Lock()
		if err == nil {
			s.cursor = e.Sequence
		}
		s.inflight = 0
		close(done)
		s.delivery.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *wgProducerSession) ack(ctx context.Context, a nodeprov.HistoryAck) error {
	s.delivery.Lock()
	cursor, flight, done := s.cursor, s.inflight, s.written
	s.delivery.Unlock()
	if a.ThroughSequence > cursor {
		if a.ThroughSequence > flight {
			return errors.New("ACK beyond delivered cursor")
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
		s.delivery.Lock()
		cursor = s.cursor
		s.delivery.Unlock()
		if a.ThroughSequence > cursor {
			return errors.New("ACK before completed write")
		}
	}
	return s.owner.Ack(a)
}

func (s *wgProducerSession) control(c nodeprov.HistoryControl) error {
	line, err := json.Marshal(c)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := nodeprov.DecodeHistoryControl(line); err != nil {
		return err
	}
	return s.write(line)
}

func (s *wgProducerSession) write(line []byte) error {
	if err := s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	for len(line) > 0 {
		n, err := s.conn.Write(line)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		line = line[n:]
	}
	return nil
}
