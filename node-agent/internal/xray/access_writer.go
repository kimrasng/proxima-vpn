package xray

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// accessWriter forwards process output unchanged while indexing complete log
// lines. Oversized lines are skipped until the next newline, not split into
// invented records. Separate instances handle stdout and stderr.
type accessWriter struct {
	mu          sync.Mutex
	destination io.Writer
	index       *AccessIndex
	pending     []byte
	discard     bool
}

const maxAccessLine = 64 << 10

func (w *accessWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.destination.Write(data)
	for remaining := data[:n]; len(remaining) > 0; {
		end := bytes.IndexByte(remaining, '\n')
		part := remaining
		if end >= 0 {
			part = remaining[:end]
		}
		if !w.discard {
			if len(w.pending)+len(part) > maxAccessLine {
				w.pending = nil
				w.discard = true
			} else {
				w.pending = append(w.pending, part...)
			}
		}
		if end < 0 {
			break
		}
		if !w.discard {
			w.index.Observe(string(w.pending), time.Now())
		}
		w.pending = nil
		w.discard = false
		remaining = remaining[end+1:]
	}
	return n, err
}
