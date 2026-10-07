import { test, expect } from "@playwright/test";
import { mockSession } from "./subscription-domain-fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

test("subscription page shows only the account URL, without device links or format queries", async ({ page }) => {
  await mockSession(page, "user", "ko");
  await page.route("**/src/api/**", route => route.continue());
  await page.route("**/api/v1/user/profile", route => route.fulfill({ json: {
    id: "user", email: "example@test.invalid", name: "Example", status: "active", plan_name: "Basic",
    sub_token: "account-token", traffic_used: 0, language: "ko",
  } }));
  // A legacy device still exists server-side; the page must not surface it.
  await page.route("**/api/v1/user/devices", route => route.fulfill({ json: [
    { id: "legacy-one", xray_uuid: "uuid-one", name: "예전 앱", subscription_url: "/sub/account-token/legacy-one", created_at: "2026-01-01T00:00:00Z" },
  ] }));
  await page.route("**/api/v1/user/subscription-domains", route => route.fulfill({ json: [] }));
  await page.goto("/portal/devices");

  const accountUrl = new URL("/sub/account-token", page.url()).toString();
  await expect(page.locator("code").filter({ hasText: accountUrl }).first()).toHaveText(accountUrl);
  await expect(page.getByText("/sub/account-token/legacy-one")).toHaveCount(0);
  await expect(page.getByText("uuid-one")).toHaveCount(0);
  await expect(page.getByText("format=")).toHaveCount(0);
  await expect(page.getByText("예전 앱")).toHaveCount(0);
});
