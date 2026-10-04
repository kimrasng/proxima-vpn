import { test, expect, type Page } from "@playwright/test";
import { chain, entry, exit, mockTopology } from "./managed-chain-fixtures";
import type { Plan, PlanRoutesResponse } from "../src/api/types";

const plan: Plan = {
  id: "00000000-0000-4000-8000-000000000001", name: "Route plan", duration_days: 30, max_devices: 3,
  max_concurrent: null, speed_limit: 50, node_group_id: "access",
  node_group_name: "Subscribers", is_active: true, purchasable: true,
  created_at: "2026-01-01T00:00:00Z", prices: [{ duration_days: 30, price_cents: 1000 }], features: [],
};
const saved: PlanRoutesResponse = {
  chain_ids: ["chain"], node_group_id: "access", speed_limit: 50,
  speed_enforcement: "shared_tier", warnings: [],
};
const second = { ...chain, id: "second", name: "Second route", enabled: false, health: "unknown", group_ids: [] };
const planEndpoint = `**/api/v1/admin/plans/${plan.id}`;
const routesEndpoint = `${planEndpoint}/routes`;

async function openRoutes(page: Page) {
  await page.goto("/admin/plans");
  await page.getByRole("row", { name: /Route plan/ }).getByRole("button", { name: /Manage routes|admin\.routeManagement\.manage/ }).click();
  const panel = page.locator("#plan-routes");
  await expect(panel.getByRole("row", { name: /Existing link/ })).toBeVisible();
  return panel;
}

// The locale owner may refine wording independently; the panel's region/row
// contracts and request payloads are the stable assertions in this suite.
function applyButton(page: Page) {
  return page.locator("#plan-routes").getByRole("button").last();
}

test.use({ storageState: { cookies: [], origins: [] } });
test.beforeEach(async ({ page }) => {
  await mockTopology(page);
  await page.route("**/api/v1/admin/plans", route => route.fulfill({ json: [plan] }));
  await page.route(planEndpoint, route => route.fulfill({ json: plan }));
  await page.route("**/api/v1/admin/node-chains", route => route.fulfill({ json: [{ ...chain, group_ids: ["access"] }, second] }));
  await page.route("**/api/v1/admin/nodes", route => route.fulfill({ json: [entry, { ...exit, reality_sni_status: "valid" }] }));
  await page.route(routesEndpoint, route => route.fulfill({ json: saved }));
});

test("shows saved routes, endpoint path, separate health and unenforced per-device warning", async ({ page }) => {
  const panel = await openRoutes(page);
  const row = panel.getByRole("row", { name: /Existing link/ });
  await expect(row.getByRole("checkbox")).toBeChecked();
  await expect(row).toContainText("stale.example.test:22001 → Exit:8443");
  await expect(panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox")).not.toBeChecked();
  await expect(applyButton(page)).toBeDisabled();
  await expect(panel).toContainText(/per.device|perDeviceWarning/i);
  await expect(panel).toContainText(/subscription|availabilityWarning/i);
  await expect(panel).toContainText(/shared|sharedTierSpeed/i);
});

test("applies only route IDs, preserves draft, and rebases an isolated group before plan save", async ({ page }) => {
  const assignments: unknown[] = [];
  const updates: Record<string, unknown>[] = [];
  await page.route(routesEndpoint, route => {
    if (route.request().method() === "PUT") {
      assignments.push(route.request().postDataJSON());
      return route.fulfill({ json: { ...saved, chain_ids: ["chain", "second"], node_group_id: "isolated" } });
    }
    return route.fulfill({ json: saved });
  });
  await page.route(planEndpoint, route => {
    if (route.request().method() === "PUT") updates.push(route.request().postDataJSON());
    return route.fulfill({ json: plan });
  });
  const panel = await openRoutes(page);
  await page.getByRole("textbox", { name: "Plan Name", exact: true }).fill("Unsaved route plan");
  await panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox").check();
  await applyButton(page).click();
  await expect(applyButton(page)).toBeDisabled();
  await expect(page.getByRole("textbox", { name: "Plan Name", exact: true })).toHaveValue("Unsaved route plan");
  expect(assignments).toEqual([{ chain_ids: ["chain", "second"] }]);
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.locator("#plan-routes")).toHaveCount(0);
  expect(updates).toHaveLength(1);
  expect(updates[0]).toMatchObject({ name: "Unsaved route plan", node_group_id: "isolated", speed_limit: 50 });
});

test("keeps selection and draft on failure and retries successfully", async ({ page }) => {
  let attempts = 0;
  await page.route(routesEndpoint, route => {
    if (route.request().method() !== "PUT") return route.fulfill({ json: saved });
    attempts++;
    return route.fulfill(attempts === 1
      ? { status: 409, json: { error: "Route assignment conflict" } }
      : { json: { ...saved, chain_ids: ["chain", "second"] } });
  });
  const panel = await openRoutes(page);
  await page.getByRole("textbox", { name: "Plan Name", exact: true }).fill("Keep my draft");
  await panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox").check();
  await applyButton(page).click();
  await expect(panel.getByText("Route assignment conflict", { exact: true })).toBeVisible();
  await expect(panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox")).toBeChecked();
  await expect(page.getByRole("textbox", { name: "Plan Name", exact: true })).toHaveValue("Keep my draft");
  await applyButton(page).click();
  await expect(panel.getByText("Route assignment conflict", { exact: true })).toHaveCount(0);
  await expect(applyButton(page)).toBeDisabled();
  expect(attempts).toBe(2);
});

test("blocks applying while plan limits or group are unsaved", async ({ page }) => {
  await page.route("**/api/v1/admin/node-groups", route => route.fulfill({ json: [{ id: "access", name: "Subscribers" }, { id: "other", name: "Other group" }] }));
  const panel = await openRoutes(page);
  await panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox").check();
  await page.getByRole("spinbutton", { name: "Speed Limit (Mbps)" }).fill("75");
  await expect(applyButton(page)).toBeDisabled();
  await expect(panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox")).toBeDisabled();
  await page.getByRole("spinbutton", { name: "Speed Limit (Mbps)" }).fill("50");
  await expect(applyButton(page)).toBeEnabled();
  await page.getByRole("button", { name: /Node Group Subscribers/ }).click();
  await page.getByRole("option", { name: "Other group", exact: true }).click();
  await expect(applyButton(page)).toBeDisabled();
  await expect(panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox")).toBeDisabled();
});

test("create editor gives saved-plan guidance and never fetches route assignments", async ({ page }) => {
  let routeReads = 0;
  await page.route("**/api/v1/admin/plans/*/routes", route => { routeReads++; return route.fulfill({ json: saved }); });
  await page.goto("/admin/plans");
  await page.getByRole("button", { name: "Create Plan", exact: true }).click();
  await expect(page.locator("#plan-routes")).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Plan Name", exact: true })).toBeVisible();
  await expect(page.getByText("Save this plan, then reopen the editor to assign routes.", { exact: true })).toBeVisible();
  expect(routeReads).toBe(0);
});

test("unapplied route drafts block Save and Refresh and require explicit discard on Cancel", async ({ page }) => {
  const panel = await openRoutes(page);
  await panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox").check();
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  await expect(panel.getByRole("button", { name: "Refresh routes", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Discard changes?" });
  await expect(dialog).toContainText("Your unapplied route selection changes will also be discarded.");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox")).toBeChecked();
  await panel.getByRole("button", { name: "Discard changes", exact: true }).click();
  await expect(panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox")).not.toBeChecked();
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
  await panel.getByRole("row", { name: /Second route/ }).getByRole("checkbox").check();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await dialog.getByRole("button", { name: "Discard changes", exact: true }).click();
  await expect(panel).toHaveCount(0);
});

test("route refresh rebases externally isolated group while preserving unrelated plan draft", async ({ page }) => {
  let isolated = false;
  const updates: Record<string, unknown>[] = [];
  await page.route(routesEndpoint, route => route.fulfill({ json: { ...saved, node_group_id: isolated ? "isolated" : "access" } }));
  await page.route(planEndpoint, route => {
    if (route.request().method() === "PUT") updates.push(route.request().postDataJSON());
    return route.fulfill({ json: plan });
  });
  const panel = await openRoutes(page);
  await page.getByRole("textbox", { name: "Plan Name", exact: true }).fill("Preserved after refresh");
  isolated = true;
  await panel.getByRole("button", { name: "Refresh routes", exact: true }).click();
  await expect(page.getByRole("button", { name: "Node Group isolated", exact: true })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Plan Name", exact: true })).toHaveValue("Preserved after refresh");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(panel).toHaveCount(0);
  expect(updates).toHaveLength(1);
  expect(updates[0]).toMatchObject({ name: "Preserved after refresh", node_group_id: "isolated" });
});

test("route refresh retains conflicting group draft and blocks Save until reconciled", async ({ page }) => {
  let isolated = false;
  await page.route("**/api/v1/admin/node-groups", route => route.fulfill({ json: [{ id: "access", name: "Subscribers" }, { id: "other", name: "Other group" }, { id: "isolated", name: "Isolated group" }] }));
  await page.route(routesEndpoint, route => route.fulfill({ json: { ...saved, node_group_id: isolated ? "isolated" : "access" } }));
  const panel = await openRoutes(page);
  await page.getByRole("button", { name: "Node Group Subscribers", exact: true }).click();
  await page.getByRole("option", { name: "Other group", exact: true }).click();
  isolated = true;
  await panel.getByRole("button", { name: "Refresh routes", exact: true }).click();
  await expect(page.getByRole("button", { name: "Node Group Other group", exact: true })).toBeVisible();
  await expect(page.getByText(/The saved route group changed while you were editing/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Node Group Other group", exact: true }).click();
  await page.getByRole("option", { name: "Isolated group", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
});
