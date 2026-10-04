import { expect, type Page } from "@playwright/test";
import type { Node, NodeChain } from "../src/api/types";
import { mockSession } from "./subscription-domain-fixtures";

export const entry: Node = {
  id: "entry", name: "Managed entry", country: "KR", region: "Seoul", ip: "192.0.2.21/32",
  port: 443, status: "online", role: "relay", publish_direct: false, traffic_multiplier: 1,
  shaping_ok: null, shaping_tiers: null, shaping_error: null, xray_too_old: false,
  xray_minimum: "", xray_version_warning: "", online_devices: 0, capacity: 0,
  os_family: "debian", max_concurrent_conns: 0, firewall_preset: "standard",
  firewall_ports: "443", created_at: "2026-01-01T00:00:00Z",
  entry_hostname: "managed.entry.example.test", entry_dns_status: "pending", entry_dns_error_code: null,
  reality_client_sni: null, reality_sni_status: null, reality_sni_error_code: null,
};
export const exit: Node = { ...entry, id: "exit", name: "Exit", role: "exit", port: 8443 };
export const chain: NodeChain = {
  id: "chain", name: "Existing link", entry_node_id: entry.id, entry_node_name: entry.name,
  entry_host: "stale.example.test", entry_port: 22001, exit_node_id: exit.id, exit_node_name: exit.name,
  exit_port: 8443, transport: "tcp", mode: "relay", priority: 7, enabled: true, health: "", group_ids: [],
};
export const endpoint = "**/api/v1/admin/node-chains";

export async function mockTopology(page: Page) {
  await mockSession(page, "admin");
  await page.route("**/src/api/**", (route) => route.continue());
  await page.route("**/api/v1/admin/nodes", (route) => route.fulfill({ json: [entry, exit] }));
  await page.route("**/api/v1/admin/node-groups", (route) => route.fulfill({ json: [{ id: "access", name: "Subscribers" }] }));
  await page.route(endpoint, (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/admin/plans", (route) => route.fulfill({ json: [] }));
}

export async function openCreate(page: Page) {
  await page.goto("/admin/node-chains");
  await page.getByRole("button", { name: "Create link", exact: true }).click();
  const form = page.getByRole("dialog");
  await form.getByRole("button", { name: /Entry node Choose an entry node/ }).click();
  await page.getByRole("option", { name: entry.name }).click();
  await form.getByRole("button", { name: /Exit node Choose an exit/ }).click();
  await page.getByRole("option", { name: exit.name }).click();
  await expect(form.getByRole("textbox", { name: "Name", exact: true })).toHaveValue("Managed entry → Exit");
  return form;
}
