import { expect, type Page } from "@playwright/test";
import type { PublicSubscriptionDomain } from "../src/api/types";

export const publicDomains: PublicSubscriptionDomain[] = [
  { id: "working", domain: "working.example.test", display_order: 2, is_default: false },
  { id: "timeout", domain: "timeout.example.test", display_order: 1, is_default: false },
  { id: "blocked", domain: "blocked.example.test", display_order: 9, is_default: true },
];

export async function mockSession(page: Page, role: "admin" | "user", language = "en") {
  await page.addInitScript(({ role, language }) => {
    localStorage.clear();
    localStorage.setItem(`proxima_${role}_token`, "browser-fixture-not-a-credential");
    localStorage.setItem("i18nextLng", language);
  }, { role, language });
  await page.route("**/api/**", (route) => route.fulfill({ json: {} }));
  await page.route("**/api/v1/admin/stats/alerts", (route) => route.fulfill({ json: { total: 0, items: [] } }));
}

export async function mockDevices(page: Page, subscriptionUrl = "/sub/fixture-route?format=clash&source=fixture") {
  await page.route("**/api/v1/user/profile", (route) => route.fulfill({
    json: { name: "Browser fixture", email: "fixture@example.test", plan_name: "Fixture", max_devices: 3 },
  }));
  await page.route("**/api/v1/user/devices", (route) => route.fulfill({
    json: [{ id: "fixture-device", name: "Fixture device", xray_uuid: "fixture-uuid", subscription_url: subscriptionUrl }],
  }));
  await page.route("**/api/v1/user/subscription-domains", (route) => route.fulfill({ json: publicDomains }));
}

export async function expectSelectedDomain(page: Page, domain: string) {
  await expect(page.getByRole("tab", { name: domain, exact: false })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tabpanel").locator("code")).toHaveText(
    `https://${domain}/sub/fixture-route?source=fixture`,
  );
}
