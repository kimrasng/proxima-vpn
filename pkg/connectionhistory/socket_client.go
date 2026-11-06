package connectionhistory

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type incoming struct {
	frame nodeprov.HistoryControl
	err   error
}

func (s *Server) serveClient(ctx context.Context, conn *net.UnixConn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { closeClient(conn) })
	defer stop()
	defer closeClient(conn)
	requests := make(chan incoming)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		r := bufio.NewReaderSize(conn, nodeprov.HistoryMaxControlLine)
		for {
			line, err := r.ReadSlice('\n')
			var frame nodeprov.HistoryControl
			if err == nil {
				frame, err = nodeprov.DecodeHistoryControl(line)
			}
			select {
			case requests <- incoming{frame, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { cancel(); closeClient(conn); <-readerDone }()
	hello := s.observer.Hello()
	if err := writeControl(conn, hello); err != nil {
		return
	}
	var cursor nodeprov.HistoryU64
	resumed := false
	flush := func() error {
		frames, err := s.observer.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID, AfterSequence: cursor})
		if err != nil {
			return err
		}
		for _, line := range frames {
			if err := writeLine(conn, line); err != nil {
				return err
			}
			e, err := nodeprov.DecodeHistoryEvent(line)
			if err != nil {
				return err
			}
			cursor = e.Sequence
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.observer.wake:
			if resumed {
				if err := flush(); err != nil {
					return
				}
			}
		case req := <-requests:
			if req.err != nil {
				if !errors.Is(req.err, io.EOF) && !errors.Is(req.err, net.ErrClosed) {
					if err := writeControl(conn, nodeprov.HistoryError{Op: "error", ConfigRevision: s.observer.Hello().ConfigRevision, Code: "binding_invalid"}); err != nil {
						return
					}
				}
				return
			}
			var commandErr error
			switch v := req.frame.(type) {
			case nodeprov.HistoryResume:
				if _, err := s.observer.Resume(v); err != nil {
					commandErr = err
					break
				}
				cursor, resumed = v.AfterSequence, true
				commandErr = flush()
			case nodeprov.HistoryApply:
				if !resumed {
					commandErr = controlError(v.ConfigRevision, "export_unavailable")
					break
				}
				applied, err := s.observer.Apply(v)
				if err != nil {
					commandErr = err
					break
				}
				if err := flush(); err != nil {
					commandErr = err
					break
				}
				commandErr = writeControl(conn, applied)
			case nodeprov.HistoryAck:
				if !resumed || v.ThroughSequence > cursor {
					commandErr = controlError(s.observer.Hello().ConfigRevision, "export_unavailable")
				} else {
					commandErr = s.observer.Ack(v)
				}
			default:
				commandErr = controlError(s.observer.Hello().ConfigRevision, "unsupported")
			}
			if commandErr != nil {
				var ce *nodeprov.HistoryError
				if !errors.As(commandErr, &ce) {
					return
				}
				if err := writeControl(conn, *ce); err != nil {
					return
				}
			}
		}
	}
}

// Closing a peer is shutdown, not a source of observation loss. Network errors
// leave replay untouched; reconnect resumes from the durable collector cursor.
func closeClient(conn *net.UnixConn) {
	conn.Close()
}
func writeControl(conn *net.UnixConn, c nodeprov.HistoryControl) error {
	line, err := encodeControl(c)
	if err != nil {
		return err
	}
	return writeLine(conn, line)
}
func writeLine(conn *net.UnixConn, line []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	for len(line) > 0 {
		n, err := conn.Write(line)
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
