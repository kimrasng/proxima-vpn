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
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminPlanRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name, fields, want string
	}{
		{"blank name", `"name":"  "`, "name is required"},
		{"null character name", `"name":"bad\u0000name"`, "name is required"},
		{"zero duration", `"duration_days":0`, "duration_days must be"},
		{"negative devices", `"max_devices":-1`, "max_devices must be"},
		{"overflow duration", `"duration_days":2147483648`, "duration_days must be"},
		{"negative traffic", `"traffic_limit":-1`, "traffic_limit must not be negative"},
		{"zero concurrency", `"max_concurrent":0`, "max_concurrent must be"},
		{"negative speed", `"speed_limit":-1`, "speed_limit must be"},
		{"overflow speed", `"speed_limit":2147483648`, "speed_limit must be"},
		{"blank group", `"node_group_id":" "`, "node_group_id is required"},
		{"invalid group", `"node_group_id":"bad"`, "node_group_id must be a UUID"},
		{"zero price duration", `"prices":[{"duration_days":0,"price_cents":100}]`, "price duration_days must be"},
		{"negative price", `"prices":[{"duration_days":30,"price_cents":-1}]`, "price_cents must not be negative"},
		{"duplicate price duration", `"prices":[{"duration_days":30,"price_cents":100},{"duration_days":30,"price_cents":200}]`, "price duration_days must be unique"},
		{"missing feature text", `"features":[{"included":true}]`, "feature text is required"},
		{"blank feature text", `"features":[{"text":{"en":" "}}]`, "feature text is required"},
		{"blank language", `"features":[{"text":{"":"Hello"}}]`, "feature language is required"},
		{"null character feature", `"features":[{"text":{"en":"bad\u0000text"}}]`, "must not contain null characters"},
		{"wrong limit type", `"speed_limit":"fast"`, "invalid request body"},
		{"wrong feature type", `"features":[{"text":{"en":5}}]`, "invalid request body"},
	}
	for _, method := range []string{"POST", "PUT"} {
		for _, tc := range cases {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				// A nil DB proves rejection happens before opening a transaction.
				h := NewAdminPlanHandler(nil)
				app := fiber.New()
				app.Post("/plans", h.Create)
				app.Put("/plans", h.Update)
				body := "{" + tc.fields + "}"
				if method == "POST" {
					var fields map[string]json.RawMessage
					_ = json.Unmarshal([]byte(`{"name":"Valid","duration_days":30,"max_devices":3,"node_group_id":"00000000-0000-0000-0000-000000000001"}`), &fields)
					_ = json.Unmarshal([]byte(body), &fields)
					encoded, _ := json.Marshal(fields)
					body = string(encoded)
				}
				req := httptest.NewRequest(method, "/plans", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				res, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = res.Body.Close() }()
				data, _ := io.ReadAll(res.Body)
				if res.StatusCode != 400 || !strings.Contains(string(data), tc.want) {
					t.Fatalf("status=%d body=%s; want 400 containing %q", res.StatusCode, data, tc.want)
				}
			})
		}
	}
}

func TestAdminPlanUpdateRejectsNullRequiredFieldsAndEmptyPatch(t *testing.T) {
	for _, body := range []string{`{}`, `{"name":null}`, `{"duration_days":null}`, `{"max_devices":null}`, `{"node_group_id":null}`, `{"is_active":null}`, `{"prices":null,"features":null}`} {
		t.Run(body, func(t *testing.T) {
			app := fiber.New()
			app.Put("/plans", NewAdminPlanHandler(nil).Update)
			req := httptest.NewRequest("PUT", "/plans", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != 400 {
				t.Fatalf("status=%d; want 400", res.StatusCode)
			}
		})
	}
}

func TestAdminPlanLimitJSONPresence(t *testing.T) {
	for _, tc := range []struct {
		body              string
		present, hasValue bool
	}{
		{`{}`, false, false},
		{`{"traffic_limit":null,"speed_limit":null,"max_concurrent":null}`, true, false},
		{`{"traffic_limit":100,"speed_limit":20,"max_concurrent":2}`, true, true},
	} {
		var req updatePlanRequest
		if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
			t.Fatal(err)
		}
		if req.TrafficLimit.Present != tc.present || req.SpeedLimit.Present != tc.present || req.MaxConcurrent.Present != tc.present {
			t.Fatalf("incorrect presence for %s: %+v", tc.body, req)
		}
		if (req.TrafficLimit.Value != nil) != tc.hasValue || (req.SpeedLimit.Value != nil) != tc.hasValue || (req.MaxConcurrent.Value != nil) != tc.hasValue {
			t.Fatalf("incorrect value for %s: %+v", tc.body, req)
		}
	}
}

func TestAdminPlanValidInput(t *testing.T) {
	var req createPlanRequest
	if err := json.Unmarshal([]byte(`{"name":"Valid","duration_days":30,"max_devices":4,"max_concurrent":2,"traffic_limit":0,"speed_limit":0,"node_group_id":"00000000-0000-0000-0000-000000000001","prices":[{"duration_days":30,"price_cents":0}],"features":[{"included":false,"text":{"en":"Feature","ko":""}}]}`), &req); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanInput(&req.Name, req.TrafficLimit, &req.DurationDays, &req.MaxDevices, req.MaxConcurrent, req.SpeedLimit, &req.NodeGroupID, req.Prices, req.Features); err != nil {
		t.Fatal(err)
	}
}

func TestAdminPlanUpdateClearsLimitsAndPreservesOmittedFields(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var groupID, planID string
	if err := pool.QueryRow(ctx, `INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, fmt.Sprintf("plan-patch-%d", os.Getpid())).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id=$1`, groupID) })
	if err := pool.QueryRow(ctx, `INSERT INTO plans (name, duration_days, max_devices, traffic_limit, speed_limit, max_concurrent, node_group_id) VALUES ('Original',30,4,1000,50,2,$1) RETURNING id::text`, groupID).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, planID) })
	app := fiber.New()
	app.Put("/plans/:id", NewAdminPlanHandler(pool).Update)
	for _, body := range []string{`{"name":"Renamed"}`, `{"traffic_limit":null,"speed_limit":null,"max_concurrent":null}`} {
		req := httptest.NewRequest("PUT", "/plans/"+planID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var plan planResponse
		err = json.NewDecoder(res.Body).Decode(&plan)
		_ = res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("status=%d decode=%v", res.StatusCode, err)
		}
		if plan.Name != "Renamed" || plan.DurationDays != 30 || plan.MaxDevices != 4 || plan.NodeGroupID != groupID {
			t.Fatalf("omitted scalar changed: %+v", plan)
		}
		if strings.Contains(body, "null") {
			if plan.TrafficLimit != nil || plan.SpeedLimit != nil || plan.MaxConcurrent != nil {
				t.Fatalf("limits not cleared: %+v", plan)
			}
		} else if plan.TrafficLimit == nil || *plan.TrafficLimit != 1000 || plan.SpeedLimit == nil || *plan.SpeedLimit != 50 || plan.MaxConcurrent == nil || *plan.MaxConcurrent != 2 {
			t.Fatalf("omitted limits changed: %+v", plan)
		}
	}
}
