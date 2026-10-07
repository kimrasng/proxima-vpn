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

// Contains reserved characters so every assertion also proves the token is
// percent-encoded exactly once into the path.
export const fixtureToken = "fixture/token+value";
export const fixtureSubscriptionPath = "/sub/fixture%2Ftoken%2Bvalue";

export async function mockSubscription(page: Page, subToken = fixtureToken) {
  await page.route("**/api/v1/user/profile", (route) => route.fulfill({
    json: { name: "Browser fixture", email: "fixture@example.test", plan_name: "Fixture", sub_token: subToken },
  }));
  await page.route("**/api/v1/user/subscription-domains", (route) => route.fulfill({ json: publicDomains }));
}

export async function expectSelectedDomain(page: Page, domain: string) {
  await expect(page.getByRole("tab", { name: domain, exact: false })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tabpanel").locator("code")).toHaveText(`https://${domain}${fixtureSubscriptionPath}`);
}

// While the per-app fallback list is collapsed and the QR dialog is closed,
// the account URL is the only visible <code> on the subscription page.
export async function expectOnlyAccountUrl(page: Page, url: string) {
  await expect(page.locator("code").filter({ visible: true })).toHaveText([url]);
}
