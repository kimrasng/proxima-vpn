package main

import (
	"fmt"
	"os"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/process"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
	"github.com/spf13/cobra"
)

// Runtime installation is an explicit maintenance operation, not a logging
// toggle. Both installation and Xray's updater use the same pin/hash/probe gate.
func installHistoryRuntimeCmd() *cobra.Command {
	var manifest, runtime, destination string
	cmd := &cobra.Command{
		Use:   "install-runtime",
		Short: "Install a pinned history runtime from a local artifact manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			if destination == "" {
				switch runtime {
				case "xray":
					destination = xray.DefaultBinaryPath
				case "hysteria2":
					destination = process.DefaultHysteria2BinaryPath
				default:
					return fmt.Errorf("runtime must be xray or hysteria2")
				}
			}
			staged, err := xray.StageHistoryRuntime(cmd.Context(), manifest, runtime, destination, "")
			if err != nil {
				return err
			}
			defer os.Remove(staged)
			if current, err := os.ReadFile(destination); err == nil {
				if err := os.WriteFile(destination+".prev", current, 0o755); err != nil {
					return fmt.Errorf("back up runtime: %w", err)
				}
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := os.Rename(staged, destination); err != nil {
				return fmt.Errorf("install runtime: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed verified %s history runtime\n", runtime)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifest, "manifest", "/etc/node-agent/runtime-manifest.json", "Local approved artifact manifest")
	cmd.Flags().StringVar(&runtime, "runtime", "", "xray or hysteria2")
	cmd.Flags().StringVar(&destination, "destination", "", "Executable destination (defaults to installed runtime path)")
	return cmd
}
