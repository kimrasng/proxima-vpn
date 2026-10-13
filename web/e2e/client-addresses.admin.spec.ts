import { test, expect, type Page } from "@playwright/test";
import { mockSession } from "./subscription-domain-fixtures";
import type { UserClientAddresses } from "../src/api/types";

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
    await page.goto("/admin/users/address-user");
    const section = addressSection(page);
    await expect(section.getByText("Observed address count", { exact: true })).toBeVisible();
    await expect(section.getByText(/same NAT share an IP address/)).toBeVisible();
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

test("failed address refresh clears the count without hiding user details", async ({ page }) => {
  await mockUser(page);
  let failed = false;
  await page.route("**/api/v1/admin/users/address-user/client-addresses", (route) => route.fulfill(
    failed ? { status: 503, json: { error: "unavailable" } }
      : { json: { complete: true, addresses: ["203.0.113.8"], count: 1 } },
  ));
  await page.goto("/admin/users/address-user");
  const section = addressSection(page);
  await expect(section.getByText("1", { exact: true })).toBeVisible();
  failed = true;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(section.getByText(/Unable to load client addresses/)).toBeVisible();
  await expect(section.getByText("1", { exact: true })).toHaveCount(0);
  await expect(section.getByText("Unknown", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("address@example.test", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save changes" })).toBeVisible();
});
