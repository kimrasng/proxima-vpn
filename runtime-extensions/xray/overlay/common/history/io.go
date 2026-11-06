package history

import (
	"context"
	"errors"
	"io"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/transport"
)

type Reader struct {
	buf.Reader
	a      *Association
	upload bool
}

func (r *Reader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	r.a.enter()
	mb, err := r.Reader.ReadMultiBuffer()
	r.a.observe(mb, r.upload)
	r.a.failure(err)
	r.a.leave(r.upload, err != nil)
	return mb, err
}

func (r *Reader) Interrupt() {
	r.a.enter()
	report(common.Interrupt(r.Reader))
	r.a.leave(r.upload, true)
}

type Writer struct {
	buf.Writer
	a      *Association
	upload bool
}

func (w *Writer) WriteMultiBuffer(mb buf.MultiBuffer) error {
	w.a.enter()
	w.a.observe(mb, w.upload)
	err := w.Writer.WriteMultiBuffer(mb)
	w.a.failure(err)
	w.a.leave(w.upload, err != nil)
	return err
}

func (a *Association) failure(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if errors.Is(err, io.EOF) {
		if a.endReason == "" {
			a.endReason = "eof"
		}
	} else if !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, context.Canceled) {
		a.endReason = "io_error"
	}
}

func (w *Writer) Close() error {
	w.a.enter()
	err := common.Close(w.Writer)
	w.a.leave(w.upload, true)
	return err
}

func (w *Writer) Interrupt() {
	w.a.enter()
	report(common.Interrupt(w.Writer))
	w.a.leave(w.upload, true)
}

// Run preserves task.Run's early return and cancellation behavior. Registration
// happens before launching tasks; their deferred completion prevents final
// snapshots while a sibling continues after the first error.
func Run(ctx context.Context, tasks ...func() error) error {
	a, _ := ctx.Value(associationKey{}).(*Association)
	return run(ctx, a, tasks...)
}

func RunLink(ctx context.Context, link *transport.Link, tasks ...func() error) error {
	var a *Association
	if w, ok := link.Writer.(*Writer); ok {
		a = w.a
	}
	return run(ctx, a, tasks...)
}

func run(ctx context.Context, a *Association, tasks ...func() error) error {
	o := FromContext(ctx)
	// An association owns its forwarding callbacks. In particular a retained
	// XUDP outbound outlives its carrier and must not pin that carrier's tasks.
	if a != nil {
		o = nil
	}
	if o != nil {
		o.mu.Lock()
		o.tasks += len(tasks)
		o.mu.Unlock()
	}
	if a != nil {
		a.mu.Lock()
		a.tasks += len(tasks)
		a.mu.Unlock()
	}
	wrapped := make([]func() error, len(tasks))
	for i, fn := range tasks {
		wrapped[i] = func() error {
			defer func() {
				if a != nil {
					a.mu.Lock()
					a.tasks--
					a.finishLocked()
					a.mu.Unlock()
				}
				if o != nil {
					o.mu.Lock()
					o.tasks--
					o.finishLocked()
					o.mu.Unlock()
				}
			}()
			return fn()
		}
	}
	return task.Run(ctx, wrapped...)
}
