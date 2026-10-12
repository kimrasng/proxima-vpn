import { test, expect } from "@playwright/test";
import { chooseLinkAction } from "./managed-chain-fixtures";

test("administrator connects an entry node to an exit and manages its link", async ({ page, request }) => {
  // This is the one real-backend admin spec and it makes ~40 API calls. The
  // api-server allows 100 requests per minute per IP and the specs before it
  // share that budget, so wait for a fresh window instead of risking a 429
  // mid-flow (the UI then renders without the data it just created). The
  // limiter uses a fixed window, so a 60s pause always starts a new one.
  test.setTimeout(150_000);
  await page.waitForTimeout(60_000);
  const unique = Math.random().toString(36).slice(2, 10);
  const token = await page.goto("/admin/dashboard").then(async () => page.evaluate(() => localStorage.getItem("proxima_admin_token")));
  expect(token).toBeTruthy();
  const headers = { Authorization: `Bearer ${token}` };
  const api = (path: string) => `/api/v1/admin/${path}`;
  const createNode = async (role: string) => {
    const response = await request.post(api("nodes/token"), { headers, data: { role, name: `ui-${role}-${unique}`, port: 8443 } });
    expect(response.ok()).toBeTruthy();
    const pending: { token: string } = await response.json();
    const registered = await request.post("/api/v1/nodes/register", { data: { reg_token: pending.token, ip: role === "relay" ? "203.0.113.21" : "203.0.113.22", port: 8443, name: `ui-${role}-${unique}` } });
    expect(registered.ok()).toBeTruthy();
    const node: { node_id: string } = await registered.json();
    return node.node_id;
  };
  const relay = await createNode("relay");
  const exit = await createNode("exit");
  const newGroup = async (name: string) => {
    const response = await request.post(api("node-groups/"), { headers, data: { name } });
    expect(response.ok()).toBeTruthy();
    const result: { id: string } = await response.json();
    return result.id;
  };
  const access = await newGroup(`ui-access-${unique}`);
  const directMembership = await request.put(api(`node-groups/${access}/nodes`), { headers, data: { node_ids: [exit] } });
  expect(directMembership.ok()).toBeTruthy();

  try {
    const entryResponse = await request.get(api(`nodes/${relay}`), { headers });
    expect(entryResponse.ok()).toBeTruthy();
    const managedEntry: { entry_hostname: string | null } = await entryResponse.json();
    // Relays only get a managed Entry hostname when the panel has managed Entry
    // DNS configured. browser-e2e.yml points it at e2e/cloudflare-stub.mjs; a
    // local run without that configuration skips rather than fails.
    test.skip(!managedEntry.entry_hostname && !process.env.CI, "panel has no managed Entry DNS configured");
    expect(managedEntry.entry_hostname, "CI must configure managed Entry DNS (cloudflare-stub.mjs)").toBeTruthy();
    const entryHostname = managedEntry.entry_hostname!;
    await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
    await page.goto("/admin/nodes");
    await page.getByRole("tab", { name: "All servers" }).click();
    await expect(page.getByRole("row", { name: new RegExp(`ui-relay-${unique}`) })).toContainText("Entry relay");

    const groupDetailRequests: string[] = [];
    page.on("request", (req) => {
      if (req.method() === "GET" && /\/api\/v1\/admin\/node-groups\/[^/?]+(?:\?|$)/.test(req.url())) {
        groupDetailRequests.push(req.url());
      }
    });
    await page.goto("/admin/node-chains");
    await expect(page.getByRole("button", { name: "Create link" })).toBeVisible();
    expect(groupDetailRequests).toEqual([]);
     await page.getByRole("button", { name: "Create link" }).click();
     const dialog = page.getByRole("dialog", { name: "Create link" });
    await dialog.getByRole("button", { name: /Entry node Choose an entry node/ }).click();
    await page.getByRole("option", { name: `ui-relay-${unique}` }).click();
    await dialog.getByRole("button", { name: /Exit node Choose an exit/ }).click();
    await page.getByRole("option", { name: `ui-exit-${unique}` }).click();
    await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(`ui-relay-${unique} → ui-exit-${unique}`);
    await expect(dialog.getByText(entryHostname, { exact: true })).toBeVisible();
    await expect(dialog.getByRole("textbox", { name: "Entry hostname" })).toHaveCount(0);
    await expect(dialog.getByRole("spinbutton", { name: "Exit listener port" })).toHaveValue("8443");
    await dialog.getByRole("textbox", { name: "Name", exact: true }).fill(`ui-chain-${unique}`);
    await dialog.getByRole("button", { name: /Access groups/ }).click();
    await page.getByRole("option", { name: `ui-access-${unique}` }).click();
     await dialog.getByRole("button", { name: "Create link" }).click();
    const row = page.getByRole("row", { name: new RegExp(`ui-chain-${unique}`) });
    await expect(row).toContainText(entryHostname);
    await expect(row).toContainText(`ui-relay-${unique}`);
    // The list shows the plans a route serves, not raw group names; the
    // group chosen in the dialog must be the one stored on the route.
    const chains: { name: string; group_ids: string[] }[] = await (await request.get(api("node-chains"), { headers })).json();
    expect(chains.find((chain) => chain.name === `ui-chain-${unique}`)?.group_ids).toEqual([access]);
    await page.goto("/admin/nodes");
    await expect(page.getByRole("tab", { name: "All servers" })).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(`ui-relay-${unique}`) })).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(`ui-exit-${unique}`) })).toBeVisible();
    await page.getByRole("tab", { name: "Entry servers" }).click();
    await expect(page.getByRole("row", { name: new RegExp(`ui-relay-${unique}`) })).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(`ui-exit-${unique}`) })).toHaveCount(0);
    await page.getByRole("tab", { name: "Exit servers" }).click();
    await expect(page.getByRole("row", { name: new RegExp(`ui-exit-${unique}`) })).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(`ui-relay-${unique}`) })).toHaveCount(0);
    await page.getByRole("tab", { name: "All servers" }).click();
    await expect(page.getByRole("row", { name: new RegExp(`ui-exit-${unique}`) })).toBeVisible();
    await page.getByRole("tab", { name: "All servers" }).click();
    await expect(page.getByRole("row", { name: new RegExp(`ui-relay-${unique}`) })).toBeVisible();
    await page.goto("/admin/node-chains");
    const managedRow = page.getByRole("row", { name: new RegExp(`ui-chain-${unique}`) });
    await chooseLinkAction(page, managedRow, "Disable");
    await expect(managedRow).toContainText("Disabled");
    await chooseLinkAction(page, managedRow, "Enable");
    await expect(managedRow).toContainText("Enabled");
    await chooseLinkAction(page, managedRow, "Edit link");
     await page.getByRole("dialog", { name: "Edit link" }).getByRole("button", { name: "Delete link" }).click();
    await page.getByRole("button", { name: "Confirm deletion" }).click();
    await expect(managedRow).toHaveCount(0);
    const retainedEntry = await request.get(api(`nodes/${relay}`), { headers });
    expect(retainedEntry.ok()).toBeTruthy();
    expect(await retainedEntry.json()).toMatchObject({ entry_hostname: entryHostname });
  } finally {
    await request.delete(api(`node-groups/${access}`), { headers });
    await request.delete(api(`nodes/${relay}`), { headers });
    await request.delete(api(`nodes/${exit}`), { headers });
  }
});
