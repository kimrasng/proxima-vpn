import { Box, SpaceBetween, StatusIndicator, type StatusIndicatorProps } from "@cloudscape-design/components";
import { useTranslation } from "react-i18next";
import type { Node } from "../api/types";

const statusTypes = {
  unconfigured: "not-started", pending: "pending", ready: "success", conflict: "warning",
  error: "error", deleting: "in-progress", deleted: "stopped", valid: "success", not_applicable: "not-started",
} as const satisfies Record<NonNullable<Node["entry_dns_status"] | Node["reality_sni_status"]> | "unconfigured", StatusIndicatorProps.Type>;

const safeCodes: readonly string[] = [
  "provider_unavailable", "provider_rejected", "ownership_conflict", "invalid_configuration", "dns_mismatch",
  "no_common_name", "invalid_sni", "listener_mismatch", "not_reality",
];

export function NodeEndpointState({ node, kind }: { readonly node: Node; readonly kind: "dns" | "sni" }) {
  const { t } = useTranslation();
  const hostname = kind === "dns" ? node.entry_hostname : node.reality_client_sni;
  const status = (kind === "dns" ? node.entry_dns_status : node.reality_sni_status) ?? "unconfigured";
  const code = kind === "dns" ? node.entry_dns_error_code : node.reality_sni_error_code;
  return (
    <SpaceBetween size="xxxs">
      {hostname && <Box variant="small">{hostname}</Box>}
      <StatusIndicator type={statusTypes[status]}>{t(`admin.nodes.endpoints.states.${status}`)}</StatusIndicator>
      {code && safeCodes.includes(code) && <Box variant="small" color="text-body-secondary">{code}</Box>}
    </SpaceBetween>
  );
}
