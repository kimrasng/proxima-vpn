package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

func planHTTP(t *testing.T, app *fiber.App, method, path, body string, status int) []byte {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != status {
		t.Fatalf("%s %s: status=%d body=%s; want %d", method, path, res.StatusCode, data, status)
	}
	return data
}

func TestAdminPlanIDValidation(t *testing.T) {
	app := fiber.New()
	h := NewAdminPlanHandler(nil)
	app.Post("/plans", h.Create)
	app.Put("/plans/:id", h.Update)
	for _, id := range []string{`""`, `"not-a-uuid"`, `123`} {
		planHTTP(t, app, "POST", "/plans", `{"id":`+id+`,"name":"Valid","duration_days":30,"max_devices":1,"node_group_id":"00000000-0000-0000-0000-000000000001"}`, 400)
	}
	for _, body := range []string{`{"id":"00000000-0000-0000-0000-000000000001"}`, `{"id":null}`, `{"advertise":null}`, `{"is_advertised":null}`} {
		planHTTP(t, app, "PUT", "/plans/example", body, 400)
	}
}

func TestPlanPublicationWorkflow(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	userID, fixturePlan, groupID := seedFreeOrderData(t, pool, ctx, fmt.Sprintf("publication-%d", os.Getpid()))
	_ = fixturePlan
	app := fiber.New()
	h := NewAdminPlanHandler(pool)
	app.Post("/plans", h.Create)
	app.Get("/plans", h.List)
	app.Get("/plans/:id", h.Get)
	app.Put("/plans/:id", h.Update)
	app.Delete("/plans/:id", h.Delete)
	app.Get("/user/plans", NewUserPlanHandler(pool).ListPlans)
	app.Post("/orders", func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return NewUserOrderHandler(pool, config.PaymentsConfig{PendingTTLRaw: "1h"}).Create(c)
	})
	var customID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&customID); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"id":%q,"name":"Publication","duration_days":30,"max_devices":1,"node_group_id":%q,"advertise":true,"is_advertised":true}`, customID, groupID)
	var plan planResponse
	if err := json.Unmarshal(planHTTP(t, app, "POST", "/plans", body, 201), &plan); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM plan_orders WHERE plan_id=$1`, customID)
		_, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, customID)
	})
	if plan.ID != customID || !plan.Advertise || plan.IsAdvertised {
		t.Fatalf("create: %+v", plan)
	}
	planHTTP(t, app, "POST", "/plans", body, 409)
	generated := planHTTP(t, app, "POST", "/plans", fmt.Sprintf(`{"name":"Generated","duration_days":30,"max_devices":1,"node_group_id":%q}`, groupID), 201)
	var auto planResponse
	if err := json.Unmarshal(generated, &auto); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, auto.ID) })
	if auto.ID == "" || auto.Advertise || auto.IsAdvertised {
		t.Fatalf("defaults: %+v", auto)
	}
	path := "/plans/" + customID
	planHTTP(t, app, "PUT", path, `{"is_advertised":true,"name":"Must rollback"}`, 400)
	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM plans WHERE id=$1`, customID).Scan(&name); err != nil || name != "Publication" {
		t.Fatalf("failed publish did not roll back: %s %v", name, err)
	}
	check := func(advertised bool) {
		t.Helper()
		var got planResponse
		if err := json.Unmarshal(planHTTP(t, app, "GET", path, "", 200), &got); err != nil {
			t.Fatal(err)
		}
		if got.IsAdvertised != advertised {
			t.Fatalf("publication: %+v", got)
		}
		var listed []userPlanItem
		if err := json.Unmarshal(planHTTP(t, app, "GET", "/user/plans?lang=en", "", 200), &listed); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range listed {
			if item.ID == customID {
				found = true
			}
		}
		if found != advertised {
			t.Fatalf("user visibility=%v want %v", found, advertised)
		}
		if !advertised {
			planHTTP(t, app, "POST", "/orders", fmt.Sprintf(`{"plan_id":%q,"duration_days":30}`, customID), 400)
		}
	}
	check(false)
	planHTTP(t, app, "PUT", path, `{"prices":[{"duration_days":30,"price_cents":0}]}`, 200)
	check(false)
	planHTTP(t, app, "PUT", path, `{"is_advertised":true}`, 200)
	check(true)
	planHTTP(t, app, "PUT", path, `{"name":"Edited"}`, 200)
	check(true)
	for _, disable := range []string{`{"advertise":false}`, `{"is_active":false}`, `{"is_advertised":false}`, `{"prices":[]}`} {
		planHTTP(t, app, "PUT", path, disable, 200)
		check(false)
		planHTTP(t, app, "PUT", path, `{"advertise":true,"is_active":true,"prices":[{"duration_days":30,"price_cents":0}]}`, 200)
		check(false)
		planHTTP(t, app, "PUT", path, `{"is_advertised":true}`, 200)
	}
	planHTTP(t, app, "PUT", path, `{"prices":[],"is_advertised":true}`, 400)
	check(true)
	planHTTP(t, app, "DELETE", path, "", 200)
	check(false)
	var all []planResponse
	if err := json.Unmarshal(planHTTP(t, app, "GET", "/plans", "", 200), &all); err != nil {
		t.Fatal(err)
	}
	for _, item := range all {
		if item.ID == customID && (item.IsAdvertised || item.IsActive || !item.Advertise) {
			t.Fatalf("admin list: %+v", item)
		}
	}
}
