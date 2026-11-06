package cmd

import (
	"context"
	"encoding/json"
	"os"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/spf13/cobra"
)

const historyBuild = "hysteria2-ca8fbd874fd6-history-v1"

func newHistoryObserver() (*connectionhistory.Observer, error) {
	id := os.Getenv("PROXIMA_HISTORY_PRODUCER_ID")
	if id == "" {
		id = "hysteria2"
	}
	return connectionhistory.NewObserver(connectionhistory.Options{
		ProducerID: id, Runtime: "hysteria2", RuntimeBuild: historyBuild,
		Capabilities: []string{"auth_transport", "tcp_flow", "udp_destination", "hot_toggle"},
	})
}

func startHistory(ctx context.Context, config *server.Config) (*connectionhistory.Server, error) {
	o, err := newHistoryObserver()
	if err != nil {
		return nil, err
	}
	config.History = o
	config.HistoryInboundTag = os.Getenv("PROXIMA_HISTORY_INBOUND_TAG")
	if config.HistoryInboundTag == "" {
		config.HistoryInboundTag = "hysteria2"
	}
	path := os.Getenv("PROXIMA_HISTORY_SOCKET")
	if path == "" {
		return nil, nil
	}
	return o.Listen(ctx, path)
}

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use: "history-capabilities", Short: "Print the precise-history build capability JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := newHistoryObserver()
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(o.Hello())
		},
	})
}
