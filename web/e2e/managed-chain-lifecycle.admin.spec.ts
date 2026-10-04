import { test, expect } from "@playwright/test";
import { chain, endpoint, mockTopology, openCreate } from "./managed-chain-fixtures";

test.use({ storageState: { cookies: [], origins: [] } });
test.beforeEach(async ({ page }) => { await mockTopology(page); });

test("legacy pools remain ordered, visible, editable and toggleable", async ({ page }) => {
  // Given legacy and explicit links in server order.
  const legacy = { ...chain, id: "legacy", name: "Legacy link", entry_node_id: undefined, entry_node_name: undefined, relay_pool_id: "pool", relay_pool_name: "Old pool", entry_host: "pool.example.test" };
  let enabled = true;
  const payloads: unknown[] = [];
  await page.route(endpoint, (route) => route.fulfill({ json: [{ ...legacy, enabled }, chain] }));
  await page.route(`${endpoint}/legacy`, (route) => {
    const body: unknown = route.request().postDataJSON();
    payloads.push(body);
    if (typeof body === "object" && body !== null && "enabled" in body && typeof body.enabled === "boolean") enabled = body.enabled;
    return route.fulfill({ json: legacy });
  });
  await page.goto("/admin/node-chains");
  const rows = page.locator("tbody tr");
  await expect(rows.nth(0)).toContainText("Old pool");
  await expect(rows.nth(1)).toContainText("Existing link");
  await rows.nth(0).getByRole("button", { name: "Disable", exact: true }).click();
  await expect(rows.nth(0)).toContainText("Disabled");
  await rows.nth(0).getByRole("button", { name: "Enable", exact: true }).click();
  await expect(rows.nth(0)).toContainText("Enabled");
  await rows.nth(0).getByRole("button", { name: "Edit link" }).click();
  const form = page.getByRole("dialog");
  // When changing only supported legacy fields.
  await expect(form.getByText(/This existing link uses legacy relay pool Old pool/)).toBeVisible();
  await form.getByRole("textbox", { name: "Entry hostname" }).fill("updated.pool.example.test");
  await form.getByRole("button", { name: "Save", exact: true }).click();
  // Then the pool is not converted and host editing remains supported.
  await expect(form).toBeHidden();
  expect(payloads).toEqual([{ enabled: false }, { enabled: true }, { name: "Legacy link", entry_host: "updated.pool.example.test", priority: 7, enabled: true }]);
});

test("deletion removes only the link and leaves managed entry available", async ({ page }) => {
  // Given a link and its independently managed entry.
  let deleted = false;
  const mutations: string[] = [];
  page.on("request", (request) => { if (["DELETE", "POST", "PATCH", "PUT"].includes(request.method())) mutations.push(`${request.method()} ${new URL(request.url()).pathname}`); });
  await page.route(endpoint, (route) => route.fulfill({ json: deleted ? [] : [chain] }));
  await page.route(`${endpoint}/chain`, (route) => { deleted = true; return route.fulfill({ json: {} }); });
  await page.goto("/admin/node-chains");
  await page.getByRole("button", { name: "Edit link" }).click();
  // When confirming deletion.
  await page.getByRole("dialog").getByRole("button", { name: "Delete link", exact: true }).click();
  await page.getByRole("button", { name: "Confirm deletion", exact: true }).click();
  // Then no DNS/node mutation is sent and the entry is still selectable.
  await expect(page.getByRole("dialog")).toBeHidden();
  expect(mutations).toEqual(["DELETE /api/v1/admin/node-chains/chain"]);
  const form = await openCreate(page);
  await expect(form.getByText("managed.entry.example.test", { exact: true })).toBeVisible();
});

for (const transport of ["udp", "tcp_udp"] as const) {
  test(`preserves explicit ports, priority and groups for ${transport}`, async ({ page }) => {
    // Given an unpublished new route.
    const payloads: unknown[] = [];
    const groups: unknown[] = [];
    await page.route(endpoint, (route) => {
      if (route.request().method() === "POST") payloads.push(route.request().postDataJSON());
      return route.fulfill({ json: route.request().method() === "POST" ? chain : [] });
    });
    await page.route(`${endpoint}/chain/groups`, (route) => { groups.push(route.request().postDataJSON()); return route.fulfill({ json: {} }); });
    const form = await openCreate(page);
    // When configuring advanced transport and publication.
    await form.getByRole("spinbutton", { name: "Exit listener port" }).fill("9443");
    await form.getByRole("button", { name: "Access groups" }).click();
    await page.getByRole("option", { name: "Subscribers" }).click();
    await form.getByRole("button", { name: "Advanced settings" }).click();
    await form.getByRole("spinbutton", { name: "Entry port", exact: true }).fill("23001");
    await form.getByRole("spinbutton", { name: "Priority", exact: true }).fill("9");
    await form.getByRole("button", { name: /Transport TCP/ }).click();
    await page.getByRole("option", { name: transport === "udp" ? "UDP" : "TCP and UDP", exact: true }).click();
    await form.getByRole("button", { name: "Create link", exact: true }).click();
    // Then only the host is derived by the server.
    await expect(form).toBeHidden();
    expect(payloads).toEqual([{ name: "Managed entry → Exit", entry_node_id: "entry", entry_port: 23001, exit_node_id: "exit", exit_port: 9443, transport, priority: 9 }]);
    expect(groups).toEqual([{ group_ids: ["access"] }]);
  });
}

for (const width of [375, 768, 1280]) {
  for (const colorScheme of ["light", "dark"] as const) {
    test(`managed form renders at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      // Given Cloudscape's responsive modal and system color preference.
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ colorScheme });
      // When inspecting the populated form and expanded controls.
      const form = await openCreate(page);
      await expect(form.getByText("managed.entry.example.test", { exact: true })).toBeVisible();
      await expect(page.locator("body")).toHaveClass(colorScheme === "dark" ? /awsui-dark-mode/ : /^(?!.*awsui-dark-mode).*$/);
      await page.screenshot({
        path: testInfo.outputPath(`managed-${width}-${colorScheme}.png`),
        animations: "disabled",
      });
      await form.getByRole("button", { name: "Advanced settings" }).click();
      await form.getByRole("heading", { name: "Create link" }).scrollIntoViewIfNeeded();
      await page.screenshot({
        path: testInfo.outputPath(`advanced-top-${width}-${colorScheme}.png`),
        animations: "disabled",
      });
      await form.getByRole("spinbutton", { name: "Priority", exact: true }).focus();
      // Then all controls stay inside the viewport without horizontal overflow.
      const overflow = await form.locator(".node-chain-form").evaluate((element) => {
        const bounds = element.getBoundingClientRect();
        return [...element.querySelectorAll("input, button")].filter((control) => {
          const rect = control.getBoundingClientRect();
          return rect.width > 0 && (rect.left < bounds.left || rect.right > bounds.right);
        }).map((control) => control.outerHTML);
      });
      expect(overflow).toEqual([]);
      await page.screenshot({
        path: testInfo.outputPath(`advanced-controls-${width}-${colorScheme}.png`),
        animations: "disabled",
      });
    });
  }
}
