import { test, expect, type Page } from "@playwright/test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QRCodeSVG } from "qrcode.react";
import type {
  Announcement, AvailableNode, CreateOrderRequest,
  OrderResponse, UserPlanItem, UserProfile, UserSummary,
} from "../src/api/types";
import en from "../src/i18n/locales/en.json" with { type: "json" };
import ko from "../src/i18n/locales/ko.json" with { type: "json" };
import { mockSession, publicDomains } from "./subscription-domain-fixtures";

const NOW = "2032-06-01T12:00:00Z";
const EXPIRES = "2032-06-11T12:00:00Z";
const GB = 1024 ** 3;
const overview: UserSummary = {
  plan_name: "Everyday Plus", status: "active", traffic_used: 25 * GB,
  traffic_limit: 100 * GB, plan_expires_at: EXPIRES,
  devices: 2, max_devices: 4, online: 1, online_ips: 2, max_concurrent: 5,
};
const profile: UserProfile = {
  id: "dashboard-user", name: "Browser fixture", email: "fixture@example.test",
  status: "active", language: "en", plan_name: "Everyday Plus",
  traffic_used: 25 * GB, traffic_limit: 100 * GB, plan_expires_at: EXPIRES,
  // Reserved characters prove the token is percent-encoded once into the path.
  sub_token: "account/token+secret",
};
const ACCOUNT_PATH = "/sub/account%2Ftoken%2Bsecret";
const accountUrl = (host: string) => `https://${host}${ACCOUNT_PATH}`;
const nodes: AvailableNode[] = [
  { name: "Seoul edge", country: "KR", region: "Seoul", status: "online" },
  { name: "Tokyo edge", country: "JP", region: "Tokyo", status: "offline" },
];
const plans: UserPlanItem[] = [
  {
    id: "plus", name: "Everyday Plus", traffic_limit: 100 * GB,
    duration_days: 30, max_devices: 4, speed_limit: 100, purchasable: true,
    prices: [{ duration_days: 30, price_cents: 1200 }, { duration_days: 90, price_cents: 3000 }],
    features: [{ text: "All available regions", included: true }],
  },
  {
    id: "no-price", name: "Unpriced preview", duration_days: 30,
    max_devices: 2, purchasable: true, prices: [], features: [],
  },
  {
    id: "admin-only", name: "Managed enterprise", duration_days: 30,
    max_devices: 10, purchasable: false,
    prices: [{ duration_days: 30, price_cents: 9900 }], features: [],
  },
];
const pendingOrder: OrderResponse = {
  id: "fixture-order", plan_id: "plus", plan_name: "Everyday Plus",
  duration_days: 90, price_cents: 3000, status: "pending", created_at: NOW,
  expires_at: "2032-06-02T12:00:00Z",
};
const bulletin: Announcement = {
  id: "bulletin", title: "Service update", content: "Your servers are ready.",
  is_active: true, created_at: NOW,
};

// Every API request is intercepted by mockSession. This state models only the
// dashboard and plan contracts; the only writes below mutate browser fixtures.
async function mockDashboard(page: Page, language = "en") {
  await mockSession(page, "user", language);
  // mockSession's broad /api/ fallback must not intercept Vite source modules.
  await page.route("**/src/api/**", (route) => route.continue());
  await page.clock.setFixedTime(new Date(NOW));
  await page.addInitScript(() => {
    const copied: string[] = [];
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: async (text: string) => { copied.push(text); },
        readText: async () => copied.at(-1) ?? "",
      },
    });
  });
  const state = {
    summary: { ...overview }, profile: { ...profile, language },
    announcements: [{ ...bulletin }], nodes: nodes.map((node) => ({ ...node })),
    plans, orders: [] as OrderResponse[],
    failures: new Set<string>(), reads: new Map<string, number>(),
    orderPosts: [] as CreateOrderRequest[], unexpectedWrites: [] as string[],
  };
  await page.route("**/api/v1/user/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.split("/").at(-1) ?? "";
    if (request.method() === "GET") {
      state.reads.set(path, (state.reads.get(path) ?? 0) + 1);
      if (state.failures.has(path)) {
        await route.fulfill({ status: 503, json: { error: "fixture unavailable" } });
        return;
      }
      const responses: Record<string, unknown> = {
        summary: state.summary, profile: state.profile,
        "subscription-domains": publicDomains, nodes: state.nodes,
        announcements: state.announcements, plans: state.plans, orders: state.orders,
        "payment-providers": [{ name: "manual", mode: "manual" }],
      };
      await route.fulfill({ json: responses[path] ?? {} });
    } else if (request.method() === "POST" && path === "orders") {
      const body: CreateOrderRequest = request.postDataJSON();
      state.orderPosts.push(body);
      const order = { ...pendingOrder, plan_id: body.plan_id, duration_days: body.duration_days };
      state.orders = [order];
      await route.fulfill({ status: 201, json: order });
    } else {
      state.unexpectedWrites.push(`${request.method()} ${request.url()}`);
      await route.fulfill({ status: 405, json: { error: "unexpected fixture mutation" } });
    }
  });
  return state;
}

async function choose(page: Page, label: string, option: string) {
  // Cloudscape Select exposes its labelled trigger as a button, not a combobox.
  await page.getByRole("button", { name: new RegExp(label) }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

async function openOverview(page: Page, title = en.user.dashboard.title) {
  await page.goto("/portal/dashboard");
  await expect(page.getByRole("heading", { name: title, exact: true })).toBeVisible();
}

function accountUrlInput(page: Page, label = en.user.devices.accountUrl) {
  return page.getByRole("textbox", { name: label, exact: true });
}

function purchaseButton(page: Page, planName = "Everyday Plus") {
  return page.getByRole("button", { name: `${en.user.plan.order.placeOrder} ${planName}`, exact: true });
}

test.describe("mocked user dashboard and purchase journey", () => {
  test("overview shows monthly used-of-cap, days left and concurrent connections, without a device tile", async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 1000 });
    await page.emulateMedia({ colorScheme: "light" });
    const state = await mockDashboard(page);
    await openOverview(page);
    await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.monthlyTraffic, { exact: true })).toBeVisible();
    await expect(page.getByText("25.0 GB of 100.0 GB used", { exact: true })).toBeVisible();
    await expect(page.getByText("100.0 GB per month", { exact: true })).toBeVisible();
    await expect(page.getByText("75.0 GB remaining", { exact: true })).toBeVisible();
    await expect(page.getByText("10 days left", { exact: true })).toBeVisible();
    // The registered-devices tile (devices / max_devices) is gone.
    await expect(page.getByText("2 of 4", { exact: true })).toHaveCount(0);
    await page.getByRole("button", { name: en.user.dashboard.connections, exact: true }).click();
    await expect(page.getByText("2 of 5", { exact: true })).toBeVisible();
    await expect(page.getByText("1 of 2 online", { exact: true })).toBeVisible();
    await expect(accountUrlInput(page)).toHaveValue(accountUrl("blocked.example.test"));
    await expect(page.getByRole("button", { name: "Add Device" })).toHaveCount(0);
    await expect(page.getByRole("dialog")).toHaveCount(0);
    expect(state.reads.get("devices")).toBeUndefined();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await testInfo.attach("dashboard-desktop-light", { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
  });

  test("public host selection changes only the host of the account URL for copy and QR", async ({ page }) => {
    await mockDashboard(page);
    await openOverview(page);
    const url = accountUrlInput(page);
    await expect(url).toHaveValue(accountUrl("blocked.example.test"));
    await choose(page, en.user.dashboard.subscriptionDomain, "working.example.test");
    const selected = accountUrl("working.example.test");
    await expect(url).toHaveValue(selected);
    await page.getByRole("button", { name: en.user.dashboard.copyUrl, exact: true }).click();
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(selected);
    await page.getByRole("button", { name: en.user.devices.showQr, exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText(en.user.devices.qrTitle, { exact: true }).first()).toBeVisible();
    const qr = dialog.locator('svg[width="220"][height="220"]');
    await expect(qr).toBeVisible();
    // Compare the encoded matrix, not just the presence of an SVG: the QR must
    // encode exactly the selected account URL, including its encoded token.
    const expectedSvg = renderToStaticMarkup(createElement(QRCodeSVG, { value: selected, size: 220 }));
    const expectedPaths = [...expectedSvg.matchAll(/<path\b[^>]*\bd="([^"]+)"/g)].map((match) => match[1]);
    expect(expectedPaths.length).toBeGreaterThan(0);
    expect(await qr.locator("path").evaluateAll((paths) => paths.map((path) => path.getAttribute("d")))).toEqual(expectedPaths);
    await dialog.getByRole("button", { name: en.user.dashboard.copyUrl, exact: true }).click();
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(selected);
  });

  test("subscription details link opens the account subscription page", async ({ page }) => {
    await mockDashboard(page);
    await openOverview(page);
    await page.getByRole("button", { name: en.user.dashboard.subscriptionDetails, exact: true }).click();
    await expect(page).toHaveURL(/\/portal\/devices$/);
    await expect(page.getByRole("heading", { name: en.user.devices.title, exact: true })).toBeVisible();
  });

  test("no-plan overview offers purchase navigation and explains that connecting needs a plan", async ({ page }) => {
    const state = await mockDashboard(page);
    state.summary = { ...overview, plan_name: null, plan_expires_at: null, traffic_limit: null, devices: 0 };
    state.profile = { ...profile, plan_name: undefined, plan_expires_at: undefined };
    await openOverview(page);
    await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.noPlanDescription, { exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.connectNeedsPlan, { exact: true })).toBeVisible();
    await page.getByRole("button", { name: en.user.dashboard.browsePlans, exact: true }).first().click();
    await expect(page).toHaveURL(/\/portal\/plan$/);
    await expect(page.getByRole("heading", { name: en.user.plan.availablePlans, exact: true })).toBeVisible();
    await expect(purchaseButton(page)).toBeEnabled();
    expect(state.orderPosts).toEqual([]);
  });

  for (const scenario of ["expired", "suspended"] as const) {
    test(`${scenario} account sees why it cannot connect`, async ({ page }) => {
      const state = await mockDashboard(page);
      if (scenario === "expired") {
        // A stale active status must not override an already elapsed expiry.
        state.summary.plan_expires_at = "2032-05-31T12:00:00Z";
      } else {
        state.summary.status = "suspended";
      }
      await openOverview(page);
      if (scenario === "expired") {
        await expect(page.getByText(en.user.dashboard.expiredHint, { exact: true })).toBeVisible();
        await expect(page.getByText(en.user.dashboard.connectNeedsPlan, { exact: true })).toBeVisible();
      } else {
        await expect(page.getByText(en.user.dashboard.suspendedHint, { exact: true })).toHaveCount(2);
      }
      await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toHaveCount(0);
      await expect(page.getByRole("dialog")).toHaveCount(0);
      expect(state.unexpectedWrites).toEqual([]);
    });
  }

  test("zero usage and unlimited allowances remain an active plan, not a no-plan state", async ({ page }) => {
    const state = await mockDashboard(page);
    state.summary = {
      ...overview, traffic_used: 0, traffic_limit: 0, plan_expires_at: null,
      devices: 0, max_devices: 0, online: 0, online_ips: 0, max_concurrent: 0,
    };
    await openOverview(page);
    await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
    await expect(page.getByText("0 B", { exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.trafficUnlimited, { exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.noExpiry, { exact: true })).toBeVisible();
    await page.getByRole("button", { name: en.user.dashboard.connections, exact: true }).click();
    await expect(page.getByText("0 · No limit", { exact: true })).toBeVisible();
    await expect(accountUrlInput(page)).toHaveValue(accountUrl("blocked.example.test"));
    await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toHaveCount(0);
    await expect(page.getByText(en.user.dashboard.connectNeedsPlan, { exact: true })).toHaveCount(0);
    await expect(page.getByRole("progressbar")).toHaveCount(0);
  });

  test("independent API failures preserve the plan and healthy sections; retries recover only their resource", async ({ page }) => {
    const state = await mockDashboard(page);
    for (const path of ["profile", "nodes", "announcements", "subscription-domains"]) state.failures.add(path);
    await openOverview(page);
    await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
    for (const message of [en.user.devices.loadError, en.user.nodes.loadError, en.user.announcements.loadError]) {
      await expect(page.getByText(message, { exact: true })).toBeVisible();
    }
    await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toHaveCount(0);
    const nodeReads = state.reads.get("nodes");
    const announcementReads = state.reads.get("announcements");
    state.failures.delete("profile");
    await page.getByRole("button", { name: en.user.dashboard.retry, exact: true }).first().click();
    // Without public domains the account URL falls back to the panel origin.
    await expect(accountUrlInput(page)).toHaveValue(new URL(ACCOUNT_PATH, page.url()).toString());
    await expect(page.getByText(en.user.dashboard.domainFallback, { exact: true })).toBeVisible();
    expect(state.reads.get("nodes")).toBe(nodeReads);
    expect(state.reads.get("announcements")).toBe(announcementReads);
    state.failures.delete("nodes");
    await page.getByRole("button", { name: en.user.dashboard.retry, exact: true }).first().click();
    await expect(page.getByText("Seoul edge", { exact: true })).toBeVisible();
    await expect(page.getByText(en.user.announcements.loadError, { exact: true })).toBeVisible();
    state.failures.delete("announcements");
    await page.getByRole("button", { name: en.user.dashboard.retry, exact: true }).click();
    await expect(page.getByRole("button", { name: bulletin.title, exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: en.user.dashboard.retry, exact: true })).toHaveCount(0);

    state.failures.delete("subscription-domains");
    state.failures.add("summary");
    await page.getByRole("button", { name: en.user.dashboard.refresh, exact: true }).click();
    await expect(page.getByText(en.user.dashboard.loadError, { exact: true })).toBeVisible();
    await expect(page.getByText("Seoul edge", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: bulletin.title, exact: true })).toBeVisible();
    await expect(accountUrlInput(page)).toHaveValue(accountUrl("blocked.example.test"));
    await expect(page.getByText(en.user.dashboard.domainFallback, { exact: true })).toHaveCount(0);
    await expect(page.getByText(en.user.dashboard.connectNeedsPlan, { exact: true })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toHaveCount(0);
    state.failures.delete("summary");
    await page.getByRole("button", { name: en.user.dashboard.retry, exact: true }).click();
    await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
    await expect(accountUrlInput(page)).toHaveValue(accountUrl("blocked.example.test"));
    await expect(page.getByText(en.user.dashboard.loadError, { exact: true })).toHaveCount(0);
  });

  test("announcements show the newest three active, unexpired entries and open only on request", async ({ page }) => {
    const state = await mockDashboard(page);
    const longContent = `${"Latest service details. ".repeat(8)}\nRead this final line in the dialog.`;
    state.announcements = [
      { ...bulletin, id: "old", title: "Older active", created_at: "2032-05-01T12:00:00Z" },
      { ...bulletin, id: "middle", title: "Middle active", created_at: "2032-05-20T12:00:00Z" },
      { ...bulletin, id: "inactive", title: "Inactive newest", is_active: false, created_at: NOW },
      { ...bulletin, id: "expired", title: "Expired newest", expires_at: NOW, created_at: NOW },
      { ...bulletin, id: "new", title: "Newest active", content: longContent, created_at: "2032-05-31T12:00:00Z", expires_at: EXPIRES },
      { ...bulletin, id: "second", title: "Second active", created_at: "2032-05-25T12:00:00Z" },
    ];
    await openOverview(page);
    await expect(page.getByRole("button", { name: /^(Newest|Second|Middle) active$/ })).toHaveText([
      "Newest active", "Second active", "Middle active",
    ]);
    for (const title of ["Older active", "Inactive newest", "Expired newest"]) {
      await expect(page.getByRole("button", { name: title, exact: true })).toHaveCount(0);
    }
    await expect(page.getByText(`${longContent.slice(0, 100)}…`, { exact: true })).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByText("Read this final line in the dialog.", { exact: true })).toHaveCount(0);
    await page.getByRole("button", { name: "Newest active", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("Newest active", { exact: true })).toBeVisible();
    await expect(dialog.getByText("Read this final line in the dialog.", { exact: true })).toBeVisible();
    await dialog.getByRole("button", { name: en.user.announcements.close, exact: true }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await page.getByRole("button", { name: en.user.dashboard.refresh, exact: true }).click();
    await expect(page.getByRole("button", { name: "Newest active", exact: true })).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });

  test("an active subscriber sees the catalog and confirms the chosen duration before createOrder", async ({ page }, testInfo) => {
    const state = await mockDashboard(page);
    await page.setViewportSize({ width: 1280, height: 1000 });
    await page.goto("/portal/plan");
    await expect(page.getByRole("heading", { name: en.user.plan.currentPlan, exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: en.user.plan.availablePlans, exact: true })).toBeVisible();
    await expect(page.getByText(en.user.plan.order.notPurchasable, { exact: true })).toHaveCount(2);
    await expect(purchaseButton(page, "Unpriced preview")).toHaveCount(0);
    await expect(purchaseButton(page, "Managed enterprise")).toHaveCount(0);
    await choose(page, en.user.plan.order.chooseDuration, "90 days · $30.00");
    await purchaseButton(page).click();
    const review = page.getByRole("dialog");
    await expect(review.getByText(en.user.plan.order.reviewTitle, { exact: true })).toBeVisible();
    await expect(review.getByText("90 days", { exact: true })).toBeVisible();
    await expect(review.getByText("$30.00", { exact: true })).toBeVisible();
    await expect(review.getByText(en.user.plan.order.reviewHint, { exact: true })).toBeVisible();
    expect(state.orderPosts).toEqual([]);
    await testInfo.attach("purchase-confirmation", { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
    await review.getByRole("button", { name: en.user.plan.order.dismiss, exact: true }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    expect(state.orderPosts).toEqual([]);
    await purchaseButton(page).click();
    await page.getByRole("dialog").getByRole("button", { name: en.user.plan.order.confirmOrder, exact: true }).click();
    await expect(page.getByText(en.user.plan.order.placed, { exact: true })).toBeVisible();
    expect(state.orderPosts).toEqual([{ plan_id: "plus", duration_days: 90 }]);
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(purchaseButton(page)).toBeDisabled();
    expect(state.unexpectedWrites).toEqual([]);
  });

  test("an existing pending order leaves the catalog visible but blocks another purchase", async ({ page }) => {
    const state = await mockDashboard(page);
    state.orders = [{ ...pendingOrder }];
    await page.goto("/portal/plan");
    await expect(page.getByRole("heading", { name: en.user.plan.availablePlans, exact: true })).toBeVisible();
    await expect(page.getByText("Your Everyday Plus order (90 days, $30.00) is awaiting payment confirmation.", { exact: true })).toBeVisible();
    await expect(purchaseButton(page)).toBeDisabled();
    await expect(page.getByRole("button", { name: new RegExp(en.user.plan.order.chooseDuration) })).toBeDisabled();
    await expect(page.getByText(en.user.plan.order.blockedByPending, { exact: true })).toBeVisible();
    await expect(page.getByText(en.user.plan.order.notPurchasable, { exact: true })).toHaveCount(2);
    await expect(page.getByRole("dialog")).toHaveCount(0);
    expect(state.orderPosts).toEqual([]);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`Korean dashboard at 390px in ${theme} mode has no horizontal document overflow`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width: 390, height: 844 });
      await page.emulateMedia({ colorScheme: theme });
      await mockDashboard(page, "ko");
      await openOverview(page, ko.user.dashboard.title);
      await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
      await expect(page.getByText(ko.user.dashboard.monthlyTraffic, { exact: true })).toBeVisible();
      await expect(page.getByRole("heading", { name: ko.user.dashboard.quickConnect, exact: true })).toBeVisible();
      await expect(accountUrlInput(page, ko.user.devices.accountUrl)).toHaveValue(accountUrl("blocked.example.test"));
      await expect(page.getByRole("button", { name: ko.user.dashboard.subscriptionDetails, exact: true })).toBeVisible();
      await expect(page.getByRole("heading", { name: ko.user.dashboard.serverStatus, exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: bulletin.title, exact: true })).toBeVisible();
      expect(await page.evaluate(() => document.body.classList.contains("awsui-dark-mode"))).toBe(theme === "dark");
      expect(await page.evaluate(() => ({ document: document.documentElement.scrollWidth, body: document.body.scrollWidth, viewport: innerWidth }))).toEqual({ document: 390, body: 390, viewport: 390 });
      await testInfo.attach(`dashboard-ko-390-${theme}`, { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
    });
  }
});
