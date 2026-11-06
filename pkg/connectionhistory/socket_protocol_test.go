package connectionhistory

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func socketFixture(t *testing.T) (*Observer, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "history-protocol-")
	check(t, err)
	t.Cleanup(func() { check(t, os.RemoveAll(root)) })
	o := testObserver(t)
	path := filepath.Join(root, "history.sock")
	ctx, cancel := context.WithCancel(context.Background())
	s, err := o.Listen(ctx, path)
	check(t, err)
	t.Cleanup(func() { cancel(); check(t, s.Close()) })
	info, err := os.Stat(path)
	check(t, err)
	if info.Mode().Perm() != 0600 {
		t.Fatal("socket is not private")
	}
	return o, path
}

func TestSocketFragmentedControlsAndFutureACK(t *testing.T) {
	o, path := socketFixture(t)
	conn, err := net.Dial("unix", path)
	check(t, err)
	defer conn.Close()
	check(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	check(t, err)
	if _, err := nodeprov.DecodeHistoryControl(line); err != nil {
		t.Fatal(err)
	}
	resume, err := encodeControl(nodeprov.HistoryResume{Op: "resume", ObserverBootID: o.boot})
	check(t, err)
	_, err = conn.Write(resume[:7])
	check(t, err)
	_, err = conn.Write(resume[7:])
	check(t, err)
	ack, err := encodeControl(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.boot, ThroughSequence: 99})
	check(t, err)
	_, err = conn.Write(ack)
	check(t, err)
	line, err = reader.ReadBytes('\n')
	check(t, err)
	frame, err := nodeprov.DecodeHistoryControl(line)
	check(t, err)
	ce, ok := frame.(nodeprov.HistoryError)
	if !ok || ce.Code != "export_unavailable" {
		t.Fatalf("future ACK accepted: %s", line)
	}
	_, err = conn.Write([]byte(`{"op":"apply","op":"apply","config_revision":"1","collection_epoch":null,"enabled":false,"bindings":[]}` + "\n"))
	check(t, err)
	line, err = reader.ReadBytes('\n')
	check(t, err)
	frame, err = nodeprov.DecodeHistoryControl(line)
	check(t, err)
	ce, ok = frame.(nodeprov.HistoryError)
	if !ok || ce.Code != "binding_invalid" {
		t.Fatalf("malformed control accepted: %s", line)
	}
}

func TestSocketRefusesExistingEndpointAndPublicDirectory(t *testing.T) {
	o, path := socketFixture(t)
	if s, err := o.Listen(context.Background(), path); err == nil {
		check(t, s.Close())
		t.Fatal("existing endpoint replaced")
	}
	root, err := os.MkdirTemp("/tmp", "history-public-")
	check(t, err)
	defer os.RemoveAll(root)
	check(t, os.Chmod(root, 0755))
	if s, err := o.Listen(context.Background(), filepath.Join(root, "public.sock")); err == nil {
		check(t, s.Close())
		t.Fatal("public directory accepted")
	}
}
