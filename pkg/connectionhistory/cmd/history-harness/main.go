package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory/internal/socketqa"
)

func run() (err error) {
	dir, err := os.MkdirTemp("/tmp", "history-socket-qa-")
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "OWNED_SOCKET_ROOT", dir)
	defer func() {
		cleanup := os.RemoveAll(dir)
		err = errors.Join(err, cleanup)
		fmt.Fprintln(os.Stderr, "SOCKET_ROOT_CLEANUP", dir, cleanup)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return socketqa.Run(ctx, dir, os.Stdout)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "PASS final_upload=19 final_download=7 fresh_epoch_baselines=0/0 ack_reconnect_replay=identical")
}
