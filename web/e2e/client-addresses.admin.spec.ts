import { test, expect, type Page } from "@playwright/test";
import { mockSession } from "./subscription-domain-fixtures";
import type { UserClientAddresses, UserTunnelSessions } from "../src/api/types";

test.use({ storageState: { cookies: [], origins: [] } });

async function mockUser(page: Page) {
  await mockSession(page, "admin", "en");
  await page.route("**/src/api/**", (route) => route.continue());
  await page.route("**/api/v1/admin/users/address-user", (route) => route.fulfill({ json: {
    id: "address-user", email: "address@example.test", name: "Address fixture",
    status: "active", is_active: true, traffic_used: 0, sub_token: "fixture-token",
    created_at: "2026-01-01T00:00:00Z", devices: [],
  } }));
  await page.route("**/api/v1/admin/plans", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/admin/users/address-user/traffic?*", (route) => route.fulfill({ json: {
    traffic_used: 0, traffic_limit: null, traffic_remaining: null,
    percentage: 0, unlimited: true, window: "month", by_node: [],
  } }));
  await page.route("**/api/v1/admin/users/address-user/login-history?*", (route) => route.fulfill({ json: [] }));
}

// Tunnel sessions and client addresses live on the "Connections & sessions" tab.
async function openSessionsTab(page: Page) {
  await page.goto("/admin/users/address-user");
  await page.getByRole("tab", { name: "Connections & sessions", exact: true }).click();
}

function addressSection(page: Page) {
  return page.locator("div").filter({
    has: page.getByRole("heading", { name: "Observed client addresses", exact: true }),
  }).filter({ has: page.getByRole("term").filter({ hasText: "Observed address count" }) }).last();
}

for (const snapshot of [
  { complete: true, addresses: ["203.0.113.8", "2001:db8::8"], count: 2 },
  { complete: true, addresses: [], count: 0 },
  // Even a numeric count from an incomplete response must not be displayed.
  { complete: false, addresses: ["203.0.113.8"], count: 99 },
] satisfies UserClientAddresses[]) {
  test(`observed addresses: complete=${snapshot.complete}, count=${snapshot.count}`, async ({ page }) => {
    await mockUser(page);
    await page.route("**/api/v1/admin/users/address-user/client-addresses", (route) => route.fulfill({ json: snapshot }));
    await openSessionsTab(page);
    const section = addressSection(page);
    await expect(section.getByText("Observed address count", { exact: true })).toBeVisible();
    // Same NAT counts once, an IPv6 /64 counts once, and this is not a device count.
    await expect(section.getByText(/behind the same NAT share one/)).toBeVisible();
    await expect(section.getByText(/closed automatically/)).toBeVisible();
    await expect(section.getByText(/not a count of physical devices/)).toBeVisible();
    for (const address of snapshot.addresses) {
      await expect(section.getByText(address, { exact: true })).toBeVisible();
    }
    if (snapshot.complete) {
      await expect(section.getByText(String(snapshot.count), { exact: true })).toBeVisible();
    } else {
      await expect(section.getByText("Unknown", { exact: true })).toBeVisible();
      await expect(section.getByText("99", { exact: true })).toHaveCount(0);
      await expect(section.getByText(/Observations are incomplete/)).toBeVisible();
    }
  });
}

function sessionSection(page: Page) {
  return page.locator("div").filter({
    has: page.getByRole("heading", { name: "Authenticated tunnel sessions", exact: true }),
  }).filter({ has: page.getByRole("term").filter({ hasText: "Authenticated tunnel session count" }) }).last();
}

for (const snapshot of [
  { complete: true, count: 2, enforced: false },
  { complete: false, count: 99, enforced: false },
] satisfies UserTunnelSessions[]) {
  test(`tunnel sessions: complete=${snapshot.complete}`, async ({ page }) => {
    await mockUser(page);
    await page.route("**/api/v1/admin/users/address-user/tunnel-sessions", (route) => route.fulfill({ json: snapshot }));
    await page.route("**/api/v1/admin/users/address-user/client-addresses", (route) => route.fulfill({ json: {
      complete: true, addresses: ["203.0.113.8"], count: 1,
    } }));
    await openSessionsTab(page);
    const section = sessionSection(page);
    await expect(section.getByText(/same IP address or UUID/)).toBeVisible();
    await expect(section.getByText(/not a count of physical devices/)).toBeVisible();
    await expect(section.getByText(/not enforced yet/)).toBeVisible();
    if (snapshot.complete) {
      await expect(section.getByText("2", { exact: true })).toBeVisible();
      await expect(addressSection(page).getByText("1", { exact: true })).toBeVisible();
    } else {
      await expect(section.getByText("Unknown", { exact: true })).toBeVisible();
      await expect(section.getByText("99", { exact: true })).toHaveCount(0);
      await expect(section.getByText(/report is incomplete/)).toBeVisible();
    }
  });
}

test("over-limit sessions warn but never claim a completed block", async ({ page }) => {
  await mockUser(page);
  await page.route("**/api/v1/admin/users/address-user/tunnel-sessions", (route) => route.fulfill({ json: { complete: true, count: 2, limit: 1, over_limit: true, enforced: false } }));
  await openSessionsTab(page);
  const section = sessionSection(page);
  await expect(section.getByText("Plan session limit", { exact: true })).toBeVisible();
  await expect(section.getByText(/Observed sessions exceed the plan limit/)).toBeVisible();
  await expect(section.getByText(/not a confirmed block/)).toBeVisible();
});

test("failed address refresh clears the count without hiding user details", async ({ page }) => {
  await mockUser(page);
  let failed = false;
  await page.route("**/api/v1/admin/users/address-user/client-addresses", (route) => route.fulfill(
    failed ? { status: 503, json: { error: "unavailable" } }
      : { json: { complete: true, addresses: ["203.0.113.8"], count: 1 } },
  ));
  await openSessionsTab(page);
  const section = addressSection(page);
  await expect(section.getByText("1", { exact: true })).toBeVisible();
  failed = true;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(section.getByText(/Unable to load client addresses/)).toBeVisible();
  await expect(section.getByText("1", { exact: true })).toHaveCount(0);
  await expect(section.getByText("Unknown", { exact: true }).first()).toBeVisible();
  await page.getByRole("tab", { name: "Overview", exact: true }).click();
  await expect(page.getByText("address@example.test", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save changes" })).toBeVisible();
});
