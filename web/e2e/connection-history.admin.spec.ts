import { expect, test, type Page } from "@playwright/test";
import type { ConnectionLoggingControl } from "../src/api/types";
import { mockSession } from "./subscription-domain-fixtures";

const nodeA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const nodeB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const epoch = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
const boot = "11111111-1111-4111-8111-111111111111";
const userId = "dddddddd-dddd-4ddd-8ddd-dddddddddddd";
const nodes = [nodeA, nodeB].map((id, index) => ({
  id, name: index === 0 ? "nodeA" : "nodeB", status: "online", role: "exit",
  country: "US", region: "New York", ip: `192.0.2.${index + 1}`, port: 443,
  traffic_multiplier: 1, cpu_usage: 10, memory_usage: 20, disk_usage: 30,
  online_devices: 0, capacity: 10, os_family: "debian", publish_direct: true,
  max_concurrent_conns: 0, firewall_preset: "standard", firewall_ports: "443",
  created_at: "2026-11-06T00:00:00Z", shaping_ok: true, shaping_tiers: 1,
  shaping_error: null, xray_too_old: false, xray_minimum: "", xray_version_warning: "",
}));
const off = {
  enabled: false, desired_revision: "0", applied_revision: "0", applied_enabled: false,
  collection_epoch: null, applied_collection_epoch: null, state: "off",
  status_at: "2026-11-06T12:00:00Z", last_config_fetch_at: null, applied_at: null,
  shared_identity: false, queue_bytes: "0", dropped_events: "0", error_code: "",
  producers: [],
} satisfies Omit<ConnectionLoggingControl, "node_id" | "node_name">;
const event = {
  schema_version: 1, event_id: `${boot}:3`, observer_boot_id: boot, producer_id: "xray",
  sequence: "3", runtime: "xray", runtime_build: "synthetic-ui-fixture",
  config_revision: "1", collection_epoch: epoch, event_time: "2026-11-06T12:00:00.020000000Z",
  monotonic_ns: "20000000", kind: "flow_update", tunnel_id: `${boot}:t:1`,
  flow_id: `${boot}:f:1`, protocol: "shadowsocks", inbound_tag: "shared-ss",
  auth: { kind: "shared", credential_ref: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", binding_revision: "1" },
  peer: { ip: "192.0.2.10", port: 50000 },
  destination: { requested_host: "example.test", requested_port: 443, transport: "tcp", effective_host: null, observed_ip: null, observed_port: null },
  bytes: { layer: "proxy_payload_observed", upload: "18446744073709551615", download: "456", final: false },
  lifecycle: { scope: "proxy_flow", started_at: null, last_observed_at: "2026-11-06T12:00:00.020000000Z", ended_at: null, partial_start: true, end_reason: null, end_semantics: null },
  quality: { identity: "unknown", parent: "exact", destination: "exact", bytes: "exact", start: "unknown", end: "unknown", completeness: "unknown" },
  boundary: null, gap: null,
};
const gap = {
  ...event, event_id: `${boot}:4`, sequence: "4", kind: "collection_gap", tunnel_id: null, flow_id: null,
  protocol: null, inbound_tag: null, peer: null, destination: null,
  auth: { kind: "unknown", credential_ref: null, binding_revision: null },
  bytes: { layer: "none", upload: null, download: null, final: false },
  lifecycle: { scope: "collection", started_at: null, last_observed_at: null, ended_at: null, partial_start: false, end_reason: null, end_semantics: null },
  quality: { identity: "unknown", parent: "unknown", destination: "unknown", bytes: "unknown", start: "unknown", end: "unknown", completeness: "unknown" },
  gap: { producer_id: "xray", observer_boot_id: boot, first_sequence: null, last_sequence: null, from_time: "2026-11-06T12:00:00.020000000Z", to_time: null, lost_events: null, reason: "producer_lost" },
};
const row = {
  node_id: nodeA, node_name: "nodeA", user_id: null, device_id: null,
  user_snapshot: null, device_snapshot: null, first_observed_at: "2026-11-06T12:00:00Z",
  observation_status: "interrupted", event, gap_coverage: [gap],
};
const totals = [
  { layer: "proxy_payload_observed", upload: "36893488147419103230", download: "456", complete_intervals: 1, partial_intervals: 1, boundary_policy: "complete_observed_intervals_only" },
  { layer: "inner_ip_observed", upload: "311", download: "419", complete_intervals: 1, partial_intervals: 0, boundary_policy: "complete_observed_intervals_only" },
];

async function fixture(page: Page) {
  await mockSession(page, "admin");
  const controls = new Map<string, ConnectionLoggingControl>(nodes.map(node => [node.id, { ...off, node_id: node.id, node_name: node.name }]));
  await page.route("**/api/v1/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    let data: unknown = [];
    if (path.endsWith("/stats/alerts")) data = { items: [], node_issues: [], total: 0 };
    else if (path.endsWith("/stats")) data = { total_users: 0, active_users: 0, online_users: 0, total_nodes: 2, online_nodes: 2, total_traffic_today: 0, upload_today: 0, download_today: 0, total_traffic_month: 0, active_alerts: 0, deltas: { available: false } };
    else if (path.endsWith("/connection-logging")) data = controls.get(path.split("/")[5] ?? "");
    else if (path === "/api/v1/admin/nodes") data = nodes;
    else if (nodes.some(n => path === `/api/v1/admin/nodes/${n.id}`)) data = nodes.find(n => path.endsWith(n.id));
    else if (path.endsWith("/connection-history/aggregate")) data = totals;
    else if (/\/connection-history\/(flows|sessions)\//.test(path)) data = row;
    else if (/\/connection-history\/(flows|sessions)$/.test(path)) data = { items: url.searchParams.has("cursor") ? [] : [row], next_cursor: url.searchParams.has("cursor") ? null : "opaque+/=" };
    else if (path === `/api/v1/admin/users/${userId}`) data = { id: userId, email: "history@example.test", name: "History fixture", status: "active", is_active: true, traffic_used: 0, created_at: "2026-01-01T00:00:00Z", devices: [] };
    else if (path.endsWith("/traffic")) data = { traffic_used: 0, traffic_limit: null, percentage: 0, unlimited: true, window: "month", by_node: [] };
    else if (path.endsWith("/client-addresses")) data = { complete: true, addresses: [], count: 0 };
    else if (path.endsWith("/tunnel-sessions")) data = { complete: true, count: 0, enforced: false };
    else if (path.endsWith("/events")) data = { items: [], total: 0 };
    else if (path.endsWith("/event-filters")) data = { event_types: [], severities: [] };
    await route.fulfill({ json: data });
  });
  return controls;
}

function loggingTable(page: Page) {
  return page.getByRole("table").filter({ has: page.getByRole("columnheader", { name: "Desired · revision", exact: true }) });
}

async function openHistory(page: Page) {
  await page.goto("/admin/connections");
  await page.getByRole("tab", { name: "Connection history", exact: true }).click();
  await expect(page.getByRole("button", { name: "example.test:443 · TCP", exact: true })).toBeVisible();
}

test("selected node becomes pending without claiming the applied policy", async ({ page }, testInfo) => {
  // Given: both nodes are OFF and only nodeA is selected.
  const controls = await fixture(page);
  const commands: unknown[] = [];
  await page.route("**/api/v1/admin/nodes/connection-logging", async route => {
    commands.push(route.request().postDataJSON());
    const current = controls.get(nodeA);
    if (!current) throw new Error("Fixture node missing");
    controls.set(nodeA, { ...current, enabled: true, desired_revision: "1", collection_epoch: epoch, state: "pending" });
    await route.fulfill({ json: { [nodeA]: { enabled: true, revision: "1", collection_epoch: epoch } } });
  });
  await page.goto("/admin/nodes");
  await expect(loggingTable(page).getByText("Off", { exact: true })).toHaveCount(2);
  await loggingTable(page).getByRole("checkbox", { name: "nodeA", exact: true }).check();
  // When: applying collection ON to that selection.
  const response = page.waitForResponse(r => r.url().endsWith("/nodes/connection-logging") && r.request().method() === "POST");
  await page.getByRole("button", { name: "Set selected ON", exact: true }).click();
  await response;
  // Then: only nodeA's desired policy changes; applied OFF and nodeB remain unchanged.
  await expect(loggingTable(page).getByRole("row").filter({ hasText: "nodeA" })).toContainText("Pending");
  await expect(loggingTable(page).getByRole("row").filter({ hasText: "nodeA" })).toContainText("OFF · 0");
  await expect(loggingTable(page).getByRole("row").filter({ hasText: "nodeB" })).toContainText("Off");
  expect(commands).toEqual([{ nodes: [{ id: nodeA, expected_revision: "0" }], enabled: true }]);
  await page.screenshot({ path: testInfo.outputPath("selected-pending.png"), fullPage: true });
});

test("atomic conflict displays the node reason and keeps both policies OFF", async ({ page }) => {
  // Given: a stale two-node command will be rejected atomically.
  await fixture(page);
  await page.route("**/api/v1/admin/nodes/connection-logging", route => route.fulfill({ status: 409, json: { error: "bulk_conflict", nodes: { [nodeB]: "revision_conflict" } } }));
  await page.goto("/admin/nodes");
  await expect(loggingTable(page).getByText("Off", { exact: true })).toHaveCount(2);
  await loggingTable(page).getByRole("checkbox", { name: "Select nodes for collection", exact: true }).check();
  // When: applying the rejected command.
  await page.getByRole("button", { name: "Set selected ON", exact: true }).click();
  // Then: the exact reason is visible and neither policy is promoted.
  await expect(page.getByText(/nodeB: revision_conflict/)).toBeVisible();
  await expect(loggingTable(page).getByText("OFF · 0", { exact: true })).toHaveCount(4);
});

test("fresh applied acknowledgment changes only the acknowledged node", async ({ page }, testInfo) => {
  // Given: nodeA is pending while nodeB remains OFF.
  const controls = await fixture(page);
  controls.set(nodeA, { ...off, node_id: nodeA, node_name: "nodeA", enabled: true, desired_revision: "1", collection_epoch: epoch, state: "pending" });
  await page.goto("/admin/nodes");
  await expect(loggingTable(page).getByText("Pending", { exact: true })).toBeVisible();
  controls.set(nodeA, { ...off, node_id: nodeA, node_name: "nodeA", enabled: true, desired_revision: "1", applied_revision: "1", applied_enabled: true, collection_epoch: epoch, applied_collection_epoch: epoch, state: "collecting" });
  // When: reloading after the API reports the applied barrier.
  const response = page.waitForResponse(r => r.url().endsWith(`/nodes/${nodeA}/connection-logging`));
  await page.reload();
  await response;
  // Then: nodeA is collecting at the matching revisions; nodeB stays OFF.
  const table = loggingTable(page);
  await expect(table.getByRole("row").filter({ hasText: "nodeA" })).toContainText("Collecting");
  await expect(table.getByRole("row").filter({ hasText: "nodeA" }).getByText("ON · 1", { exact: true })).toHaveCount(2);
  await expect(table.getByRole("row").filter({ hasText: "nodeB" }).getByText("OFF · 0", { exact: true })).toHaveCount(2);
  await page.screenshot({ path: testInfo.outputPath("applied-acknowledgment.png"), fullPage: true });
});

test("node detail handles an unavailable supplier with null capabilities", async ({ page }, testInfo) => {
  // Given: a valid unavailable producer response, not an installed capability claim.
  await fixture(page);
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.route(`**/api/v1/admin/nodes/${nodeA}/connection-logging`, route => route.fulfill({ json: {
    ...off, node_id: nodeA, node_name: "nodeA", state: "unsupported",
    producers: [{ producer_id: "xray", observer_boot_id: null, runtime_build: "", capabilities: null, applied_revision: "0", last_durable_sequence: "0", error_code: "supplier_unavailable" }],
  } }));
  // When: reading this node's logging details.
  await page.goto(`/admin/nodes/${nodeA}`);
  // Then: unsupported status and the producer reason stay readable without a page crash.
  await expect(page.getByText("Unsupported", { exact: true })).toBeVisible();
  await expect(page.getByText(/supplier_unavailable/)).toBeVisible();
  expect(errors).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("unavailable-producer.png"), fullPage: true });
});

test("history detail preserves unknown lifecycle, gap and large byte samples", async ({ page }, testInfo) => {
  // Given: interrupted shared-credential history with separate byte-layer totals.
  await fixture(page);
  await openHistory(page);
  await expect(page.getByText("36,893,488,147,419,103,230 B / 456 B", { exact: true })).toBeVisible();
  await expect(page.getByText("311 B / 419 B", { exact: true })).toBeVisible();
  // When: opening its immutable destination detail.
  const detailResponse = page.waitForResponse(r => new URL(r.url()).pathname.includes(`/connection-history/flows/${nodeA}/${epoch}/`));
  await page.getByRole("button", { name: "example.test:443 · TCP", exact: true }).click();
  await detailResponse;
  const dialog = page.getByRole("dialog", { name: "Connection observation details", exact: true });
  // Then: loss and unknown endpoints never become fabricated final values.
  await expect(dialog.getByText("Shared credential · account unknown", { exact: true })).toBeVisible();
  await expect(dialog.getByText(/18,446,744,073,709,551,615 B/)).toBeVisible();
  await expect(dialog.getByText(/final total unknown/)).toBeVisible();
  await expect(dialog.getByText("producer_lost", { exact: true })).toBeVisible();
  await expect(dialog.getByText(/Lost events: Unknown/)).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("history-detail.png"), animations: "disabled" });
  await dialog.getByText("producer_lost", { exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("history-detail-end.png"), animations: "disabled" });
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});

test("history sends server filters and unchanged cursor with UTC bounds", async ({ page }) => {
  // Given: an operator enters exact server-side filters.
  await fixture(page);
  await openHistory(page);
  await page.getByRole("textbox", { name: "Destination", exact: true }).fill("example.test");
  await page.getByRole("textbox", { name: "Started at or after", exact: true }).fill("2026-11-06T12:00:00+09:00");
  await page.getByRole("button", { name: "Protocol All", exact: true }).click();
  await page.getByRole("option", { name: "shadowsocks", exact: true }).click();
  await page.getByRole("button", { name: "Observation state All", exact: true }).click();
  await page.getByRole("option", { name: "Interrupted · end unknown", exact: true }).click();
  const filtered = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/connection-history/flows") && new URL(r.url()).searchParams.has("destination"));
  // When: applying them and advancing one page.
  await page.getByRole("button", { name: "Apply filters", exact: true }).click();
  const filterUrl = new URL((await filtered).url());
  expect(filterUrl.searchParams.get("destination")).toBe("example.test");
  expect(filterUrl.searchParams.get("from")).toBe("2026-11-06T03:00:00.000Z");
  expect(filterUrl.searchParams.get("protocol")).toBe("shadowsocks");
  expect(filterUrl.searchParams.get("status")).toBe("interrupted");
  const next = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/connection-history/flows") && new URL(r.url()).searchParams.has("cursor"));
  await page.getByRole("button", { name: "Next", exact: true }).click();
  // Then: the opaque cursor is forwarded unchanged alongside the same filters.
  const nextUrl = new URL((await next).url());
  expect(nextUrl.searchParams.get("cursor")).toBe("opaque+/=");
  expect(nextUrl.searchParams.get("destination")).toBe("example.test");
});

test("user history fixes the account scope on page and aggregate requests", async ({ page }) => {
  // Given: a concrete user detail page.
  await fixture(page);
  await page.goto(`/admin/users/${userId}`);
  const requests = Promise.all([
    page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/connection-history/flows")),
    page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/connection-history/aggregate")),
  ]);
  // When: opening that user's history.
  await page.getByRole("tab", { name: "Connection history", exact: true }).click();
  // Then: both requests are scoped to this user, without an editable account field.
  for (const response of await requests) expect(new URL(response.url()).searchParams.get("user_id")).toBe(userId);
  await expect(page.getByRole("textbox", { name: "Account ID", exact: true })).toHaveCount(0);
});

test("all five consumers retain their responsive themed surface", async ({ page }, testInfo) => {
  // Given: deterministic fixtures and the project's real production UI.
  await fixture(page);
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  // When: visiting every page that consumes the history panels.
  for (const [name, route] of [
    ["dashboard", "/admin/dashboard"], ["nodes", "/admin/nodes"],
    ["node-detail", `/admin/nodes/${nodeA}`], ["connections", "/admin/connections"],
    ["user-detail", `/admin/users/${userId}`],
  ]) {
    await page.goto(route);
    if (name === "connections" || name === "user-detail") {
      await page.getByRole("tab", { name: "Connection history", exact: true }).click();
      await expect(page.getByRole("button", { name: "example.test:443 · TCP", exact: true })).toBeVisible();
    } else {
      await expect(loggingTable(page).getByText("Off", { exact: true })).toHaveCount(name === "node-detail" ? 1 : 2);
    }
    // Then: no primary-document overflow or page errors, with a capture per consumer.
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`${name}.png`), fullPage: true });
  }
  expect(errors).toEqual([]);
});
