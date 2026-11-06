package connectionlog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// ControlProducer is implemented by an in-agent observer wrapper, not its raw
// exporter. Apply must include capture-open/close and return the real barrier.
// Changes returns a broadcast channel closed/replaced on every replay advance.
type ControlProducer interface {
	Hello() nodeprov.HistoryHello
	Apply(context.Context, nodeprov.HistoryApply) (nodeprov.HistoryApplied, error)
	Resume(nodeprov.HistoryResume) ([][]byte, error)
	Ack(nodeprov.HistoryAck) error
	Changes() <-chan struct{}
}

type controlSource struct {
	id, runtime string
	control     ControlProducer
}

func NewControlSource(id, runtime string, control ControlProducer) (Producer, error) {
	if control == nil || id == "" || (runtime != "xray" && runtime != "hysteria2" && runtime != "wireguard") {
		return nil, fmt.Errorf("invalid direct history producer")
	}
	return controlSource{id: id, runtime: runtime, control: control}, nil
}

func (s controlSource) ID() string             { return s.id }
func (s controlSource) HistoryRuntime() string { return s.runtime }

type joinedSourceConn struct {
	net.Conn
	done <-chan struct{}
}

type controlDelivery struct {
	mu        sync.Mutex
	ready     bool
	delivered nodeprov.HistoryU64
	announced nodeprov.HistoryU64
	changed   chan struct{}
}

func (d *controlDelivery) ack(ctx context.Context, boot nodeprov.HistoryUUID, a nodeprov.HistoryAck) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.ready || a.ObserverBootID != boot || a.ThroughSequence > d.announced {
		return fmt.Errorf("ACK exceeds direct producer delivery")
	}
	for a.ThroughSequence > d.delivered {
		changed := d.changed
		d.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			d.mu.Lock()
			return ctx.Err()
		}
		d.mu.Lock()
	}
	return nil
}

func (c joinedSourceConn) Close() error {
	err := c.Conn.Close()
	<-c.done
	return err
}

func (s controlSource) Dial(ctx context.Context) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serve(ctx, server)
	}()
	return joinedSourceConn{Conn: client, done: done}, nil
}

func (s controlSource) serve(ctx context.Context, conn net.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	hello := s.control.Hello()
	if hello.ProducerID != s.id || sendControl(conn, hello) != nil {
		return
	}
	// There is only one apply in flight. ACKs bypass this queue, so an exporter
	// flushing replay cannot deadlock with a collector fsyncing and ACKing it.
	commands := make(chan nodeprov.HistoryControl, 1)
	done := make(chan struct{})
	delivery := &controlDelivery{changed: make(chan struct{})}
	go func() {
		defer close(done)
		defer cancel()
		reader := bufio.NewReaderSize(conn, nodeprov.HistoryMaxControlLine)
		for {
			line, err := reader.ReadSlice('\n')
			if err != nil {
				return
			}
			command, err := nodeprov.DecodeHistoryControl(line)
			if err != nil {
				return
			}
			if ack, ok := command.(nodeprov.HistoryAck); ok {
				if delivery.ack(ctx, hello.ObserverBootID, ack) != nil || s.control.Ack(ack) != nil {
					return
				}
				continue
			}
			select {
			case commands <- command:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); conn.Close(); <-done }()
	var cursor nodeprov.HistoryU64
	var resumed bool
	flush := func() error {
		frames, err := s.control.Resume(nodeprov.HistoryResume{Op: "resume",
			ObserverBootID: hello.ObserverBootID, AfterSequence: cursor})
		if err != nil {
			return err
		}
		for _, frame := range frames {
			event, err := nodeprov.DecodeHistoryEvent(frame)
			if err != nil {
				return err
			}
			if event.ObserverBootID != hello.ObserverBootID || event.ProducerID != hello.ProducerID {
				return fmt.Errorf("direct history producer changed lifetime")
			}
			delivery.mu.Lock()
			delivery.announced = event.Sequence
			delivery.mu.Unlock()
			if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return err
			}
			// Pipe writes can be partial, just like a Unix stream.
			for len(frame) > 0 {
				n, err := conn.Write(frame)
				if err != nil {
					return err
				}
				frame = frame[n:]
			}
			cursor = event.Sequence
			delivery.mu.Lock()
			delivery.delivered = cursor
			close(delivery.changed)
			delivery.changed = make(chan struct{})
			delivery.mu.Unlock()
		}
		return nil
	}
	for {
		changed := s.control.Changes()
		if changed == nil {
			return
		}
		if resumed {
			if err := flush(); err != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
			continue
		case frame := <-commands:
			switch command := frame.(type) {
			case nodeprov.HistoryResume:
				if _, err := s.control.Resume(command); err != nil {
					return
				}
				cursor, resumed = command.AfterSequence, true
				delivery.mu.Lock()
				delivery.ready, delivery.delivered, delivery.announced = true, cursor, cursor
				delivery.mu.Unlock()
			case nodeprov.HistoryApply:
				if !resumed {
					return
				}
				applied, err := s.control.Apply(ctx, command)
				if err != nil {
					var controlErr *nodeprov.HistoryError
					if !errors.As(err, &controlErr) {
						controlErr = &nodeprov.HistoryError{Op: "error", ConfigRevision: command.ConfigRevision, Code: "export_unavailable"}
					}
					sendControl(conn, *controlErr)
					return
				}
				if err := flush(); err != nil {
					return
				}
				if err := sendControl(conn, applied); err != nil {
					return
				}
			default:
				return
			}
		}
	}
}
