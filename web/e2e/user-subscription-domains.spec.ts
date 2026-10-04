import { test, expect } from "@playwright/test";
import { expectSelectedDomain, mockDevices, mockSession, publicDomains } from "./subscription-domain-fixtures";
import en from "../src/i18n/locales/en.json" with { type: "json" };
import ko from "../src/i18n/locales/ko.json" with { type: "json" };
import zh from "../src/i18n/locales/zh.json" with { type: "json" };

test.describe("PHS-027 user subscription domains", () => {
  test.beforeEach(async ({ page }) => {
    await mockSession(page, "user");
    await mockDevices(page);
  });

  test("baseline: default wins over order and manual choice preserves route/query", async ({ page }) => {
    // Given an intentionally unsorted public pool and a legacy absolute URL.
    await mockDevices(page, "https://legacy.example.test/sub/fixture-route?format=clash&source=fixture");
    await page.goto("/portal/devices");
    await expectSelectedDomain(page, "blocked.example.test");
    await expect(page.getByRole("tab")).toHaveText([
      "blocked.example.test", "timeout.example.test", "working.example.test",
    ]);
    // When the user manually chooses a different host.
    await page.getByRole("tab", { name: "working.example.test", exact: true }).click();
    // Then only the host/format change; route and unrelated query survive.
    await expectSelectedDomain(page, "working.example.test");
  });

  test("relative subscription URLs do not inherit the panel port", async ({ page }) => {
    await page.goto("/portal/devices");
    await expectSelectedDomain(page, "blocked.example.test");
  });

  test("legacy port replacement preserves encoded subscription token and unrelated parameters", async ({ page }) => {
    await mockDevices(page, "http://legacy.example.test:9443/sub/fixture%2Fencoded?token=fixture%2Bvalue&format=clash");
    await page.goto("/portal/devices");
    await expect(page.getByRole("tabpanel").locator("code")).toHaveText(
      "https://blocked.example.test/sub/fixture%2Fencoded?token=fixture%2Bvalue",
    );
  });

  test("missing subscription URL retains the device UUID route", async ({ page }) => {
    await mockDevices(page, "");
    await page.goto("/portal/devices");
    await expect(page.getByRole("tabpanel").locator("code")).toHaveText("https://blocked.example.test/sub/fixture-uuid");
  });

  for (const scenario of ["empty", "error", "malformed"] as const) {
    test(`verifier: ${scenario} fallback preserves existing format and complete encoded URL`, async ({ page }) => {
      const original = "https://legacy.example.test:9443/sub/encoded%2Ftoken?format=clash&source=fixture";
      await mockDevices(page, original);
      await page.route("**/api/v1/user/subscription-domains", (route) => route.fulfill({
        status: scenario === "error" ? 503 : 200,
        json: scenario === "empty" ? [] : {},
      }));
      await page.goto("/portal/devices");
      await expect(page.locator("code")).toHaveCount(3);
      await expect(page.locator("code").last()).toHaveText(original);
      await expect(page.getByRole("tab")).toHaveCount(0);
    });
  }

  const malformedRows = [
    ...["user@valid.example.test", "valid.example.test/path", "valid.example.test?q=1", "valid.example.test#fragment",
      "*.example.test", "bad..example.test", "-bad.example.test", "bad_label.example.test", "999.1"].map((domain) => ({
      name: `invalid authority ${domain}`, row: { id: domain, domain, display_order: 0, is_default: true },
    })),
    { name: "legacy IPv4", row: { id: "ipv4", domain: "192.0.2.1", display_order: 0, is_default: true } },
    { name: "short IPv4", row: { id: "short-ipv4", domain: "192.1", display_order: 0, is_default: true } },
    { name: "hex IPv4", row: { id: "hex-ipv4", domain: "0xc0000201", display_order: 0, is_default: true } },
    { name: "IPv6", row: { id: "ipv6", domain: "2001:db8::1", display_order: 0, is_default: true } },
    { name: "bracketed IPv6", row: { id: "bracket-ipv6", domain: "[2001:db8::1]", display_order: 0, is_default: true } },
    { name: "hostname with port", row: { id: "port", domain: "valid.example.test:9443", display_order: 0, is_default: true } },
    { name: "missing domain", row: { id: "missing", display_order: 0, is_default: true } },
    { name: "empty domain", row: { id: "empty", domain: "", display_order: 0, is_default: true } },
    { name: "whitespace domain", row: { id: "space", domain: " \t ", display_order: 0, is_default: true } },
    { name: "numeric domain", row: { id: "number", domain: 42, display_order: 0, is_default: true } },
    { name: "URL instead of hostname", row: { id: "url", domain: "https://invalid.example.test/path", display_order: 0, is_default: true } },
    { name: "null row", row: null },
    { name: "primitive row", row: "invalid.example.test" },
    { name: "invalid ordering metadata", row: { id: "order", domain: "invalid.example.test", display_order: "zero", is_default: true } },
  ];

  test("verifier: canParse absent keeps ordinary numeric-label and punycode hosts while filtering invalid rows", async ({ page }) => {
    // Given a Safari/iOS 16-era URL API before any application code loads.
    await page.addInitScript(() => Object.defineProperty(URL, "canParse", { value: undefined, configurable: true }));
    const hosts = ["normal.example.test", "123.example.test", "xn--bcher-kva.example.test"];
    let rows: unknown[] = [...malformedRows.map(({ row }) => row), ...hosts.map((domain, display_order) => ({
      id: domain, domain, display_order, is_default: false,
    }))];
    const original = "https://legacy.example.test:9443/sub/encoded%2Ftoken?format=clash&source=fixture";
    await mockDevices(page, original);
    await page.route("**/api/v1/user/subscription-domains", (route) => route.fulfill({ json: rows }));
    // When the mixed response loads, then only valid DNS hosts remain selectable.
    await page.goto("/portal/devices");
    expect(await page.evaluate(() => typeof URL.canParse)).toBe("undefined");
    await expect(page.getByRole("tab")).toHaveText(hosts);
    for (const host of hosts) {
      await page.getByRole("tab", { name: host, exact: true }).click();
      await expect(page.getByRole("tabpanel").locator("code")).toHaveText(
        `https://${host}/sub/encoded%2Ftoken?source=fixture`,
      );
    }
    rows = malformedRows.map(({ row }) => row);
    await page.reload();
    await expect(page.locator("code")).toHaveCount(3);
    await expect(page.getByRole("tab")).toHaveCount(0);
    await expect(page.locator("code").last()).toHaveText(original);
    await expect(page.getByRole("button", { name: "Test reachability", exact: true })).toHaveCount(0);
  });

  test("verifier: explicit QR formats override format without losing route or other query parameters", async ({ page }) => {
    await mockDevices(page, "https://legacy.example.test:9443/sub/encoded%2Ftoken?format=clash&source=fixture");
    await page.goto("/portal/devices");
    await page.getByRole("button", { name: en.user.devices.showQr, exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.locator("code")).toHaveText("https://legacy.example.test:9443/sub/encoded%2Ftoken?source=fixture");
    await dialog.getByRole("tab", { name: "Clash", exact: true }).click();
    await expect(dialog.locator("code")).toHaveText("https://legacy.example.test:9443/sub/encoded%2Ftoken?format=clash&source=fixture");
  });

  for (const { name, row } of malformedRows) {
    test(`verifier: ${name} leaves no unusable tab and preserves fallback`, async ({ page }) => {
      const original = "https://legacy.example.test:9443/sub/encoded%2Ftoken?format=clash&source=fixture";
      await mockDevices(page, original);
      await page.route("**/api/v1/user/subscription-domains", (route) => route.fulfill({ json: [row] }));
      await page.goto("/portal/devices");
      await expect(page.locator("code")).toHaveCount(3);
      await expect(page.getByRole("tab")).toHaveCount(0);
      await expect(page.locator("code").last()).toHaveText(original);
      await expect(page.getByRole("button", { name: "Test reachability", exact: true })).toHaveCount(0);
    });
  }

  test("verifier: mixed malformed rows preserve valid domains and reload discards stale selection", async ({ page }) => {
    let rows: unknown[] = [...malformedRows.map(({ row }) => row), ...publicDomains];
    await page.route("**/api/v1/user/subscription-domains", (route) => route.fulfill({ json: rows }));
    await page.goto("/portal/devices");
    await expect(page.getByRole("tab")).toHaveText([
      "blocked.example.test", "timeout.example.test", "working.example.test",
    ]);
    await page.getByRole("tab", { name: "working.example.test", exact: true }).click();
    await expectSelectedDomain(page, "working.example.test");
    rows = malformedRows.map(({ row }) => row);
    await page.reload();
    await expect(page.getByRole("tab")).toHaveCount(0);
    await expect(page.locator("code").last()).toHaveText(
      new URL("/sub/fixture-route?format=clash&source=fixture", page.url()).toString(),
    );
  });

  test("browser race orders working first, bounds timeout, retries and allows manual fallback", async ({ page }) => {
    await page.clock.install();
    const requests: string[] = [];
    let retry = false;
    await page.route("https://*.example.test/**", async (route) => {
      const request = route.request();
      requests.push(request.url());
      expect(request.method()).toBe("HEAD");
      expect(request.headers()["authorization"]).toBeUndefined();
      if (retry || request.url().includes("working.example.test")) {
        await route.fulfill({ status: 200, body: "" });
      } else if (request.url().includes("blocked.example.test")) {
        await route.abort("connectionrefused");
      }
    });
    await page.goto("/portal/devices");
    await page.getByRole("button", { name: "Test reachability", exact: true }).click();
    await expect.poll(() => requests.length).toBe(3);
    await expect(page.getByText(en.user.devices.testingDomains, { exact: true })).toBeVisible();
    await page.clock.fastForward(8001);
    await expect(page.getByRole("tab")).toHaveText([
      "working.example.test ✓", "blocked.example.test ✕", "timeout.example.test ✕",
    ], { timeout: 3000 });
    await expectSelectedDomain(page, "working.example.test");
    expect(requests.sort()).toEqual([
      "https://blocked.example.test/sub/fixture-route?source=fixture",
      "https://timeout.example.test/sub/fixture-route?source=fixture",
      "https://working.example.test/sub/fixture-route?source=fixture",
    ]);
    await page.getByRole("tab", { name: "timeout.example.test ✕", exact: true }).click();
    await expectSelectedDomain(page, "timeout.example.test");
    await expect(page.getByText(en.user.devices.domainUnreachable, { exact: true })).toBeVisible();
    retry = true;
    await page.getByRole("button", { name: "Test reachability", exact: true }).click();
    await expect(page.getByRole("tab")).toHaveText([
      "blocked.example.test ✓", "timeout.example.test ✓", "working.example.test ✓",
    ]);
    await expectSelectedDomain(page, "blocked.example.test");
    await expect.poll(() => requests.length).toBe(6);
  });

  test("all failed domains stay selectable and reload clears stale reachability", async ({ page }) => {
    await page.route("https://*.example.test/**", (route) => route.abort("connectionrefused"));
    await page.goto("/portal/devices");
    await page.getByRole("button", { name: "Test reachability", exact: true }).click();
    await expect(page.getByRole("tab")).toHaveText([
      "blocked.example.test ✕", "timeout.example.test ✕", "working.example.test ✕",
    ]);
    await page.getByRole("tab", { name: "working.example.test ✕", exact: true }).click();
    await expectSelectedDomain(page, "working.example.test");
    await page.reload();
    await expect(page.getByRole("tab")).toHaveText([
      "blocked.example.test", "timeout.example.test", "working.example.test",
    ]);
    await expectSelectedDomain(page, "blocked.example.test");
  });

  for (const scenario of ["empty", "error", "malformed"] as const) {
    test(`${scenario} public pool preserves original fallback URL`, async ({ page }) => {
      await page.route("**/api/v1/user/subscription-domains", (route) => route.fulfill({
        status: scenario === "error" ? 503 : 200,
        json: scenario === "empty" ? [] : { error: "fixture" },
      }));
      await page.goto("/portal/devices");
      const expected = new URL("/sub/fixture-route?format=clash&source=fixture", page.url()).toString();
      await expect(page.locator("code")).toHaveCount(3);
      await expect(page.locator("code").last()).toHaveText(expected);
      await expect(page.getByRole("tab")).toHaveCount(0);
      await expect(page.getByRole("button", { name: "Test reachability", exact: true })).toHaveCount(0);
    });
  }

  test("interrupted probes do not carry results into remounted devices", async ({ page }) => {
    await page.route("https://*.example.test/**", () => {});
    for (let attempt = 0; attempt < 3; attempt++) {
      await page.goto("/portal/devices");
      await page.getByRole("button", { name: "Test reachability", exact: true }).click();
      await page.goto("/portal/account");
    }
    await page.goto("/portal/devices");
    await expect(page.getByRole("tab")).toHaveText([
      "blocked.example.test", "timeout.example.test", "working.example.test",
    ]);
    await expectSelectedDomain(page, "blocked.example.test");
  });

  for (const [language, messages] of Object.entries({ en, ko, zh })) {
    for (const width of [375, 1280]) {
      test(`${language} guidance and selection at ${width}px`, async ({ page }) => {
        await mockSession(page, "user", language);
        await mockDevices(page);
        await page.setViewportSize({ width, height: 900 });
        const adminRequests: string[] = [];
        page.on("request", (request) => {
          if (request.url().includes("/api/v1/admin/")) adminRequests.push(request.url());
        });
        await page.goto("/portal/devices");
        await expect(page.getByText(messages.user.devices.subscriptionDomainWarning, { exact: true })).toBeVisible();
        await expect(page.getByText(messages.user.devices.subscriptionDomainDescription, { exact: true })).toBeVisible();
        await page.getByRole("tab", { name: "working.example.test", exact: true }).click();
        await expectSelectedDomain(page, "working.example.test");
        await expect(page.getByRole("button", { name: messages.user.devices.testDomains, exact: true })).toBeVisible();
        await expect(page.getByText(messages.admin.subscriptionDomains.healthTitle, { exact: true })).toHaveCount(0);
        expect(adminRequests).toEqual([]);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      });
    }
  }
});
