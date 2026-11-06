package connectionhistory_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory/internal/socketqa"
)

func TestRealUnixSocketApplyResumeAckAndCallbacks(t *testing.T) {
	// Given an owned short path (Darwin Unix sockaddr has a 104-byte bound).
	root, err := os.MkdirTemp("/tmp", "history-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wire bytes.Buffer
	// When actual socket controls interleave with the runtime-owned callbacks.
	err = socketqa.Run(ctx, root, &wire)
	// Then the harness has asserted 19/7, partial zero baselines, gate silence,
	// ACK reclamation and identical replay following a lost ACK.
	if err != nil {
		t.Fatalf("%v\n%s", err, wire.String())
	}
	if bytes.Contains(wire.Bytes(), []byte("private-authenticated-label")) {
		t.Fatal("secret label leaked")
	}
}
