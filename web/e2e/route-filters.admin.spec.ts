import { test, expect } from "@playwright/test";
import { entry, exit, chain, mockTopology } from "./managed-chain-fixtures";

test.use({ storageState: { cookies: [], origins: [] } });
test.beforeEach(async ({ page }) => { await mockTopology(page); });

test("disabled assigned routes do not turn a server into unassigned", async ({ page }) => {
  await page.route("**/api/v1/admin/node-chains", (route) => route.fulfill({ json: [{ ...chain, enabled: false, group_ids: ["access"] }] }));
  await page.goto("/admin/nodes");
  await page.getByRole("button", { name: /Subscription group assignment/ }).click();
  await page.getByRole("option", { name: "Assigned to subscription groups", exact: true }).click();
  await expect(page.getByRole("row", { name: /Managed entry/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /^Exit / })).toBeVisible();
});

for (const filter of ["entry", "exit", "node"]) {
  test(`unresolved ${filter} URL filter is visible and clearable`, async ({ page }) => {
    await page.route("**/api/v1/admin/node-chains", (route) => route.fulfill({ json: [chain] }));
    await page.goto(`/admin/node-chains?${filter}=deleted-server`);
    await expect(page.getByText("Unavailable server filter: deleted-server", { exact: true }).last()).toBeVisible();
    await page.getByRole("button", { name: "Clear filters" }).click();
    await expect(page.getByRole("row", { name: /Existing link/ })).toBeVisible();
  });
}

test("batch form remains usable in a narrow viewport", async ({ page }) => {
  await page.setViewportSize({ width: 430, height: 900 });
  await page.route("**/api/v1/admin/nodes", (route) => route.fulfill({ json: [entry, exit] }));
  await page.goto("/admin/node-chains?entry=entry");
  await page.getByRole("button", { name: "Add multiple exits" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByRole("button", { name: "Preview port allocation" })).toBeVisible();
});
