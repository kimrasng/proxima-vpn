import { expect, test, type Page, type Response } from "@playwright/test";
import type { ConnectionHistoryRow } from "../src/api/types";
import { mockSession } from "./subscription-domain-fixtures";

const nodeA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const nodeB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const userId = "dddddddd-dddd-4ddd-8ddd-dddddddddddd";
const epoch = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
const boot = "11111111-1111-4111-8111-111111111111";
const nodes = [nodeA, nodeB].map((id, index) => ({
  id, name: `node${index === 0 ? "A" : "B"}`, status: "online", role: "exit",
  country: "US", region: "New York", ip: `192.0.2.${index + 1}`, port: 443,
  traffic_multiplier: 1, cpu_usage: 10, memory_usage: 20, disk_usage: 30,
  online_devices: 0, capacity: 10, os_family: "debian", publish_direct: true,
  max_concurrent_conns: 0, firewall_preset: "standard", firewall_ports: "443",
  created_at: "2026-11-06T00:00:00Z", shaping_ok: true, shaping_tiers: 1,
  shaping_error: null, xray_too_old: false, xray_minimum: "", xray_version_warning: "",
}));
const rows: ConnectionHistoryRow[] = nodes.map((node, index) => ({
  node_id: node.id, node_name: node.name, user_id: userId, user_snapshot: "Scope fixture",
  device_id: null, device_snapshot: null, first_observed_at: "2026-11-06T12:00:00Z",
  observation_status: "observing", gap_coverage: [],
  event: {
    schema_version: 1, event_id: `${boot}:${index + 1}`, observer_boot_id: boot,
    producer_id: "xray", sequence: String(index + 1), runtime: "xray",
    runtime_build: "synthetic-node-scope-fixture", config_revision: "1",
    collection_epoch: epoch, event_time: "2026-11-06T12:00:00Z",
    monotonic_ns: "1", kind: "flow_update", tunnel_id: `${boot}:t:${index}`,
    flow_id: `${boot}:f:${index}`, protocol: "vless", inbound_tag: "fixture",
    auth: { kind: "device", credential_ref: null, binding_revision: null },
    peer: { ip: "192.0.2.10", port: 50000 },
    destination: { requested_host: `${node.name.toLowerCase()}.example.test`, requested_port: 443, transport: "tcp", effective_host: null, observed_ip: null, observed_port: null },
    bytes: { layer: "proxy_payload_observed", upload: String(100 + index), download: "456", final: false },
    lifecycle: { scope: "proxy_flow", started_at: null, last_observed_at: "2026-11-06T12:00:00Z", ended_at: null, partial_start: true, end_reason: null, end_semantics: null },
    quality: { identity: "exact", parent: "exact", destination: "exact", bytes: "exact", start: "unknown", end: "unknown", completeness: "partial" },
    gap: null,
  },
}));

async function fixture(page: Page) {
  await mockSession(page, "admin");
  const queries: string[] = [];
  await page.route("**/api/v1/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    let data: unknown = [];
    const node = nodes.find(item => path.startsWith(`/api/v1/admin/nodes/${item.id}`));
    if (path.endsWith("/stats/alerts")) data = { items: [], node_issues: [], total: 0 };
    else if (path.endsWith("/connection-logging") && node) data = {
      node_id: node.id, node_name: node.name, enabled: false, desired_revision: "1",
      applied_revision: "1", applied_enabled: false, collection_epoch: null,
      applied_collection_epoch: null, state: "off", status_at: null,
      last_config_fetch_at: null, applied_at: null, shared_identity: false,
      queue_bytes: "0", dropped_events: "0", error_code: "", producers: [],
    };
    else if (node && path === `/api/v1/admin/nodes/${node.id}`) data = node;
    else if (path === "/api/v1/admin/nodes") data = nodes;
    else if (/\/connection-history\/(flows|sessions)\//.test(path)) {
      data = rows.find(row => path.includes(row.node_id));
    } else if (/\/connection-history\/(flows|sessions|aggregate)$/.test(path)) {
      queries.push(url.pathname + url.search);
      const selected = rows.filter(row =>
        (!url.searchParams.has("node_id") || row.node_id === url.searchParams.get("node_id")) &&
        (!url.searchParams.has("user_id") || row.user_id === url.searchParams.get("user_id")));
      data = path.endsWith("/aggregate")
        ? [{ layer: "proxy_payload_observed", upload: String(selected.reduce((sum, row) => sum + Number(row.event.bytes.upload), 0)), download: String(selected.length * 456), complete_intervals: 0, partial_intervals: selected.length, boundary_policy: "complete_observed_intervals_only" }]
        : { items: url.searchParams.has("cursor") ? [] : selected, next_cursor: url.searchParams.has("cursor") ? null : "opaque+/=" };
    } else if (path === `/api/v1/admin/users/${userId}`) data = { id: userId, email: "scope@example.test", name: "Scope fixture", status: "active", is_active: true, traffic_used: 0, created_at: "2026-01-01T00:00:00Z", devices: [] };
    else if (path.endsWith("/traffic")) data = { traffic_used: 0, traffic_limit: null, percentage: 0, unlimited: true, window: "month", by_node: [] };
    else if (path.endsWith("/client-addresses")) data = { complete: true, addresses: [], count: 0 };
    else if (path.endsWith("/tunnel-sessions")) data = { complete: true, count: 0, enforced: false };
    else if (path.endsWith("/events")) data = { items: [], total: 0 };
    else if (path.endsWith("/event-filters")) data = { event_types: [], severities: [] };
    await route.fulfill({ json: data });
  });
  return queries;
}

async function scopedAction(page: Page, action: () => Promise<unknown>, scope: { readonly node_id?: string; readonly user_id?: string }, kind = "flows") {
  // Subscribe before the action; no sleeps or network-idle heuristics.
  const responses = Promise.all([kind, "aggregate"].map(endpoint =>
    page.waitForResponse(response => new URL(response.url()).pathname === `/api/v1/admin/connection-history/${endpoint}`)));
  await action();
  for (const response of await responses) {
    expect(response.status()).toBe(200);
    for (const [key, value] of Object.entries(scope)) {
      expect(new URL(response.url()).searchParams.get(key)).toBe(value);
    }
  }
  await expect(page.getByRole("tabpanel").getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
}

test("node detail opens immutable scoped history and renders the delta", async ({ page }, testInfo) => {
  // Given: two observers with distinguishable rows and aggregate bytes.
  const queries = await fixture(page);
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(`/admin/nodes/${nodeA}`);
  await expect(page.getByRole("heading", { name: "nodeA", exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("node-overview.png"), fullPage: true });
  // When: opening history from this node, not from the global sidebar.
  await expect(page.getByRole("tab", { name: "Connection history", exact: true })).toBeVisible();
  await scopedAction(page, () => page.getByRole("tab", { name: "Connection history", exact: true }).click(), { node_id: nodeA });
  // Then: list, aggregate, and controls cannot silently widen to another node.
  await expect(page.getByRole("button", { name: "nodea.example.test:443 · TCP", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "nodeb.example.test:443 · TCP", exact: true })).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Node ID", exact: true })).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Account ID", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(errors).toEqual([]);
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
  await page.screenshot({ path: testInfo.outputPath("node-history.png"), fullPage: true, animations: "disabled" });
  await testInfo.attach("scoped-requests", { body: JSON.stringify(queries, null, 2), contentType: "application/json" });
});

test("node scope survives filters clear pagination refresh and session drilldown", async ({ page }, testInfo) => {
  // Given: the node-local panel has opened.
  const queries = await fixture(page);
  await page.goto(`/admin/nodes/${nodeA}`);
  await scopedAction(page, () => page.getByRole("tab", { name: "Connection history", exact: true }).click(), { node_id: nodeA });
  // When: using each existing history navigation path.
  await page.getByRole("textbox", { name: "Account ID", exact: true }).fill(userId);
  await scopedAction(page, () => page.getByRole("button", { name: "Apply filters", exact: true }).click(), { node_id: nodeA, user_id: userId });
  await scopedAction(page, () => page.getByRole("button", { name: "Next", exact: true }).click(), { node_id: nodeA });
  await scopedAction(page, () => page.getByRole("button", { name: "Previous", exact: true }).click(), { node_id: nodeA });
  await scopedAction(page, () => page.getByRole("button", { name: "Clear filters", exact: true }).click(), { node_id: nodeA });
  await scopedAction(page, () => page.getByRole("tabpanel").getByRole("button", { name: "Refresh", exact: true }).click(), { node_id: nodeA });
  await page.getByRole("button", { name: "History type" }).click();
  await scopedAction(page, () => page.getByRole("option", { name: "Transport / activity sessions", exact: true }).click(), { node_id: nodeA }, "sessions");
  const detail = page.waitForResponse(response => new URL(response.url()).pathname.includes(`/sessions/${nodeA}/`));
  await page.getByRole("button", { name: `${boot}:t:0`, exact: true }).click();
  await detail;
  await scopedAction(page, () => page.getByRole("button", { name: "View session destinations", exact: true }).click(), { node_id: nodeA });
  // Then: every list and aggregate request carries the same immutable node.
  expect(queries.length).toBeGreaterThanOrEqual(16);
  for (const query of queries) expect(new URL(query, "http://fixture").searchParams.get("node_id")).toBe(nodeA);
  await testInfo.attach("scoped-navigation-requests", { body: JSON.stringify(queries, null, 2), contentType: "application/json" });
});

test("route changes discard the previous node history scope", async ({ page }) => {
  // Given: nodeA's panel has a user filter.
  await fixture(page);
  await page.goto(`/admin/nodes/${nodeA}`);
  await scopedAction(page, () => page.getByRole("tab", { name: "Connection history", exact: true }).click(), { node_id: nodeA });
  await page.getByRole("textbox", { name: "Account ID", exact: true }).fill(userId);
  // When: React Router receives a same-component node route transition.
  const loaded = page.waitForResponse(response => new URL(response.url()).pathname === `/api/v1/admin/nodes/${nodeB}`);
  await page.evaluate(id => {
    window.history.pushState({}, "", `/admin/nodes/${id}`);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, nodeB);
  await loaded;
  // Then: the new node starts on overview and opens only its own history.
  await expect(page.getByRole("heading", { name: "nodeB", exact: true })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Connection history", exact: true })).toHaveAttribute("aria-selected", "false");
  await scopedAction(page, () => page.getByRole("tab", { name: "Connection history", exact: true }).click(), { node_id: nodeB });
  await expect(page.getByRole("textbox", { name: "Account ID", exact: true })).toHaveValue("");
  await expect(page.getByRole("button", { name: "nodeb.example.test:443 · TCP", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "nodea.example.test:443 · TCP", exact: true })).toHaveCount(0);
});

test("user detail retains fixed user scope and editable node filtering", async ({ page }) => {
  // Given: the existing user-detail consumer.
  await fixture(page);
  await page.goto(`/admin/users/${userId}`);
  // When: opening and clearing its history.
  const seen: Response[] = [];
  page.on("response", response => {
    if (/\/connection-history\/(flows|aggregate)\?/.test(response.url())) seen.push(response);
  });
  await scopedAction(page, () => page.getByRole("tab", { name: "Connection history", exact: true }).click(), { user_id: userId });
  await page.getByRole("textbox", { name: "Node ID", exact: true }).fill(nodeB);
  await scopedAction(page, () => page.getByRole("button", { name: "Apply filters", exact: true }).click(), { user_id: userId, node_id: nodeB });
  await scopedAction(page, () => page.getByRole("button", { name: "Clear filters", exact: true }).click(), { user_id: userId });
  // Then: the user stays fixed while the node restriction can be removed.
  await expect(page.getByRole("textbox", { name: "Account ID", exact: true })).toHaveCount(0);
  for (const response of seen) expect(new URL(response.url()).searchParams.get("user_id")).toBe(userId);
  for (const response of seen.slice(-2)) expect(new URL(response.url()).searchParams.has("node_id")).toBe(false);
});
