// The small adapter invokes the real API-side public config transformer without
// starting an application or constructing any database/client services.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func main() {
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 1024*1024))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read config:", err)
		os.Exit(1)
	}
	output, err := services.WithDeviceEgressRouting(input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate routing:", err)
		os.Exit(1)
	}
	if _, err = os.Stdout.Write(output); err != nil {
		fmt.Fprintln(os.Stderr, "write config:", err)
		os.Exit(1)
	}
}
