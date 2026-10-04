import { test, expect } from "@playwright/test";
import { chain, endpoint, entry, exit, mockTopology, openCreate } from "./managed-chain-fixtures";

test.use({ storageState: { cookies: [], origins: [] } });
test.beforeEach(async ({ page }) => { await mockTopology(page); });

for (const [status, label] of Object.entries({ pending: "Pending", ready: "Ready", error: "Error", conflict: "Conflict" })) {
  test(`creates without entry_host when managed DNS is ${status}`, async ({ page }) => {
    // Given a stored hostname independent of provider convergence.
    await page.route("**/api/v1/admin/nodes", (route) => route.fulfill({ json: [{ ...entry, entry_dns_status: status }, exit] }));
    const payloads: unknown[] = [];
    const groups: unknown[] = [];
    await page.route(endpoint, (route) => {
      if (route.request().method() === "POST") payloads.push(route.request().postDataJSON());
      return route.fulfill({ json: route.request().method() === "POST" ? chain : [] });
    });
    await page.route(`${endpoint}/chain/groups`, (route) => { groups.push(route.request().postDataJSON()); return route.fulfill({ json: {} }); });
    const form = await openCreate(page);
    await expect(form.getByText("managed.entry.example.test", { exact: true })).toBeVisible();
    await expect(form.getByText(label, { exact: true })).toBeVisible();
    await expect(form.getByRole("textbox", { name: "Entry hostname" })).toHaveCount(0);
    // When creating with default ports and no publication groups.
    await form.getByRole("button", { name: "Create link", exact: true }).click();
    // Then the server derives the host and zero groups remain valid.
    await expect(form).toBeHidden();
    expect(payloads).toEqual([{ name: "Managed entry → Exit", entry_node_id: "entry", entry_port: 0, exit_node_id: "exit", exit_port: 8443, transport: "tcp", priority: 0 }]);
    expect(groups).toEqual([{ group_ids: [] }]);
  });
}

test("blocks missing managed hostname without losing input or using IP", async ({ page }) => {
  // Given a registered entry without a hostname.
  await page.route("**/api/v1/admin/nodes", (route) => route.fulfill({ json: [{ ...entry, entry_hostname: null }, exit] }));
  const form = await openCreate(page);
  // When entering presentation and port values.
  await form.getByRole("textbox", { name: "Name", exact: true }).fill("Retained name");
  await form.getByRole("spinbutton", { name: "Exit listener port" }).fill("9443");
  // Then only creation is blocked and the explanation is inline.
  await expect(form.getByText(/This entry has no managed hostname/)).toBeVisible();
  await expect(form.getByRole("button", { name: "Create link", exact: true })).toBeDisabled();
  await expect(form.getByRole("textbox", { name: "Name", exact: true })).toHaveValue("Retained name");
  await expect(form.getByRole("spinbutton", { name: "Exit listener port" })).toHaveValue("9443");
});

for (const missing of [false, true]) {
  test(`explicit edit omits host even with missing hostname ${missing}`, async ({ page }) => {
    // Given a saved host different from the live managed host.
    await page.route(endpoint, (route) => route.fulfill({ json: [chain] }));
    if (missing) await page.route("**/api/v1/admin/nodes", (route) => route.fulfill({ json: [{ ...entry, entry_hostname: null }, exit] }));
    const payloads: unknown[] = [];
    await page.route(`${endpoint}/chain`, (route) => { payloads.push(route.request().postDataJSON()); return route.fulfill({ json: chain }); });
    await page.goto("/admin/node-chains");
    await page.getByRole("button", { name: "Edit link", exact: true }).click();
    const form = page.getByRole("dialog");
    await expect(form.getByText("stale.example.test", { exact: true })).toHaveCount(0);
    if (!missing) await expect(form.getByText("managed.entry.example.test", { exact: true })).toBeVisible();
    await expect(form.getByRole("spinbutton", { name: "Exit listener port" })).toBeDisabled();
    // When saving presentation changes.
    await form.getByRole("textbox", { name: "Name", exact: true }).fill("Renamed");
    await form.getByRole("button", { name: "Save", exact: true }).click();
    // Then immutable endpoint fields are absent.
    await expect(form).toBeHidden();
    expect(payloads).toEqual([{ name: "Renamed", priority: 7, enabled: true }]);
  });
}

for (const failure of ["create", "groups", "compensation"] as const) {
  test(`retains input after ${failure} failure`, async ({ page }) => {
    // Given a rejected create, group assignment, or compensation request.
    const deleted: string[] = [];
    await page.route(endpoint, (route) => route.fulfill(route.request().method() === "POST"
      ? failure === "create" ? { status: 409, json: { error: "managed Entry hostname unavailable" } } : { json: chain }
      : { json: [] }));
    await page.route(`${endpoint}/chain/groups`, (route) => route.fulfill({ status: 500, json: { error: "Group assignment failed" } }));
    await page.route(`${endpoint}/chain`, (route) => { deleted.push(route.request().method()); return route.fulfill(failure === "compensation" ? { status: 500, json: { error: "Cleanup failed" } } : { json: {} }); });
    const form = await openCreate(page);
    await form.getByRole("textbox", { name: "Name", exact: true }).fill("Keep my input");
    await form.getByRole("spinbutton", { name: "Exit listener port" }).fill("9443");
    // When submitting.
    await form.getByRole("button", { name: "Create link", exact: true }).click();
    // Then the relevant failure remains visible and input is retained.
    await expect(form.getByText(failure === "create" ? "managed Entry hostname unavailable" : failure === "groups" ? "Group assignment failed" : "Cleanup failed", { exact: true })).toBeVisible();
    await expect(form.getByRole("textbox", { name: "Name", exact: true })).toHaveValue("Keep my input");
    await expect(form.getByRole("spinbutton", { name: "Exit listener port" })).toHaveValue("9443");
    await expect(form.getByRole("button", { name: "Create link", exact: true })).toBeEnabled();
    expect(deleted).toEqual(failure === "create" ? [] : ["DELETE"]);
  });
}
