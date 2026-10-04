import { test, expect, type Page } from "@playwright/test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QRCodeSVG } from "qrcode.react";
import type {
  Announcement, AvailableNode, CreateDeviceRequest, CreateOrderRequest,
  Device, OrderResponse, UserPlanItem, UserProfile, UserSummary,
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
};
const devices: Device[] = [
  {
    id: "phone", name: "My phone", xray_uuid: "phone-uuid", created_at: NOW,
    subscription_url: "http://legacy.example.test:9443/sub/phone%2Ftoken?token=value%2Bsecret&format=clash&source=dashboard",
  },
  {
    id: "laptop", name: "My laptop", xray_uuid: "laptop-uuid", created_at: NOW,
    subscription_url: "/sub/laptop%2Ftoken?source=dashboard&format=clash&token=other%2Bsecret",
  },
];
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
    devices: devices.map((device) => ({ ...device })),
    announcements: [{ ...bulletin }], nodes: nodes.map((node) => ({ ...node })),
    plans, orders: [] as OrderResponse[],
    failures: new Set<string>(), reads: new Map<string, number>(),
    devicePosts: [] as CreateDeviceRequest[], orderPosts: [] as CreateOrderRequest[],
    unexpectedWrites: [] as string[], summaryGate: null as Promise<void> | null,
    deviceGate: null as Promise<void> | null,
    newDevice: {
      id: "new-device", name: "New phone", xray_uuid: "new-uuid", created_at: NOW,
      subscription_url: "/sub/account%2Ftoken/new-device?token=new%2Bsecret&source=dashboard",
    } satisfies Device,
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
      if (path === "summary" && state.summaryGate) await state.summaryGate;
      if (path === "devices" && state.deviceGate) await state.deviceGate;
      const responses: Record<string, unknown> = {
        summary: state.summary, profile: state.profile, devices: state.devices,
        "subscription-domains": publicDomains, nodes: state.nodes,
        announcements: state.announcements, plans: state.plans, orders: state.orders,
        "payment-providers": [{ name: "manual", mode: "manual" }],
      };
      await route.fulfill({ json: responses[path] ?? {} });
    } else if (request.method() === "POST" && path === "devices") {
      state.devicePosts.push(request.postDataJSON());
      state.devices.push({ ...state.newDevice });
      state.summary.devices = state.devices.length;
      // The real create endpoint omits the token-bearing subscription URL.
      const createdDevice: Device = {
        id: state.newDevice.id, name: state.newDevice.name,
        xray_uuid: state.newDevice.xray_uuid, created_at: state.newDevice.created_at,
      };
      await route.fulfill({ status: 201, json: createdDevice });
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

function purchaseButton(page: Page, planName = "Everyday Plus") {
  return page.getByRole("button", { name: `${en.user.plan.order.placeOrder} ${planName}`, exact: true });
}

test.describe("mocked user dashboard and purchase journey", () => {
  test("overview shows monthly used-of-cap, days left and device counts, not IP counts", async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 1000 });
    await page.emulateMedia({ colorScheme: "light" });
    await mockDashboard(page);
    await openOverview(page);
    await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.monthlyTraffic, { exact: true })).toBeVisible();
    await expect(page.getByText("25.0 GB of 100.0 GB used", { exact: true })).toBeVisible();
    await expect(page.getByText("100.0 GB per month", { exact: true })).toBeVisible();
    await expect(page.getByText("75.0 GB remaining", { exact: true })).toBeVisible();
    await expect(page.getByText("10 days left", { exact: true })).toBeVisible();
    await expect(page.getByText("2 of 4", { exact: true })).toBeVisible();
    await expect(page.getByText("1 currently connected", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: en.user.dashboard.connections, exact: true }).click();
    await expect(page.getByText("2 of 5", { exact: true })).toBeVisible();
    await expect(page.getByText("1 of 2 online", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: en.user.devices.addDevice, exact: true })).toBeEnabled();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await testInfo.attach("dashboard-desktop-light", { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
  });

  test("quick-add reloads the canonical URL after POST and refreshes summary without navigation", async ({ page }) => {
    const state = await mockDashboard(page);
    state.devices = [];
    state.summary.devices = 0;
    state.summary.max_devices = 3;
    await openOverview(page);
    await expect(page.getByText("0 of 3", { exact: true })).toBeVisible();
    const initialSummaryReads = state.reads.get("summary") ?? 0;
    const initialDeviceReads = state.reads.get("devices") ?? 0;
    let releaseSummary!: () => void;
    let releaseDevices!: () => void;
    state.summaryGate = new Promise<void>((resolve) => { releaseSummary = resolve; });
    state.deviceGate = new Promise<void>((resolve) => { releaseDevices = resolve; });
    try {
      await page.getByRole("button", { name: en.user.devices.addDevice, exact: true }).click();
      const dialog = page.getByRole("dialog");
      await dialog.getByRole("textbox", { name: en.user.devices.deviceName, exact: true }).fill("  New phone  ");
      await dialog.getByRole("button", { name: en.user.dashboard.addAndGetUrl, exact: true }).click();
      await expect(page.getByRole("dialog")).toHaveCount(0);
      expect(state.devicePosts).toEqual([{ name: "New phone" }]);
      await expect.poll(() => state.reads.get("devices") ?? 0).toBeGreaterThan(initialDeviceReads);
      await expect.poll(() => state.reads.get("summary") ?? 0).toBeGreaterThan(initialSummaryReads);
      await expect(page.getByRole("textbox", { name: en.user.devices.subscriptionUrl, exact: true })).toHaveCount(0);
      await expect(page.getByRole("button", { name: en.user.dashboard.copyUrl, exact: true })).toHaveCount(0);
      state.deviceGate = null;
      releaseDevices();
      await expect(page.getByText(en.user.dashboard.deviceReady, { exact: true })).toBeVisible();
      await expect(page.getByRole("textbox", { name: en.user.devices.subscriptionUrl, exact: true })).toHaveValue(
        "https://blocked.example.test/sub/account%2Ftoken/new-device?token=new%2Bsecret&source=dashboard",
      );
      await expect(page).toHaveURL(/\/portal\/dashboard$/);
    } finally {
      state.summaryGate = null;
      state.deviceGate = null;
      releaseSummary();
      releaseDevices();
    }
    await expect(page.getByText("1 of 3", { exact: true })).toBeVisible();
    expect(state.unexpectedWrites).toEqual([]);
  });

  test("device, app format and public host selections preserve encoded route/query for copy and QR", async ({ page }) => {
    await mockDashboard(page);
    await openOverview(page);
    const url = page.getByRole("textbox", { name: en.user.devices.subscriptionUrl, exact: true });
    await expect(url).toHaveValue(
      "https://blocked.example.test/sub/phone%2Ftoken?token=value%2Bsecret&source=dashboard",
    );
    await choose(page, en.user.dashboard.selectDevice, "My laptop");
    await choose(page, en.user.dashboard.clientFormat, "Sing-box");
    await choose(page, en.user.dashboard.subscriptionDomain, "working.example.test");
    const selected = "https://working.example.test/sub/laptop%2Ftoken?source=dashboard&format=singbox&token=other%2Bsecret";
    await expect(url).toHaveValue(selected);
    await page.getByRole("button", { name: en.user.dashboard.copyUrl, exact: true }).click();
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(selected);
    await page.getByRole("button", { name: en.user.devices.showQr, exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText(en.user.devices.qrTitle, { exact: true }).first()).toBeVisible();
    const qr = dialog.locator('svg[width="220"][height="220"]');
    await expect(qr).toBeVisible();
    // Compare the encoded matrix, not just the presence of an SVG: the QR must
    // encode exactly the selected URL, including its token and chosen format.
    const expectedSvg = renderToStaticMarkup(createElement(QRCodeSVG, { value: selected, size: 220 }));
    const expectedPaths = [...expectedSvg.matchAll(/<path\b[^>]*\bd="([^"]+)"/g)].map((match) => match[1]);
    expect(expectedPaths.length).toBeGreaterThan(0);
    expect(await qr.locator("path").evaluateAll((paths) => paths.map((path) => path.getAttribute("d")))).toEqual(expectedPaths);
    await dialog.getByRole("button", { name: en.user.dashboard.copyUrl, exact: true }).click();
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(selected);
  });

  test("no-plan overview offers purchase navigation and disables device creation", async ({ page }) => {
    const state = await mockDashboard(page);
    state.summary = { ...overview, plan_name: null, plan_expires_at: null, traffic_limit: null, devices: 0 };
    state.profile = { ...profile, plan_name: undefined, plan_expires_at: undefined };
    state.devices = [];
    await openOverview(page);
    await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.noPlanDescription, { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: en.user.devices.addDevice, exact: true })).toBeDisabled();
    await page.getByRole("button", { name: en.user.dashboard.browsePlans, exact: true }).first().click();
    await expect(page).toHaveURL(/\/portal\/plan$/);
    await expect(page.getByRole("heading", { name: en.user.plan.availablePlans, exact: true })).toBeVisible();
    await expect(purchaseButton(page)).toBeEnabled();
    expect(state.devicePosts).toEqual([]);
    expect(state.orderPosts).toEqual([]);
  });

  for (const scenario of ["expired", "suspended", "device-limit"] as const) {
    test(`${scenario} account cannot quick-add a device`, async ({ page }) => {
      const state = await mockDashboard(page);
      if (scenario === "expired") {
        // A stale active status must not override an already elapsed expiry.
        state.summary.plan_expires_at = "2032-05-31T12:00:00Z";
      } else if (scenario === "suspended") {
        state.summary.status = "suspended";
      } else {
        // A stale summary undercount must not override the loaded device list.
        state.summary.devices = 0;
        state.summary.max_devices = 2;
      }
      await openOverview(page);
      await expect(page.getByRole("button", { name: en.user.devices.addDevice, exact: true })).toBeDisabled();
      if (scenario === "expired") {
        await expect(page.getByText(en.user.dashboard.expiredHint, { exact: true })).toBeVisible();
      } else if (scenario === "suspended") {
        await expect(page.getByText(en.user.dashboard.suspendedHint, { exact: true })).toHaveCount(2);
      } else {
        await expect(page.getByText(en.user.devices.limitReached, { exact: true })).toBeVisible();
      }
      await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toHaveCount(0);
      await expect(page.getByRole("dialog")).toHaveCount(0);
      expect(state.devicePosts).toEqual([]);
    });
  }

  test("zero usage and unlimited allowances remain an active plan, not a no-plan state", async ({ page }) => {
    const state = await mockDashboard(page);
    state.summary = {
      ...overview, traffic_used: 0, traffic_limit: 0, plan_expires_at: null,
      devices: 0, max_devices: 0, online: 0, online_ips: 0, max_concurrent: 0,
    };
    state.devices = [];
    await openOverview(page);
    await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
    await expect(page.getByText("0 B", { exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.trafficUnlimited, { exact: true })).toBeVisible();
    await expect(page.getByText(en.user.dashboard.noExpiry, { exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "0 · No limit", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: en.user.devices.addDevice, exact: true })).toBeEnabled();
    await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toHaveCount(0);
    await expect(page.getByText(en.user.dashboard.connectNeedsPlan, { exact: true })).toHaveCount(0);
    await expect(page.getByRole("progressbar")).toHaveCount(0);
  });

  test("independent API failures preserve the plan and healthy sections; retries recover only their resource", async ({ page }) => {
    const state = await mockDashboard(page);
    for (const path of ["devices", "nodes", "announcements", "subscription-domains"]) state.failures.add(path);
    await openOverview(page);
    await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
    for (const message of [en.user.devices.loadError, en.user.nodes.loadError, en.user.announcements.loadError]) {
      await expect(page.getByText(message, { exact: true })).toBeVisible();
    }
    await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toHaveCount(0);
    const nodeReads = state.reads.get("nodes");
    const announcementReads = state.reads.get("announcements");
    state.failures.delete("devices");
    await page.getByRole("button", { name: en.user.dashboard.retry, exact: true }).first().click();
    await expect(page.getByRole("textbox", { name: en.user.devices.subscriptionUrl, exact: true })).toHaveValue(
      "http://legacy.example.test:9443/sub/phone%2Ftoken?token=value%2Bsecret&source=dashboard",
    );
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
    await expect(page.getByRole("textbox", { name: en.user.devices.subscriptionUrl, exact: true })).toHaveValue(
      "https://blocked.example.test/sub/phone%2Ftoken?token=value%2Bsecret&source=dashboard",
    );
    await expect(page.getByText(en.user.dashboard.domainFallback, { exact: true })).toHaveCount(0);
    await expect(page.getByText(en.user.dashboard.connectNeedsPlan, { exact: true })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: en.user.dashboard.noPlanTitle, exact: true })).toHaveCount(0);
    state.failures.delete("summary");
    await page.getByRole("button", { name: en.user.dashboard.retry, exact: true }).click();
    await expect(page.getByRole("heading", { name: "Everyday Plus", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: en.user.devices.addDevice, exact: true })).toBeEnabled();
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
      await expect(page.getByRole("textbox", { name: ko.user.devices.subscriptionUrl, exact: true })).toHaveValue(
        "https://blocked.example.test/sub/phone%2Ftoken?token=value%2Bsecret&source=dashboard",
      );
      await expect(page.getByRole("heading", { name: ko.user.dashboard.serverStatus, exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: bulletin.title, exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: ko.user.devices.addDevice, exact: true })).toBeEnabled();
      expect(await page.evaluate(() => document.body.classList.contains("awsui-dark-mode"))).toBe(theme === "dark");
      expect(await page.evaluate(() => ({ document: document.documentElement.scrollWidth, body: document.body.scrollWidth, viewport: innerWidth }))).toEqual({ document: 390, body: 390, viewport: 390 });
      await testInfo.attach(`dashboard-ko-390-${theme}`, { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
    });
  }
});
