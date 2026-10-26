package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
)

func promotionKindsApp(t *testing.T) (*fiber.App, *pgxpool.Pool) {
	t.Helper()
	pool := testOrderDB(t)
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	trials, affiliates := NewAdminFreeTrialHandler(pool), NewAdminAffiliateHandler(pool)
	app.Get("/free-trials", trials.List)
	app.Post("/free-trials", trials.Create)
	app.Put("/free-trials/:id", trials.Update)
	app.Delete("/free-trials/:id", trials.Delete)
	app.Get("/affiliates", affiliates.List)
	app.Post("/affiliates", affiliates.Create)
	app.Put("/affiliates/:id", affiliates.Update)
	app.Delete("/affiliates/:id", affiliates.Delete)
	app.Get("/affiliates/:id/commissions", affiliates.Commissions)
	app.Put("/affiliates/:id/commissions/:commissionId/paid", affiliates.MarkPaid)
	app.Get("/promo/:code", NewPromoLandingHandler(pool).Get)
	return app, pool
}

func promotionKindsRequest(t *testing.T, app *fiber.App, method, path, body string, status int, result any) {
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
		t.Fatalf("%s %s: status %d, want %d; body %s", method, path, res.StatusCode, status, data)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPromotionKindsCreateDefaults(t *testing.T) {
	// Given: migrated trial routes and a unique campaign code.
	app, pool := promotionKindsApp(t)
	code := uuid.NewString()
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM free_trial_promotions WHERE code=$1`, code) })
	// When: optional defaults are omitted.
	var result freeTrialResponse
	promotionKindsRequest(t, app, "POST", "/free-trials",
		`{"code":"`+code+`","duration_days":30,"trial_days":7}`, 201, &result)
	// Then: defaults and nullable plan are preserved in the response and DB.
	if !result.IsActive || !result.RequiresCard || result.MaxRedemptionsPerUser != 1 || result.PlanID != nil {
		t.Fatalf("incorrect defaults: %+v", result)
	}
}

func TestPromotionKindsRejectInvalidInput(t *testing.T) {
	for _, tc := range []struct{ name, path, body string }{
		{"trial duration", "/free-trials", `{"code":"X","duration_days":0,"trial_days":7}`},
		{"trial plan", "/free-trials", `{"code":"X","duration_days":30,"trial_days":7,"plan_id":"bad"}`},
		{"trial cap", "/free-trials", `{"code":"X","duration_days":30,"trial_days":7,"max_redemptions":-1}`},
		{"affiliate rate", "/affiliates", `{"name":"X","code":"X","commission_rate":101,"discount_type":"percent","discount_value":10}`},
		{"affiliate percent", "/affiliates", `{"name":"X","code":"X","commission_rate":10,"discount_type":"percent","discount_value":101}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: routes with no DB, proving validation precedes persistence.
			app := fiber.New()
			app.Post("/free-trials", NewAdminFreeTrialHandler(nil).Create)
			app.Post("/affiliates", NewAdminAffiliateHandler(nil).Create)
			// When/Then: invalid input is rejected without touching the DB.
			promotionKindsRequest(t, app, "POST", tc.path, tc.body, 400, nil)
		})
	}
}

func TestPromotionKindsPublicLookup(t *testing.T) {
	app, pool := promotionKindsApp(t)
	ctx := context.Background()
	for _, kind := range []string{"coupon", "trial", "affiliate", "inactive", "expired", "exhausted", "missing"} {
		t.Run(kind, func(t *testing.T) {
			// Given: a unique promotion in one namespace or an unavailable code.
			code := uuid.NewString()
			var err error
			switch kind {
			case "trial":
				_, err = pool.Exec(ctx, `INSERT INTO free_trial_promotions(code,duration_days,trial_days) VALUES($1,30,7)`, code)
				t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM free_trial_promotions WHERE code=$1`, code) })
			case "affiliate":
				_, err = pool.Exec(ctx, `INSERT INTO affiliate_partners(name,code,commission_rate,discount_type,discount_value) VALUES('Partner',$1,10,'fixed',150)`, code)
				t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM affiliate_partners WHERE code=$1`, code) })
			case "coupon", "inactive", "expired", "exhausted":
				_, err = pool.Exec(ctx, `INSERT INTO promotion_codes(code,discount_type,discount_value,valid_from,valid_until,is_active,max_redemptions,redeemed_count)
					VALUES($1,'percent',20,NOW()-INTERVAL '2 days',
					CASE WHEN $2='expired' THEN NOW()-INTERVAL '1 day' ELSE NOW()+INTERVAL '1 day' END,
					$2<>'inactive',1,CASE WHEN $2='exhausted' THEN 1 ELSE 0 END)`, code, kind)
				t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM promotion_codes WHERE code=$1`, code) })
			}
			if err != nil {
				t.Fatal(err)
			}
			// When: the public endpoint is requested without authentication.
			status := 200
			if kind == "missing" {
				status = 404
			}
			var result promoLandingResponse
			promotionKindsRequest(t, app, "GET", "/promo/"+code, "", status, &result)
			// Then: validity reflects availability and trial metadata is exposed.
			valid := kind == "coupon" || kind == "trial" || kind == "affiliate"
			if result.Code != code || result.IsValid != valid {
				t.Fatalf("unexpected public result: %+v", result)
			}
			if kind == "trial" && (result.Type != "trial" || result.TrialDays == nil || *result.TrialDays != 7) {
				t.Fatalf("missing trial metadata: %+v", result)
			}
		})
	}
}

func TestPromotionKindsCommissionSummaryAndPaid(t *testing.T) {
	app, pool := promotionKindsApp(t)
	ctx := context.Background()
	partnerID, commissionID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO affiliate_partners(id,name,code,commission_rate,discount_type,discount_value)
		VALUES($1,'Partner',$2,10,'percent',5)`, partnerID, partnerID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM affiliate_commissions WHERE partner_id=$1`, partnerID)
		_, _ = pool.Exec(ctx, `DELETE FROM affiliate_partners WHERE id=$1`, partnerID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO affiliate_commissions(id,partner_id,order_amount,commission_amount)
		VALUES($1,$2,1000,100)`, commissionID, partnerID); err != nil {
		t.Fatal(err)
	}
	t.Run("mark paid", func(t *testing.T) {
		// Given: a pending commission. When: mark it paid through its own partner.
		promotionKindsRequest(t, app, "PUT", "/affiliates/"+partnerID+"/commissions/"+commissionID+"/paid", "", 200, nil)
		// Then: the database stores the paid state.
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM affiliate_commissions WHERE id=$1`, commissionID).Scan(&status); err != nil || status != "paid" {
			t.Fatalf("status=%s, err=%v", status, err)
		}
	})
	t.Run("summary", func(t *testing.T) {
		// Given: the paid commission. When: request the summary.
		var result struct {
			Paid int64 `json:"commission_paid"`
			Owed int64 `json:"commission_owed"`
		}
		promotionKindsRequest(t, app, "GET", "/affiliates/"+partnerID+"/commissions", "", 200, &result)
		// Then: the paid and owed totals match the ledger.
		if result.Paid != 100 || result.Owed != 0 {
			t.Fatalf("unexpected summary: %+v", result)
		}
	})
	t.Run("wrong partner", func(t *testing.T) {
		// Given: a commission belonging to another partner. When/Then: no cross-partner update is possible.
		promotionKindsRequest(t, app, "PUT", "/affiliates/"+uuid.NewString()+"/commissions/"+commissionID+"/paid", "", 404, nil)
	})
}
