package socketqa

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type collector struct {
	conn   net.Conn
	reader *bufio.Reader
	output io.Writer
}

func (c *collector) send(frame nodeprov.HistoryControl) error {
	b, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(b, '\n'))
	return err
}
func (c *collector) receive() ([]byte, error) {
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if _, err := c.output.Write(line); err != nil {
		return nil, err
	}
	return line, nil
}
func (c *collector) untilApplied() ([]nodeprov.HistoryEvent, nodeprov.HistoryApplied, error) {
	var out []nodeprov.HistoryEvent
	for {
		line, err := c.receive()
		if err != nil {
			return nil, nodeprov.HistoryApplied{}, err
		}
		frame, err := nodeprov.DecodeHistoryControl(line)
		if err == nil {
			if applied, ok := frame.(nodeprov.HistoryApplied); ok {
				return out, applied, nil
			}
			return nil, nodeprov.HistoryApplied{}, fmt.Errorf("unexpected control: %T", frame)
		}
		e, err := nodeprov.DecodeHistoryEvent(line)
		if err != nil {
			return nil, nodeprov.HistoryApplied{}, err
		}
		out = append(out, e)
	}
}
func connect(ctx context.Context, path string, output io.Writer) (*collector, nodeprov.HistoryHello, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, nodeprov.HistoryHello{}, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, nodeprov.HistoryHello{}, errors.Join(err, conn.Close())
	}
	c := &collector{conn: conn, reader: bufio.NewReader(conn), output: output}
	line, err := c.receive()
	if err != nil {
		return nil, nodeprov.HistoryHello{}, errors.Join(err, conn.Close())
	}
	frame, err := nodeprov.DecodeHistoryControl(line)
	if err != nil {
		return nil, nodeprov.HistoryHello{}, errors.Join(err, conn.Close())
	}
	hello, ok := frame.(nodeprov.HistoryHello)
	if !ok {
		return nil, nodeprov.HistoryHello{}, errors.Join(fmt.Errorf("missing hello"), conn.Close())
	}
	return c, hello, nil
}
func value[T any](v T) *T { return &v }
