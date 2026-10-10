package handlers

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// The old per-field APIs could not move a node off its camouflage site: the
// node SNI had to match the listeners and the listeners had to match the node.
// SetRealityTarget changes both atomically and validates only the end state.
func TestSetRealityTargetMovesNodeAndListenersAtomically(t *testing.T) {
	db := chainTestDB(t)
	ctx := context.Background()
	node := seedChainNode(t, db, "reality-target-"+crypto.NewUUID(), "exit", 443)
	t.Cleanup(func() { _, _ = db.Exec(ctx, `DELETE FROM inbounds WHERE node_id=$1`, node) })
	if _, err := db.Exec(ctx, `UPDATE nodes SET reality_client_sni='www.microsoft.com', reality_sni_source='admin', reality_sni_status='valid' WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO inbounds (node_id, protocol, port, tag, settings, enabled) VALUES ($1,'vless_reality',8443,'vless-in','{"dest":"www.microsoft.com:443","server_names":["www.microsoft.com"],"keep":"me"}',true)`, node); err != nil {
		t.Fatal(err)
	}

	var probed []string
	probeErr := error(nil)
	h := &AdminNodeHandler{db: db, activity: NewAdminNodeHandler(db, nil, "").activity,
		realityProbe: func(_ context.Context, host string, port int) error {
			probed = append(probed, host)
			return probeErr
		}}
	app := fiber.New()
	app.Put("/nodes/:id/reality-target", h.SetRealityTarget)
	t.Cleanup(func() { _ = app.Shutdown() })
	put := func(body string) (int, string) {
		r := httptest.NewRequest("PUT", "/nodes/"+node+"/reality-target", strings.NewReader(body))
		r.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		res, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	state := func() (sni, dest, names, keep string) {
		if err := db.QueryRow(ctx, `SELECT n.reality_client_sni, i.settings->>'dest', (i.settings->'server_names')::text, i.settings->>'keep' FROM nodes n JOIN inbounds i ON i.node_id=n.id WHERE n.id=$1`, node).Scan(&sni, &dest, &names, &keep); err != nil {
			t.Fatal(err)
		}
		return
	}

	// Failed pre-flight: 422 and nothing changes.
	probeErr = errors.New("handshake failed")
	if code, _ := put(`{"hostname":"www.apple.com"}`); code != 422 {
		t.Fatalf("unreachable target = %d, want 422", code)
	}
	if sni, dest, _, _ := state(); sni != "www.microsoft.com" || dest != "www.microsoft.com:443" {
		t.Fatalf("rejected change was persisted: %s %s", sni, dest)
	}

	// Successful change moves node and listener together, keeping other keys.
	probeErr = nil
	if code, body := put(`{"hostname":"WWW.Apple.com"}`); code != 200 || !strings.Contains(body, `"www.apple.com"`) {
		t.Fatalf("change = %d %s", code, body)
	}
	sni, dest, names, keep := state()
	if sni != "www.apple.com" || dest != "www.apple.com:443" || names != `["www.apple.com"]` || keep != "me" {
		t.Fatalf("after change: sni=%s dest=%s names=%s keep=%s", sni, dest, names, keep)
	}
	if len(probed) != 2 || probed[1] != "www.apple.com" {
		t.Fatalf("probe calls = %v", probed)
	}

	// Malformed host is rejected before probing.
	if code, _ := put(`{"hostname":"https://x/"}`); code != 400 {
		t.Fatalf("malformed host = %d, want 400", code)
	}
	if len(probed) != 2 {
		t.Fatal("malformed host must not be probed")
	}
}
