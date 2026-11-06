//go:build linux

package connectionlog

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type wgKernelEvents struct {
	input   <-chan nodeprov.HistoryEvent
	control <-chan nodeprov.HistoryControl
	conn    net.Conn
	all     []nodeprov.HistoryEvent
	ctx     context.Context
}

func wgSubscribe(t *testing.T, ctx context.Context, w *WireGuardObserver) *wgKernelEvents {
	t.Helper()
	c, err := w.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(c)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var hello nodeprov.HistoryHello
	if err := json.Unmarshal(line, &hello); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(c).Encode(nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID}); err != nil {
		t.Fatal(err)
	}
	events := make(chan nodeprov.HistoryEvent, 1024)
	controls := make(chan nodeprov.HistoryControl, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			e, err := nodeprov.DecodeHistoryEvent(line)
			if err != nil {
				control, err := nodeprov.DecodeHistoryControl(line)
				if err != nil {
					t.Error(err)
					return
				}
				select {
				case controls <- control:
				case <-ctx.Done():
					return
				}
				continue
			}
			select {
			case events <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
	return &wgKernelEvents{input: events, control: controls, conn: c, ctx: ctx}
}

func (s *wgKernelEvents) apply(t *testing.T, a nodeprov.HistoryApply) (nodeprov.HistoryApplied, error) {
	t.Helper()
	if err := s.conn.SetWriteDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return nodeprov.HistoryApplied{}, err
	}
	if err := json.NewEncoder(s.conn).Encode(a); err != nil {
		return nodeprov.HistoryApplied{}, err
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case c := <-s.control:
		switch v := c.(type) {
		case nodeprov.HistoryApplied:
			return v, nil
		case nodeprov.HistoryError:
			return nodeprov.HistoryApplied{}, &v
		default:
			t.Fatalf("unexpected control %T", c)
		}
	case <-timer.C:
		t.Fatal("applied deadline exceeded")
	case <-s.ctx.Done():
		t.Fatal(s.ctx.Err())
	}
	return nodeprov.HistoryApplied{}, s.ctx.Err()
}

func (s *wgKernelEvents) wait(t *testing.T, match func(nodeprov.HistoryEvent) bool) nodeprov.HistoryEvent {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e, ok := <-s.input:
			if !ok {
				t.Fatal("export stream closed")
			}
			s.all = append(s.all, e)
			line, err := nodeprov.EncodeHistoryEvent(e)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("EVENT %s", line)
			if match(e) {
				return e
			}
		case <-timer.C:
			t.Fatal("event deadline exceeded")
		case <-s.ctx.Done():
			t.Fatal(s.ctx.Err())
		}
	}
}
