import { test, expect, type Page } from "@playwright/test";
import { mockSession } from "./subscription-domain-fixtures";

async function trialFixture(page: Page, subscribed: boolean) {
  await mockSession(page, "user");
  await page.route("**/src/api/**", (route) => route.continue());
  const writes: string[] = [];
  let canceled = false;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (method !== "GET") writes.push(path);
    if (path === "/api/v1/promo/TRIAL7") {
      return route.fulfill({ json: { type: "trial", code: "TRIAL7", is_valid: true, trial_days: 7 } });
    }
    if (path.endsWith("/free-trials/redeem")) {
      expect(route.request().postDataJSON()).toEqual({ code: "TRIAL7" });
      return route.fulfill({ status: 201, json: { mode: "redirect", redirect_url: "https://checkout.stripe.test/trial" } });
    }
    if (path.endsWith("/free-trials/subscription/cancel")) canceled = true;
    if (path.includes("/free-trials/subscription")) {
      return route.fulfill({ json: subscribed ? {
        id: "trial", code: "TRIAL7", plan_name: "Trial plan", status: "trialing",
        price_cents: 1500, currency: "usd", duration_days: 30,
        trial_ends_at: "2030-01-08T12:00:00Z", current_period_end: "2030-01-08T12:00:00Z",
        cancel_at_period_end: canceled,
      } : null });
    }
    const responses: Record<string, unknown> = {
      profile: { id: "trial-user", name: "Trial user", status: "active", language: "en",
        email: "fixture@example.test", plan_id: subscribed ? "trial-plan" : null,
        plan_name: subscribed ? "Trial plan" : null, traffic_used: 0, traffic_limit: null,
        plan_expires_at: subscribed ? "2030-01-08T12:00:00Z" : null, sub_token: "fixture" },
      plans: [{ id: "trial-plan", name: "Trial plan", duration_days: 30, max_devices: 1,
        purchasable: true, prices: [{ duration_days: 30, price_cents: 1500 }], features: [] }],
      orders: [], "payment-providers": [{ name: "stripe", mode: "redirect" }],
      "reset-tickets": [], history: [],
    };
    return route.fulfill({ json: responses[path.split("/").at(-1) ?? ""] ?? {} });
  });
  return writes;
}

test("trial landing uses card checkout without creating a coupon order", async ({ page }) => {
  const writes = await trialFixture(page, false);
  await page.route("https://checkout.stripe.test/trial", (route) => route.fulfill({ body: "<html><body>Test payment gateway</body></html>", contentType: "text/html" }));
  await page.goto("/portal/plan?promo=TRIAL7");
  await expect(page.getByRole("button", { name: "Start free trial", exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(writes).toEqual([]);
  await page.getByRole("button", { name: "Start free trial", exact: true }).click();
  await expect(page).toHaveURL("https://checkout.stripe.test/trial");
  expect(writes).toEqual(["/api/v1/user/free-trials/redeem"]);
});

test("trial cancellation requires confirmation and preserves the access end", async ({ page }) => {
  const writes = await trialFixture(page, true);
  await page.goto("/portal/plan");
  await page.getByRole("button", { name: "Cancel automatic billing", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  expect(writes).toEqual([]);
  await dialog.getByRole("button", { name: "Cancel automatic billing", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText("Automatic billing is canceled. Your granted access remains available.", { exact: true })).toBeVisible();
  expect(writes).toEqual(["/api/v1/user/free-trials/subscription/cancel"]);
  await expect(page.getByRole("heading", { name: "Trial plan", exact: true }).first()).toBeVisible();
});
