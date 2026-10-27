package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// A node renamed in the admin UI has to show up under its new name in the
// subscription. A direct chain copies the node name when the node joins a
// group, so the subscription must not prefer that stale copy.
func TestRenamedNodeShowsNewNameInSubscription(t *testing.T) {
	fixture := newGroupMembershipHTTPFixture(t)
	nodes := NewAdminNodeHandler(fixture.pool, nil, "")
	fixture.app.Put("/admin/nodes/:id", nodes.UpdateNode)

	if status, body := fixture.setNodes(`{"node_ids":["` + fixture.nodeID + `"]}`); status != fiber.StatusOK {
		t.Fatalf("add node status = %d, want 200; body=%s", status, body)
	}
	_, before := fixture.subscription()
	if !strings.Contains(before, fixture.nodeName) {
		t.Fatalf("subscription does not list the original name %q:\n%s", fixture.nodeName, before)
	}

	renamed := "renamed-" + fixture.nodeName
	request := httptest.NewRequest(fiber.MethodPut, "/admin/nodes/"+fixture.nodeID,
		strings.NewReader(`{"name":"`+renamed+`"}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := fixture.app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("rename status = %d, want 200", response.StatusCode)
	}

	_, after := fixture.subscription()
	if !strings.Contains(after, renamed) {
		t.Fatalf("subscription does not show the new name %q:\n%s", renamed, after)
	}
}
