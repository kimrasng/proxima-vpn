import { test, expect } from "@playwright/test";
import { entry, exit, chain, mockTopology } from "./managed-chain-fixtures";
import type { CreateNodeChainBatchRequest } from "../src/api/types";

test.use({ storageState: { cookies: [], origins: [] } });
test.beforeEach(async ({ page }) => { await mockTopology(page); });

test("server inventory includes dual-role servers and preserves role in URL", async ({ page }) => {
  await page.route("**/api/v1/admin/nodes", (route) => route.fulfill({ json: [entry, exit, { ...exit, id: "both", name: "Combined", role: "both" }] }));
  await page.route("**/api/v1/admin/node-chains", (route) => route.fulfill({ json: [{ ...chain, exit_node_id: "both", exit_node_name: "Combined" }] }));
  await page.goto("/admin/nodes");
  await expect(page.getByRole("tab", { name: "All servers" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Operating nodes" })).toHaveCount(0);
  await page.getByRole("tab", { name: "Entry servers" }).click();
  await expect(page.getByRole("row", { name: /Combined/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /Managed entry/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /^Exit / })).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("tab", { name: "Entry servers" })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: "Exit servers" }).click();
  await expect(page.getByRole("row", { name: /Combined/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /Managed entry/ })).toHaveCount(0);
  await page.getByRole("row", { name: /Combined/ }).getByRole("button", { name: "1 routes" }).click();
  await expect(page).toHaveURL(/node=both/);
  await expect(page.getByRole("row", { name: /Existing link/ })).toBeVisible();
});

test("previews multiple exit ports then commits exact chosen allocation", async ({ page }) => {
  const second = { ...exit, id: "exit-2", name: "Second exit", port: 9443 };
  await page.route("**/api/v1/admin/nodes", (route) => route.fulfill({ json: [entry, exit, second] }));
  const payloads: CreateNodeChainBatchRequest[] = [];
  await page.route("**/api/v1/admin/node-chains/batch", (route) => {
    const payload: CreateNodeChainBatchRequest = route.request().postDataJSON();
    payloads.push(payload);
    return route.fulfill({ json: { preview: payload.preview, routes: payload.routes.map((row, index) => ({ ...chain, ...row, id: `created-${index}`, entry_port: 24443 + index })) } });
  });
  await page.goto("/admin/node-chains?entry=entry");
  await page.getByRole("button", { name: "Add multiple exits" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: /Exit servers to connect/ }).click();
  await page.getByRole("option", { name: /^Exit KR/ }).click();
  await page.getByRole("option", { name: /^Second exit KR/ }).click();
  await dialog.getByRole("heading", { level: 2 }).click();
  await dialog.getByRole("button", { name: "Preview port allocation" }).click();
  await expect(dialog.getByText(/managed.entry.example.test:24443/)).toBeVisible();
  await expect(dialog.getByText(/managed.entry.example.test:24444/)).toBeVisible();
  expect(payloads[0].routes.map((row) => row.entry_port)).toEqual([0, 0]);
  await dialog.getByRole("button", { name: "Save route batch" }).click();
  await expect(dialog).toBeHidden();
  expect(payloads[1].preview).toBe(false);
  expect(payloads[1].routes.map((row) => row.entry_port)).toEqual([24443, 24444]);
});

test("port conflicts keep form values and discard outdated preview", async ({ page }) => {
  await page.route("**/api/v1/admin/node-chains/batch", (route) => route.fulfill({ status: 409, json: { error: "entry port overlaps an existing chain" } }));
  await page.goto("/admin/node-chains?entry=entry");
  await page.getByRole("button", { name: "Add multiple exits" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: /Exit servers to connect/ }).click();
  await page.getByRole("option", { name: /^Exit KR/ }).click();
  await dialog.getByRole("heading", { level: 2 }).click();
  await dialog.getByRole("spinbutton", { name: "Entry port", exact: true }).fill("24443");
  await dialog.getByRole("button", { name: "Preview port allocation" }).click();
  await expect(dialog.getByText("entry port overlaps an existing chain")).toBeVisible();
  await expect(dialog.getByRole("spinbutton", { name: "Entry port", exact: true })).toHaveValue("24443");
});

test("direct routes are visible without pretending application was acknowledged", async ({ page }) => {
  await page.route("**/api/v1/admin/node-chains", (route) => route.fulfill({ json: [{ ...chain, entry_node_id: undefined, entry_node_name: undefined, entry_port: undefined, entry_host: "", group_ids: ["access"], health: "unknown" }] }));
  await page.route("**/api/v1/admin/nodes", (route) => route.fulfill({ json: [entry, { ...exit, publish_direct: true, reality_sni_status: "valid" }] }));
  await page.goto("/admin/node-chains");
  await expect(page.getByRole("row", { name: /Existing link/ })).toContainText("Direct connection");
  await expect(page.getByText("Application acknowledgment unavailable")).toBeVisible();
});
