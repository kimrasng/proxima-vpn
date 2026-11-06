package mux

import (
	"errors"
	"io"
	"testing"

	"github.com/xtls/xray-core/common/buf"
)

type observedWrite struct {
	entered chan struct{}
	release chan struct{}
}

func (w *observedWrite) WriteMultiBuffer(b buf.MultiBuffer) error {
	close(w.entered)
	<-w.release
	buf.ReleaseMulti(b)
	return nil
}

func TestHistoryCloseRejectsNewWritesAndJoinsAcceptedWrite(t *testing.T) {
	// Given one accepted write paused inside I/O, not under the session lock.
	writer := &observedWrite{entered: make(chan struct{}), release: make(chan struct{})}
	s := &Session{ID: 1, parent: NewSessionManager(), output: writer}
	if !s.parent.Add(s) {
		t.Fatal("session registration failed")
	}
	finished := make(chan error, 1)
	go func() { finished <- s.WriteMultiBuffer(buf.MergeBytes(nil, []byte("accepted"))) }()
	<-writer.entered
	// When closure races the accepted writer, later writes are rejected.
	if err := s.Close(false); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteMultiBuffer(buf.MergeBytes(nil, []byte("late"))); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("late write: %v", err)
	}
	// Then quiescence waits for exactly the already accepted operation.
	joined := make(chan struct{})
	go func() { s.historyWriters.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("joined before the accepted write returned")
	default:
	}
	close(writer.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	<-joined
}
