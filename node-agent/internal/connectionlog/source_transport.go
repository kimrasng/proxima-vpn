package connectionlog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type unixSource struct{ id, path string }

func NewUnixSource(id, path string) Producer { return unixSource{id, path} }
func (s unixSource) ID() string              { return s.id }
func (s unixSource) Dial(ctx context.Context) (net.Conn, error) {
	dir, err := os.Lstat(filepath.Dir(s.path))
	if err != nil {
		return nil, err
	}
	socket, err := os.Lstat(s.path)
	if err != nil {
		return nil, err
	}
	if !dir.IsDir() || dir.Mode().Perm() != 0o700 || socket.Mode()&os.ModeSocket == 0 || socket.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("history socket must be private (0700 directory, 0600 socket)")
	}
	for _, info := range []os.FileInfo{dir, socket} {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
			return nil, fmt.Errorf("history socket owner must be service UID or root")
		}
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", s.path)
}

func sendControl(conn net.Conn, frame nodeprov.HistoryControl) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := nodeprov.DecodeHistoryControl(data); err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
