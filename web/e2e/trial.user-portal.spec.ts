import { test, expect, type Page } from "@playwright/test";
import type { TrialSubscription } from "../src/api/trialSubscription";
import { mockSession } from "./subscription-domain-fixtures";

async function trialFixture(page: Page, subscribed: boolean, eligible = true,
  subscriptionFields: Partial<TrialSubscription> = {}) {
  await mockSession(page, "user");
  await page.route("**/src/api/**", (route) => route.continue());
  const writes: string[] = [];
  let canceled = false;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (method !== "GET") writes.push(path);
    if (path === "/api/v1/user/promotions/TRIAL7") {
      return route.fulfill({ json: {
        type: "trial", code: "TRIAL7", is_valid: eligible, is_eligible: eligible, trial_days: 7, requires_card: true,
        plan: { id: "trial-plan", name: "Trial plan", duration_days: 30, price_cents: 1500,
          traffic_limit: null, speed_limit: null, max_devices: 1, features: [],
          prices: [{ duration_days: 30, price_cents: 1500 }] },
      } });
    }
    if (method === "POST" && path === "/api/v1/user/orders") {
      expect(route.request().postDataJSON()).toEqual({
        plan_id: "trial-plan", duration_days: 30, promotion_code: "TRIAL7",
      });
      return route.fulfill({ status: 201, json: {
        id: "order-1", plan_id: "trial-plan", plan_name: "Trial plan", duration_days: 30,
        price_cents: 0, discount_cents: 1500, promotion_code: "TRIAL7", status: "pending",
        created_at: "2030-01-01T00:00:00Z", expires_at: "2030-01-02T00:00:00Z",
      } });
    }
    if (method === "POST" && path === "/api/v1/user/orders/order-1/checkout") {
      expect(route.request().postDataJSON()).toEqual({ provider: "stripe" });
      return route.fulfill({ json: { provider: "stripe", mode: "redirect", redirect_url: "https://checkout.stripe.test/trial" } });
    }
    if (method === "DELETE" && path === "/api/v1/user/orders/order-1/promotion") {
      return route.fulfill({ json: {
        id: "order-1", plan_id: "trial-plan", plan_name: "Trial plan", duration_days: 30,
        price_cents: 1500, discount_cents: 0, status: "pending",
        created_at: "2030-01-01T00:00:00Z", expires_at: "2030-01-02T00:00:00Z",
      } });
    }
    if (path.endsWith("/free-trials/subscription/cancel")) canceled = true;
    if (path.includes("/free-trials/subscription")) {
      return route.fulfill({ json: subscribed ? {
        id: "trial", code: "TRIAL7", plan_name: "Trial plan", status: "trialing",
        price_cents: 1500, currency: "usd", duration_days: 30,
        trial_ends_at: "2030-01-08T12:00:00Z", current_period_end: "2030-01-08T12:00:00Z",
        cancel_at_period_end: canceled,
        payment_method: { brand: "visa", last4: "4242", exp_month: 12, exp_year: 2030, funding: "credit" },
        ...subscriptionFields,
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
      summary: { status: "active", traffic_used: 0, traffic_limit: null, plan_name: null },
      nodes: [], announcements: [], "subscription-domains": [],
    };
    return route.fulfill({ json: responses[path.split("/").at(-1) ?? ""] ?? {} });
  });
  return writes;
}

test("trial landing uses card checkout without creating a coupon order", async ({ page }) => {
  const writes = await trialFixture(page, false);
  // The payment flow opens Stripe in a new tab, so the route must live on the
  // browsing context; a page.route does not cover pages opened by window.open.
  await page.context().route("https://checkout.stripe.test/trial", (route) => route.fulfill({ body: "<html><body>Test payment gateway</body></html>", contentType: "text/html" }));
  await page.goto("/portal/plan?promo=TRIAL7&resume=1");
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "Trial plan" })).toBeVisible();
  await expect(dialog.getByText("$0.00").first()).toBeVisible();
  const popupPromise = page.waitForEvent("popup");
  await dialog.getByRole("button", { name: "Pay $0.00", exact: true }).click();
  const popup = await popupPromise;
  await expect(popup).toHaveURL("https://checkout.stripe.test/trial");
  expect(writes).toEqual(["/api/v1/user/orders", "/api/v1/user/orders/order-1/checkout"]);
});

test("removing an applied promotion restores the price and code field", async ({ page }) => {
  const writes = await trialFixture(page, false);
  await page.goto("/portal/plan?promo=TRIAL7&resume=1");
  const dialog = page.getByRole("dialog");
  const remove = dialog.getByRole("button", { name: "Remove promotion", exact: true });
  await expect(remove).toBeVisible();
  await expect(dialog.getByRole("textbox", { name: "Promotion code" })).toHaveCount(0);
  await expect(dialog.locator(".plan-info__receipt-total")).toContainText("$0.00");
  const removed = page.waitForResponse(response => response.request().method() === "DELETE"
    && new URL(response.url()).pathname === "/api/v1/user/orders/order-1/promotion");
  await remove.click();
  expect((await removed).ok()).toBe(true);
  await expect(remove).toHaveCount(0);
  await expect(dialog.getByRole("textbox", { name: "Promotion code" })).toBeVisible();
  await expect(dialog.locator(".plan-info__receipt-total")).toContainText("$15.00");
  expect(writes).toEqual(["/api/v1/user/orders", "/api/v1/user/orders/order-1/promotion"]);
});

test("opening a promotion link while signed in shows an eligible offer again", async ({ page }) => {
  const writes = await trialFixture(page, false);
  await page.goto("/portal/dashboard");
  await expect(page).toHaveURL(/\/portal\/dashboard$/);
  await page.evaluate(() => {
    localStorage.setItem("proxima_offer_seen:fixture@example.test:TRIAL7", "1");
    window.history.pushState({}, "", "/p/TRIAL7");
    window.dispatchEvent(new PopStateEvent("popstate"));
  });
  const offer = page.getByRole("dialog");
  await expect(offer.getByRole("heading", { name: "Trial plan" })).toBeVisible();
  await expect(page).toHaveURL(/\/portal\/dashboard$/);
  expect(writes).toEqual([]);
  await offer.getByRole("button", { name: "Not now", exact: true }).click();
  await expect(offer).toBeHidden();
});

test("an ineligible trial still shows its offer once with one lookup and no order", async ({ page }) => {
  const writes = await trialFixture(page, false, false);
  const lookups: string[] = [];
  page.on("request", request => {
    const path = new URL(request.url()).pathname;
    if (path.includes("/promotions/TRIAL7") || path === "/api/v1/promo/TRIAL7") lookups.push(path);
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/p/TRIAL7");
  const offer = page.getByRole("dialog");
  await expect(offer.getByRole("heading", { name: "Trial plan" })).toBeVisible();
  await expect(offer.getByText(/eligibility requirements/)).toBeVisible();
  await expect(page).toHaveURL(/\/portal\/dashboard$/);
  expect(lookups).toEqual(["/api/v1/user/promotions/TRIAL7"]);
  expect(writes).toEqual([]);
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations()
      .filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity)
      .map(animation => animation.finished));
  });
  await page.screenshot({ path: ".omo/evidence/promo-cloudscape/offer-desktop.png" });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: ".omo/evidence/promo-cloudscape/offer-mobile.png" });
  await offer.getByRole("button", { name: "Start free trial", exact: true }).click();
  const notice = page.getByRole("dialog");
  await expect(notice.getByText(/eligibility requirements/)).toBeVisible();
  await expect(notice.getByRole("heading", { name: "Promotion notice" })).toBeVisible();
  expect(writes).toEqual([]);
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations()
      .filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity)
      .map(animation => animation.finished));
  });
  await page.screenshot({ path: ".omo/evidence/promo-cloudscape/notice-mobile.png" });
});

test("trial cancellation requires confirmation and preserves the access end", async ({ page }) => {
  const writes = await trialFixture(page, true);
  await page.goto("/portal/plan");
  const manage = page.getByRole("button", { name: "Manage billing", exact: true });
  await expect(manage).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Free trial · Automatic billing", exact: true })).toHaveCount(0);
  await expect(page.locator(".trial-summary").getByText("Visa •••• 4242", { exact: true })).toBeVisible();
  await manage.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("Trial plan")).toBeVisible();
  expect(writes).toEqual([]);
  await dialog.getByRole("button", { name: "Cancel automatic billing", exact: true }).click();
  const confirmDialog = page.getByRole("dialog").filter({
    hasText: "Only future billing stops. Your current trial or paid access remains available.",
  });
  await expect(confirmDialog).toBeVisible();
  expect(writes).toEqual([]);
  await confirmDialog.getByRole("button", { name: "Cancel automatic billing", exact: true }).click();
  await expect(confirmDialog).toBeHidden();
  await expect(page.getByText("Automatic billing is canceled. Your granted access remains available.", { exact: true })).toBeVisible();
  expect(writes).toEqual(["/api/v1/user/free-trials/subscription/cancel"]);
  await expect(page.getByText("Trial plan").first()).toBeVisible();
});

test("canceled billing keeps Stripe card details and one management action", async ({ page }) => {
  await trialFixture(page, true, true, { status: "canceled" });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/portal/plan");
  await expect(page.locator(".trial-summary").getByText("Visa •••• 4242", { exact: true })).toBeVisible();
  await expect(page.locator(".trial-summary").getByText("No charge is scheduled.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Manage billing", exact: true })).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Free trial · Automatic billing", exact: true })).toHaveCount(0);
  await page.screenshot({ path: "/tmp/proxima-billing-card-desktop.png" });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator(".trial-summary").getByText("Visa •••• 4242", { exact: true })).toBeVisible();
  await page.screenshot({ path: "/tmp/proxima-billing-card-mobile.png" });
});

test("Stripe card lookup failure is distinct from no saved card", async ({ page }) => {
  await trialFixture(page, true, true, { payment_method: null, payment_method_unavailable: true });
  await page.goto("/portal/plan");
  await expect(page.locator(".trial-summary").getByText("Card details could not be loaded from Stripe.", { exact: true })).toBeVisible();
  await expect(page.getByText("No card is saved.", { exact: true })).toHaveCount(0);
});

test("billing management opens Stripe Customer Portal and refreshes on return", async ({ page }) => {
  await trialFixture(page, true);
  await page.route("**/api/v1/user/billing/portal", route => {
    expect(route.request().method()).toBe("POST");
    expect(route.request().postDataJSON()).toEqual({});
    return route.fulfill({ json: { url: "https://billing.stripe.test/session" } });
  });
  await page.context().route("https://billing.stripe.test/session", route =>
    route.fulfill({ contentType: "text/html", body: "<html><body>Stripe Customer Portal</body></html>" }));
  await page.goto("/portal/plan");
  await expect(page.locator(".trial-summary").getByText("Visa •••• 4242", { exact: true })).toBeVisible();
  const popupOpened = page.waitForEvent("popup");
  await page.getByRole("button", { name: "Billing portal", exact: true }).click();
  const popup = await popupOpened;
  await expect(popup).toHaveURL("https://billing.stripe.test/session");
  const refreshed = page.waitForResponse(response =>
    new URL(response.url()).pathname === "/api/v1/user/free-trials/subscription");
  await popup.close();
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  expect((await refreshed).ok()).toBe(true);
  await expect(page.locator(".trial-summary").getByText("Visa •••• 4242", { exact: true })).toBeVisible();
});
