import { test, expect } from "@playwright/test";
import type { CreateSubscriptionDomainRequest, SubscriptionDomain } from "../src/api/types";
import { mockSession } from "./subscription-domain-fixtures";

test.use({ trace: "off", video: "off", screenshot: "off" });

const endpoint = "**/api/v1/admin/subscription-domains";
const firstDomain: SubscriptionDomain = {
  id: "first", domain: "first.example.test", enabled: true, is_public: true,
  display_order: 5, is_default: true, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
};

test.describe("PHS-027 admin subscription domains", () => {
  test.use({ storageState: { cookies: [], origins: [] } });
  test.beforeEach(async ({ page }) => {
    await mockSession(page, "admin");
    await page.route(endpoint, (route) => route.fulfill({ json: [firstDomain] }));
    await page.route(`${endpoint}/health`, (route) => route.fulfill({ json: [] }));
  });

  for (const width of [375, 1280]) {
    test(`CRUD, default, order, enablement and visibility at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      let domains = [firstDomain];
      let expectedRequest: CreateSubscriptionDomainRequest = {
        domain: "second.example.test", display_order: 0, enabled: true, is_public: true, is_default: true,
      };
      const mutations: string[] = [];
      await page.route(`${endpoint}/second`, async (route) => {
        mutations.push(route.request().method());
        if (route.request().method() === "DELETE") {
          domains = domains.filter((domain) => domain.id !== "second");
          await route.fulfill({ status: 204 });
        } else {
          expect(route.request().method()).toBe("PUT");
          expect(route.request().postDataJSON()).toEqual(expectedRequest);
          domains = domains.map((domain) => domain.id === "second" ? { ...domain, ...expectedRequest } : domain);
          await route.fulfill({ json: domains[0] });
        }
      });
      await page.route(endpoint, async (route) => {
        if (route.request().method() === "POST") {
          mutations.push("POST");
          expect(route.request().postDataJSON()).toEqual(expectedRequest);
          domains = [
            { ...firstDomain, id: "second", ...expectedRequest },
            { ...firstDomain, is_default: false },
          ];
          await route.fulfill({ status: 201, json: domains[0] });
        } else {
          await route.fulfill({ json: domains });
        }
      });
      await page.goto("/admin/subscription-domains");
      await page.getByRole("button", { name: "Add domain", exact: true }).click();
      const form = page.getByRole("dialog", { name: "Add subscription domain" });
      await form.getByRole("textbox", { name: "Domain", exact: true }).fill("second.example.test");
      await form.getByRole("spinbutton", { name: "Display order", exact: true }).fill("0");
      await form.getByRole("checkbox", { name: "Default domain", exact: true }).check();
      await form.getByRole("button", { name: "Save", exact: true }).click();
      await expect(page.getByText("Subscription domain added.", { exact: true })).toBeVisible();
      const rows = page.locator("tbody tr");
      await expect(rows.nth(0).locator("td").nth(0)).toHaveText("second.example.test");
      await expect(rows.nth(0).locator("td").nth(4)).toHaveText("Yes");
      await expect(rows.nth(1).locator("td").nth(4)).toHaveText("No");
      await rows.nth(0).getByRole("button", { name: "Edit", exact: true }).click();
      const edit = page.getByRole("dialog", { name: "Edit subscription domain" });
      await edit.getByRole("textbox", { name: "Domain", exact: true }).fill("renamed.example.test");
      await edit.getByRole("spinbutton", { name: "Display order", exact: true }).fill("8");
      for (const name of ["Enabled", "Visible to users", "Default domain"]) {
        await edit.getByRole("checkbox", { name, exact: true }).uncheck();
      }
      expectedRequest = { domain: "renamed.example.test", display_order: 8, enabled: false, is_public: false, is_default: false };
      await edit.getByRole("button", { name: "Save", exact: true }).click();
      await expect(page.getByText("Subscription domain updated.", { exact: true })).toBeVisible();
      await expect(rows.nth(1).locator("td").nth(0)).toHaveText("renamed.example.test");
      for (const column of [1, 2, 4]) await expect(rows.nth(1).locator("td").nth(column)).toHaveText("No");
      await expect(rows.nth(1).locator("td").nth(3)).toHaveText("8");
      await rows.nth(1).getByRole("button", { name: "Delete", exact: true }).click();
      await page.getByRole("dialog", { name: "Delete subscription domain" }).getByRole("button", { name: "Cancel", exact: true }).click();
      await expect(rows).toHaveCount(2);
      await rows.nth(1).getByRole("button", { name: "Delete", exact: true }).click();
      await page.getByRole("dialog", { name: "Delete subscription domain" }).getByRole("button", { name: "Delete", exact: true }).click();
      await expect(page.getByText("Subscription domain deleted.", { exact: true })).toBeVisible();
      await expect(rows).toHaveCount(1);
      expect(mutations).toEqual(["POST", "PUT", "DELETE"]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    });
  }

  for (const [status, label] of Object.entries({ healthy: "Healthy", warning: "Warning", failed: "Failed", unknown: "Unknown" })) {
    test(`health enum ${status} labels DNS, TLS and certificate`, async ({ page }) => {
      await page.route(`${endpoint}/health`, (route) => route.fulfill({ json: [{
        domain_id: "first", dns_status: status, tls_status: status, certificate_status: status,
        certificate_expires_at: null, checked_at: null, error: null,
      }] }));
      await page.goto("/admin/subscription-domains");
      const row = page.locator("tbody tr");
      await expect(row.getByText(label, { exact: true })).toHaveCount(3);
      await expect(row.getByText("Expiry unavailable", { exact: true })).toBeVisible();
      await expect(page.getByText("Administrator health", { exact: true })).toBeVisible();
    });
  }

  test("health endpoint failure leaves CRUD available with unknown labels", async ({ page }) => {
    await page.route(`${endpoint}/health`, (route) => route.fulfill({ status: 503, json: { error: "fixture" } }));
    await page.goto("/admin/subscription-domains");
    await expect(page.locator("tbody tr").getByText("Unknown", { exact: true })).toHaveCount(3);
    await page.getByRole("button", { name: "Add domain", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "Add subscription domain" })).toBeVisible();
  });

  test("invalid order and failed save never claim success", async ({ page }) => {
    let saves = 0;
    await page.route(endpoint, (route) => {
      if (route.request().method() === "POST") {
        saves++;
        return route.fulfill({ status: 500, json: { error: "fixture" } });
      }
      return route.fulfill({ json: [] });
    });
    await page.goto("/admin/subscription-domains");
    await expect(page.getByText("No subscription domains configured.", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Add domain", exact: true }).click();
    const form = page.getByRole("dialog", { name: "Add subscription domain" });
    await form.getByRole("textbox", { name: "Domain", exact: true }).fill("valid.example.test");
    await form.getByRole("spinbutton", { name: "Display order", exact: true }).fill("-1");
    await form.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Display order must be a non-negative whole number.", { exact: true })).toBeVisible();
    expect(saves).toBe(0);
    await form.getByRole("spinbutton", { name: "Display order", exact: true }).fill("0");
    await form.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Failed to save subscription domain.", { exact: true })).toBeVisible();
    await expect(form).toBeVisible();
    await expect(form.getByRole("textbox", { name: "Domain", exact: true })).toHaveValue("valid.example.test");
    await expect(page.getByText("Subscription domain added.", { exact: true })).toHaveCount(0);
    expect(saves).toBe(1);
  });

  for (const scenario of ["error", "malformed"] as const) {
    test(`${scenario} pool response is recoverable with refresh`, async ({ page }) => {
      await page.route(endpoint, (route) => route.fulfill({ status: scenario === "error" ? 500 : 200, json: {} }));
      await page.goto("/admin/subscription-domains");
      await expect(page.getByText("Failed to load subscription domains or their health.", { exact: true })).toBeVisible();
      await page.route(endpoint, (route) => route.fulfill({ json: [firstDomain] }));
      await page.getByRole("button", { name: "Refresh", exact: true }).click();
      await expect(page.getByText("first.example.test", { exact: true })).toBeVisible();
      await expect(page.getByText("Failed to load subscription domains or their health.", { exact: true })).toHaveCount(0);
    });
  }
});

test.describe("PHS-027 public filtering integration", () => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test("only enabled public domains reach the user picker without administrator health", async ({ page, request }) => {
    const login = await request.post("/api/v1/admin/auth/login", { data: {
      email: process.env.E2E_ADMIN_EMAIL || "admin@example.com",
      password: process.env.E2E_ADMIN_PASSWORD || "Admin1234!",
    } });
    expect(login.ok()).toBe(true);
    const admin: unknown = await login.json();
    if (typeof admin !== "object" || admin === null || !("token" in admin) || typeof admin.token !== "string") {
      throw new Error("Admin fixture login did not return a token");
    }
    const headers = { Authorization: `Bearer ${admin.token}` };
    const suffix = crypto.randomUUID();
    const email = `domain-${suffix}@example.test`;
    const password = "DomainFixturePass123!";
    const registration = await request.post("/api/v1/auth/register", { data: { email, password, name: "Domain fixture" } });
    expect(registration.ok()).toBe(true);
    const userLogin = await request.post("/api/v1/auth/login", { data: { email, password } });
    expect(userLogin.ok()).toBe(true);
    const user: unknown = await userLogin.json();
    if (typeof user !== "object" || user === null || !("token" in user) || typeof user.token !== "string") {
      throw new Error("User fixture login did not return a token");
    }
    const ids: string[] = [];
    try {
      for (const [name, enabled, isPublic] of [["public", true, true], ["private", true, false], ["disabled", false, true]] as const) {
        const response = await request.post("/api/v1/admin/subscription-domains", { headers, data: {
          domain: `${name}-${suffix}.example.test`, enabled, is_public: isPublic, is_default: false, display_order: 0,
        } });
        expect(response.ok()).toBe(true);
        const created: unknown = await response.json();
        if (typeof created !== "object" || created === null || !("id" in created) || typeof created.id !== "string") {
          throw new Error("Domain fixture create did not return an id");
        }
        ids.push(created.id);
      }
      const response = await request.get("/api/v1/user/subscription-domains", { headers: { Authorization: `Bearer ${user.token}` } });
      expect(response.ok()).toBe(true);
      const domains: unknown = await response.json();
      expect(domains).toEqual([{
        id: ids[0], domain: `public-${suffix}.example.test`, display_order: 0, is_default: false,
        created_at: expect.stringMatching(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z$/),
      }]);
      await page.addInitScript((token) => {
        localStorage.clear();
        localStorage.setItem("proxima_user_token", token);
        localStorage.setItem("i18nextLng", "en");
      }, user.token);
      await page.route("**/api/v1/user/profile", (route) => route.fulfill({ json: { plan_name: "Fixture", sub_token: "fixture-token" } }));
      await page.goto("/portal/devices");
      await expect(page.getByRole("tab")).toHaveText([`public-${suffix}.example.test`]);
      await expect(page.getByRole("tabpanel").locator("code")).toHaveText(`https://public-${suffix}.example.test/sub/fixture-token`);
    } finally {
      for (const id of ids) {
        const deleted = await request.delete(`/api/v1/admin/subscription-domains/${id}`, { headers });
        expect(deleted.ok()).toBe(true);
      }
    }
  });
});
