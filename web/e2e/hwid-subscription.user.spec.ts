import { test, expect } from "@playwright/test";
import { mockSession } from "./subscription-domain-fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

test("one account subscription URL stays primary while legacy device links remain fallback", async ({ page }) => {
  await mockSession(page, "user", "ko");
  await page.route("**/src/api/**", route => route.continue());
  await page.route("**/api/v1/user/profile", route => route.fulfill({ json: {
    id: "user", email: "example@test.invalid", name: "Example", status: "active", plan_name: "Basic",
    sub_token: "account-token", traffic_used: 0, language: "ko",
  } }));
  await page.route("**/api/v1/user/devices", route => route.fulfill({ json: [
    { id: "legacy-one", xray_uuid: "uuid-one", name: "예전 앱", subscription_url: "/sub/account-token/legacy-one", created_at: "2026-01-01T00:00:00Z" },
  ] }));
  await page.route("**/api/v1/user/summary", route => route.fulfill({ json: { max_devices: 10 } }));
  await page.route("**/api/v1/user/subscription-domains", route => route.fulfill({ json: [] }));
  await page.goto("/portal/devices");
  await expect(page.locator("code").filter({ hasText: "/sub/account-token" }).first()).toBeVisible();
  await expect(page.locator("code").filter({ hasText: "/sub/account-token/legacy-one" }).first()).toBeVisible();
});
