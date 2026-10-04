import { test, expect, type Page } from "@playwright/test";
import { mockSession } from "./subscription-domain-fixtures";

const endpoint = "**/api/v1/admin/nodes";
const node = {
  id: "exit-fixture", name: "Exit fixture", country: "US", region: "Test", ip: "192.0.2.1",
  port: 443, status: "online", role: "both", publish_direct: true, traffic_multiplier: 1,
  shaping_ok: null, shaping_tiers: null, shaping_error: null, xray_too_old: false,
  xray_minimum: "", xray_version_warning: "", online_devices: 0, capacity: 0,
  os_family: "debian", max_concurrent_conns: 0, firewall_preset: "standard",
  firewall_ports: "443", created_at: "2026-01-01T00:00:00Z",
  entry_hostname: "entry.example.test", entry_dns_status: "ready", entry_dns_error_code: null,
  reality_client_sni: "old.example.test", reality_sni_status: "conflict",
  reality_sni_error_code: "listener_mismatch",
} as const;

async function showTechnicalColumns(page: Page) {
  await expect(page.getByRole("columnheader", { name: "Managed Entry DNS", exact: true })).toHaveCount(0);
  await expect(page.getByRole("columnheader", { name: "Canonical Reality SNI", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Preferences", exact: true }).click();
  const preferences = page.getByRole("dialog", { name: "Preferences", exact: true });
  for (const name of ["Managed Entry DNS", "Canonical Reality SNI"]) {
    const checkbox = preferences.getByRole("checkbox", { name, exact: true });
    await expect(checkbox).not.toBeChecked();
    await checkbox.check();
  }
  await preferences.getByRole("button", { name: "Save", exact: true }).click();
  await expect(preferences).toBeHidden();
}

async function openEditor(page: Page) {
  await page.goto("/admin/nodes");
  await page.getByRole("tab", { name: "All servers", exact: true }).click();
  await page.locator("tbody tr").getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: "Edit Reality SNI", exact: true }).click();
  return page.getByRole("dialog", { name: "Edit Reality SNI", exact: true });
}

test.describe("PHS-030 node endpoints", () => {
  test.use({ storageState: { cookies: [], origins: [] } });
  test.beforeEach(async ({ page }) => {
    await mockSession(page, "admin");
    await page.route("**/src/api/**", (route) => route.continue());
    await page.route(endpoint, (route) => route.fulfill({ json: [node] }));
    await page.route("**/api/v1/admin/node-chains", (route) => route.fulfill({ json: [] }));
  });

  for (const [status, label] of Object.entries({ pending: "Pending", ready: "Ready", conflict: "Conflict", error: "Error", deleting: "Deleting", deleted: "Deleted" })) {
    test(`shows read-only DNS ${status} and canonical SNI`, async ({ page }) => {
      // Given a sanitized managed DNS response.
      await page.route(endpoint, (route) => route.fulfill({ json: [{ ...node, entry_dns_status: status, entry_dns_error_code: "dns_mismatch" }] }));
      // When the operator opens all servers and opts into technical columns.
      await page.goto("/admin/nodes");
      await page.getByRole("tab", { name: "All servers", exact: true }).click();
      await showTechnicalColumns(page);
      // Then endpoint values and distinct states are read-only.
      const row = page.locator("tbody tr");
      await expect(row.getByText("entry.example.test", { exact: true })).toBeVisible();
      await expect(row.getByText(label, { exact: true }).first()).toBeVisible();
      await expect(row.getByText("dns_mismatch", { exact: true })).toBeVisible();
      await expect(row.getByText("old.example.test", { exact: true })).toBeVisible();
      await expect(row.getByText("listener_mismatch", { exact: true })).toBeVisible();
      await expect(row.getByRole("textbox")).toHaveCount(0);
    });
  }

  for (const scenario of ["null", "relay", "pending", "not_applicable"] as const) {
    test(`does not offer SNI editing for ${scenario}`, async ({ page }) => {
      // Given a node without meaningful editable Reality state.
      const fixture = {
        ...node,
        ...(scenario === "null" ? { entry_hostname: null, entry_dns_status: null, entry_dns_error_code: null, reality_client_sni: null, reality_sni_status: null, reality_sni_error_code: null } : {}),
        ...(scenario === "relay" ? { role: "relay" } : {}),
        ...(scenario === "pending" ? { status: "pending" } : {}),
        ...(scenario === "not_applicable" ? { reality_sni_status: "not_applicable" } : {}),
      };
      await page.route(endpoint, (route) => route.fulfill({ json: [fixture] }));
      // When opening its actions.
      await page.goto("/admin/nodes");
      await page.getByRole("tab", { name: "All servers", exact: true }).click();
      const row = page.locator("tbody tr");
      if (scenario === "null") {
        await showTechnicalColumns(page);
        await expect(row.getByText("Unconfigured", { exact: true })).toHaveCount(2);
      }
      await row.getByRole("button", { name: "Actions", exact: true }).click();
      // Then the dedicated action is unavailable.
      await expect(page.getByRole("menuitem", { name: "Edit Reality SNI", exact: true })).toHaveCount(0);
    });
  }

  for (const width of [375, 768, 1280]) {
    test(`saves only operator SNI and refreshes at ${width}px`, async ({ page }, testInfo) => {
      // Given an editable conflict, with a server-normalized result.
      await page.setViewportSize({ width, height: 900 });
      let saved = false;
      let mutations = 0;
      await page.route(endpoint, (route) => route.fulfill({ json: [{ ...node, ...(saved ? { reality_client_sni: "new.example.test", reality_sni_status: "valid", reality_sni_error_code: null } : {}) }] }));
      await page.route(`${endpoint}/${node.id}`, async (route) => {
        expect(route.request().method()).toBe("PUT");
        expect(route.request().postDataJSON()).toEqual({ reality_client_sni: "NEW.example.test" });
        mutations++;
        saved = true;
        await route.fulfill({ json: node });
      });
      const form = await openEditor(page);
      // When saving the explicit operator input.
      await form.getByRole("textbox", { name: "Reality client SNI", exact: true }).fill("NEW.example.test");
      await page.screenshot({
        path: testInfo.outputPath(`sni-editor-${width}.png`),
        animations: "disabled",
      });
      await form.getByRole("button", { name: "Save", exact: true }).click();
      // Then a fresh GET, not the mutation response, supplies the canonical state.
      await expect(form).toBeHidden();
      await expect(page.getByText("Reality SNI saved.", { exact: true })).toBeVisible();
      await showTechnicalColumns(page);
      await expect(page.locator("tbody tr").getByText("new.example.test", { exact: true })).toBeVisible();
      await expect(page.locator("tbody tr").getByText("Valid", { exact: true })).toBeVisible();
      expect(mutations).toBe(1);
    });
  }

  for (const status of [400, 409, 500]) {
    test(`retains input after HTTP ${status}`, async ({ page }) => {
      // Given an API rejection.
      await page.route(`${endpoint}/${node.id}`, (route) => route.fulfill({ status, json: { error: status === 409 ? "Reality SNI is incompatible with node listeners" : "invalid reality_client_sni" } }));
      const form = await openEditor(page);
      const input = form.getByRole("textbox", { name: "Reality client SNI", exact: true });
      await input.fill("candidate.example.test");
      // When saving.
      await form.getByRole("button", { name: "Save", exact: true }).click();
      // Then the form remains editable and reports actionable failure.
      await expect(form.getByText(status === 400 ? /^Enter a hostname without/ : status === 409 ? /^This SNI is incompatible/ : /^Failed to save/)).toBeVisible();
      await expect(input).toHaveValue("candidate.example.test");
      await expect(form.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
      await expect(page.getByText("Reality SNI saved.", { exact: true })).toHaveCount(0);
    });
  }

  test("does not silently trim surrounding whitespace", async ({ page }) => {
    // Given prohibited surrounding whitespace.
    let mutations = 0;
    await page.route(`${endpoint}/${node.id}`, (route) => { mutations++; return route.fulfill({ json: node }); });
    const form = await openEditor(page);
    const input = form.getByRole("textbox", { name: "Reality client SNI", exact: true });
    await input.fill(" candidate.example.test ");
    // When saving.
    await form.getByRole("button", { name: "Save", exact: true }).click();
    // Then validation preserves the raw value and prevents mutation.
    await expect(form.getByText(/^Enter a hostname without/)).toBeVisible();
    await expect(input).toHaveValue(" candidate.example.test ");
    expect(mutations).toBe(0);
  });

  test("blocks duplicate submission and dismissal while saving", async ({ page }) => {
    // Given a request held at the API boundary.
    let mutations = 0;
    let release = () => {};
    const responseReady = new Promise<void>((resolve) => { release = resolve; });
    await page.route(`${endpoint}/${node.id}`, async (route) => {
      mutations++;
      await responseReady;
      await route.fulfill({ json: node });
    });
    const form = await openEditor(page);
    // When saving and attempting to dismiss before the response.
    await form.getByRole("button", { name: "Save", exact: true }).click();
    try {
      await expect(form.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
      await expect(form.getByRole("button", { name: "Cancel", exact: true })).toBeDisabled();
      await page.keyboard.press("Escape");
      // Then the pending operation stays visible and unique.
      await expect(form).toBeVisible();
      expect(mutations).toBe(1);
    } finally {
      release();
    }
    await expect(form).toBeHidden();
  });

  test("keeps endpoint fields out of the general edit payload", async ({ page }) => {
    // Given an existing registered node with endpoint state.
    const payloads: unknown[] = [];
    await page.route(`${endpoint}/${node.id}`, async (route) => {
      if (route.request().method() === "PUT") payloads.push(route.request().postDataJSON());
      await route.fulfill({ json: node });
    });
    await page.goto("/admin/nodes");
    await page.getByRole("tab", { name: "All servers", exact: true }).click();
    await page.locator("tbody tr").getByRole("button", { name: "Actions", exact: true }).click();
    await page.getByRole("menuitem", { name: "Edit", exact: true }).click();
    const form = page.getByRole("dialog");
    // When saving through the unchanged general editor.
    await form.getByRole("button", { name: "Save", exact: true }).click();
    // Then only the existing general fields are sent.
    await expect(form).toBeHidden();
    expect(payloads).toEqual([{
      name: node.name, country: node.country, region: node.region, traffic_multiplier: 1,
      max_concurrent_conns: 0, role: "both", publish_direct: true,
      firewall_preset: "standard", labels: {}, os_family: "debian",
    }]);
  });
});
