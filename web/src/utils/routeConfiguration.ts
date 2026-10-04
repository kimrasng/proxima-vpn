import type { Node, NodeChain } from "../api/types";

// Configuration checks are not subscriber eligibility or policy acknowledgments.
// Keep that distinction visible until the server provides authoritative evidence.
export type RouteIssue = "disabled" | "unassigned" | "directPrivate" | "entryOffline" | "exitOffline" | "dnsPending" | "sniPending" | "healthFailed";

export function routeConfigurationIssues(chain: NodeChain, nodes: Node[]): RouteIssue[] {
  const issues: RouteIssue[] = [];
  const entry = nodes.find((node) => node.id === chain.entry_node_id);
  const exit = nodes.find((node) => node.id === chain.exit_node_id);
  if (!chain.enabled) issues.push("disabled");
  if (!chain.group_ids.length) issues.push("unassigned");
  if (!chain.entry_node_id && !chain.relay_pool_id && exit && !exit.publish_direct) issues.push("directPrivate");
  if (chain.entry_node_id) {
    if (entry?.status !== "online") issues.push("entryOffline");
    if (entry?.entry_dns_status !== "ready") issues.push("dnsPending");
  }
  if (exit?.status !== "online") issues.push("exitOffline");
  if (chain.transport !== "udp" && exit?.reality_sni_status !== "valid" && exit?.reality_sni_status !== "not_applicable") issues.push("sniPending");
  if (chain.health === "unhealthy" || chain.health === "error") issues.push("healthFailed");
  return issues;
}
