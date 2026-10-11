package handlers

import (
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestBuildInstallCommandPerOSFamily(t *testing.T) {
	h := &AdminNodeHandler{panelURL: "https://panel.test"}

	for _, family := range []nodeprov.OSFamily{nodeprov.OSDebian, nodeprov.OSRHEL, ""} {
		cmd := h.buildInstallCommand(family, "tok", "node one", "", "", 443, "443,20001-22000")
		if !strings.HasPrefix(cmd, "bash <(curl -s https://panel.test/scripts/install.sh) --server https://panel.test --token tok") {
			t.Errorf("%q: command = %q", family, cmd)
		}
	}

	// Alpine's busybox ash has no bash and no process substitution, so the
	// command must install bash and run a downloaded file instead.
	cmd := h.buildInstallCommand(nodeprov.OSAlpine, "tok", "node one", "KR", "", 8443, "")
	if strings.Contains(cmd, "<(") {
		t.Fatalf("alpine command uses process substitution: %q", cmd)
	}
	for _, want := range []string{
		"apk add --no-cache -q bash curl && ",
		"curl -fsSL https://panel.test/scripts/install.sh -o /tmp/proxima-install.sh && ",
		"bash /tmp/proxima-install.sh --server https://panel.test --token tok --port 8443",
		"--name 'node one'",
		"--country 'KR'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("alpine command %q lacks %q", cmd, want)
		}
	}
}
