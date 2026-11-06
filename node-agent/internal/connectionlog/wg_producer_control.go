package connectionlog

import (
	"bufio"
	"context"
	"errors"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func (s *wgProducerSession) read(ctx context.Context) {
	reader := bufio.NewReaderSize(s.conn, nodeprov.HistoryMaxControlLine)
	resumed := false
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return
		}
		control, err := nodeprov.DecodeHistoryControl(line)
		if err != nil {
			return
		}
		var response nodeprov.HistoryControl
		switch v := control.(type) {
		case nodeprov.HistoryResume:
			if resumed {
				err = errors.New("already resumed")
				break
			}
			_, err = s.owner.Resume(v)
			if err == nil {
				resumed = true
				response = v
			}
		case nodeprov.HistoryApply:
			if !resumed {
				err = errors.New("resume required")
				break
			}
			// This is deliberately NOT s.owner.history.Apply.
			response, err = s.owner.Apply(ctx, v)
		case nodeprov.HistoryAck:
			if !resumed {
				err = errors.New("resume required")
				break
			}
			err = s.ack(ctx, v)
		default:
			err = errors.New("unsupported control")
		}
		if err != nil {
			var wire *nodeprov.HistoryError
			if errors.As(err, &wire) {
				response = *wire
			} else {
				code := "export_unavailable"
				if errors.Is(err, ErrWireGuardUnsupported) {
					code = "unsupported"
				}
				response = nodeprov.HistoryError{Op: "error", ConfigRevision: s.owner.Hello().ConfigRevision, Code: code}
			}
		}
		if response != nil {
			// A bounded mailbox; a peer flooding controls without reading
			// responses is disconnected, not allowed to block capture or ACK.
			select {
			case s.commands <- response:
			case <-ctx.Done():
				return
			default:
				return
			}
		}
	}
}
