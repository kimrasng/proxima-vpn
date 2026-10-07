import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  Container,
  Header,
  Popover,
  SpaceBetween,
  StatusIndicator,
  Table,
} from "@cloudscape-design/components";
import { getPlanRoutes, listNodeChains, listNodes, setPlanRoutes } from "../../api/admin";
import type { Node, NodeChain, NodeGroup, Plan, PlanRoutesResponse } from "../../api/types";
import { adminError } from "../../utils/adminError";
import { routeConfigurationIssues } from "../../utils/routeConfiguration";
import "./planRoutesPanel.css";

interface PlanRoutesPanelProps {
  plan: Plan;
  nodeGroups: NodeGroup[];
  policyChanged: boolean;
  disabled: boolean;
  onBusyChange: (busy: boolean) => void;
  onDirtyChange: (dirty: boolean) => void;
  onSavedRoutes: (routes: PlanRoutesResponse) => void;
}

interface RouteRow {
  id: string;
  chain?: NodeChain;
}

export default function PlanRoutesPanel({
  plan,
  nodeGroups,
  policyChanged,
  disabled,
  onBusyChange,
  onDirtyChange,
  onSavedRoutes,
}: PlanRoutesPanelProps) {
  const { t } = useTranslation();
  const [routes, setRoutes] = useState<PlanRoutesResponse | null>(null);
  const [chains, setChains] = useState<NodeChain[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [applied, setApplied] = useState(false);
  // Loading must use the latest editor draft without making draft edits
  // trigger an automatic refresh that would erase route selections.
  const onSavedRoutesRef = useRef(onSavedRoutes);
  onSavedRoutesRef.current = onSavedRoutes;

  const load = useCallback(async () => {
    setLoading(true);
    onBusyChange(true);
    setError(null);
    setApplied(false);
    try {
      const [savedRoutes, chainList, nodeList] = await Promise.all([
        getPlanRoutes(plan.id), listNodeChains(), listNodes(),
      ]);
      setRoutes(savedRoutes);
      setChains(chainList);
      setNodes(nodeList);
      setSelectedIds(savedRoutes.chain_ids);
      onDirtyChange(false);
      onSavedRoutesRef.current(savedRoutes);
    } catch (cause) {
      // Do not allow applying a stale selection after a failed refresh.
      setRoutes(null);
      setError(adminError(cause, t("admin.routeManagement.fetchError")));
    } finally {
      setLoading(false);
      onBusyChange(false);
    }
  }, [plan.id, t, onBusyChange, onDirtyChange]);

  useEffect(() => { void load(); }, [load]);

  const apply = async () => {
    if (!routes || policyChanged || disabled || applying || loading) return;
    setApplying(true);
    onBusyChange(true);
    setError(null);
    setApplied(false);
    try {
      const savedRoutes = await setPlanRoutes(plan.id, selectedIds);
      setRoutes(savedRoutes);
      setSelectedIds(savedRoutes.chain_ids);
      onDirtyChange(false);
      onSavedRoutesRef.current(savedRoutes);
      setApplied(true);
    } catch (cause) {
      setError(adminError(cause, t("admin.routeManagement.applyError")));
    } finally {
      setApplying(false);
      onBusyChange(false);
    }
  };

  const missingIds = routes?.chain_ids.filter((id) => !chains.some((chain) => chain.id === id)) ?? [];
  // Keep missing saved IDs visible and selectable rather than silently dropping
  // assignments when the catalog is temporarily incomplete.
  const rows: RouteRow[] = [
    ...chains.map((chain) => ({ id: chain.id, chain })),
    ...missingIds.map((id) => ({ id })),
  ];
  const dirty = routes !== null && (
    selectedIds.length !== routes.chain_ids.length || selectedIds.some((id) => !routes.chain_ids.includes(id))
  );
  const blocked = disabled || policyChanged || applying || loading || routes === null;
  const routeName = (row: RouteRow) => row.chain?.name ?? t("admin.routeManagement.missingRoute", { id: row.id });
  const groupName = routes && (nodeGroups.find((group) => group.id === routes.node_group_id)?.name ?? routes.node_group_id);
  const policyLabel = routes?.speed_enforcement === "device_global_v1"
    ? t("admin.routeManagement.deviceSpeed", { speed: routes.speed_limit ?? plan.speed_limit ?? 0 })
    : routes?.speed_enforcement === "shared_tier"
      ? t("admin.routeManagement.sharedTierSpeed", { speed: routes.speed_limit ?? plan.speed_limit ?? 0 })
      : t("admin.routeManagement.unlimitedSpeed");
  const guidance = [
    t("admin.routeManagement.availabilityWarning"),
    ...(routes?.speed_enforcement === "device_global_v1" ? [t("admin.routeManagement.devicePolicyWarning")] :
      routes?.speed_enforcement === "shared_tier" ? [t("admin.routeManagement.perDeviceWarning")] : []),
    t("admin.routeManagement.sharedGroupWarning"),
    ...(routes?.warnings ?? []).filter((warning) => warning !== "route_assignment_not_subscriber_readiness" &&
      !(routes?.speed_enforcement === "device_global_v1" && warning === "device_bandwidth_requires_current_agent_ack"))
      .map((warning) => t(`admin.routeManagement.serverWarnings.${warning}`, { defaultValue: warning })),
  ];

  const serverStatus = (nodeId: string) => {
    const node = nodes.find((item) => item.id === nodeId);
    return <StatusIndicator type={node?.status === "online" ? "success" : node ? "warning" : "info"}>
      {t(`admin.routeManagement.${node?.status === "online" ? "online" : node ? "offline" : "unknown"}`)}
    </StatusIndicator>;
  };

  return (
    <section id="plan-routes" aria-label={t("admin.routeManagement.planTitle")}>
      <Container header={<Header variant="h2">{t("admin.routeManagement.planTitle")}</Header>}>
        <SpaceBetween size="s">
          <div className="plan-routes-summary">
            {routes && <Box>{t("admin.routeManagement.savedGroup", { group: groupName })} · {t("admin.routeManagement.speedPolicy")}: {policyLabel}</Box>}
            <Popover triggerType="custom" position="top" header={t("admin.routeManagement.guidance")}
              content={<SpaceBetween size="xs">{guidance.map((message, index) => <Box key={`${index}-${message}`}>{message}</Box>)}</SpaceBetween>}>
              <Button variant="inline-link" iconName="status-info">{t("admin.routeManagement.guidance")}</Button>
            </Popover>
          </div>
          {policyChanged && <Alert type="warning">{t("admin.routeManagement.groupChanged")}</Alert>}
          {dirty && <Alert type="warning">{t("admin.routeManagement.pendingChanges", { defaultValue: "Apply or discard your route changes before saving plan settings. Refresh is disabled while route changes are unapplied." })}</Alert>}
          {error && <Alert type="error">{error}</Alert>}
          {applied && <Alert type="success">{t("admin.routeManagement.applied")}</Alert>}
          {missingIds.length > 0 && <Alert type="warning">{t("admin.routeManagement.missingRoutesWarning")}</Alert>}
          <div className="intrinsic-table">
          <Table<RouteRow>
            items={rows}
            trackBy="id"
            wrapLines={false}
            resizableColumns={false}
            loading={loading}
            loadingText={t("admin.routeManagement.loading")}
            selectionType="multi"
            selectedItems={rows.filter((row) => selectedIds.includes(row.id))}
            isItemDisabled={() => blocked}
            onSelectionChange={({ detail }) => {
              if (blocked) return;
              const nextIds = detail.selectedItems.map((row) => row.id);
              setSelectedIds(nextIds);
              onDirtyChange(routes !== null && (
                nextIds.length !== routes.chain_ids.length || nextIds.some(id => !routes.chain_ids.includes(id))
              ));
              setApplied(false);
            }}
            ariaLabels={{
              tableLabel: t("admin.routeManagement.planTitle"),
              selectionGroupLabel: t("admin.routeManagement.selectAllRoutes"),
              allItemsSelectionLabel: () => t("admin.routeManagement.selectAllRoutes"),
              itemSelectionLabel: (_data, row) => t("admin.routeManagement.selectRoute", { name: routeName(row) }),
            }}
            header={<Header variant="h3" counter={t("admin.routeManagement.selectedCount", { count: selectedIds.length })}>{t("admin.routeManagement.planRoutes")}</Header>}
            empty={<Box textAlign="center">{t("admin.routeManagement.planEmpty")}</Box>}
            columnDefinitions={[
              { id: "name", header: t("admin.routeManagement.name"), cell: routeName },
              { id: "path", header: t("admin.routeManagement.path"), cell: ({ chain }) => chain ? (
                <Box>{chain.entry_host || nodes.find((node) => node.id === chain.exit_node_id)?.ip || "—"}:{chain.entry_port ?? chain.exit_port} → {chain.exit_node_name}:{chain.exit_port}</Box>
              ) : "—" },
              { id: "enabled", header: t("admin.routeManagement.enabled"), cell: ({ chain }) => chain ? <StatusIndicator type={chain.enabled ? "success" : "stopped"}>
                {t(`admin.routeManagement.${chain.enabled ? "enabled" : "disabled"}`)}
              </StatusIndicator> : t("admin.routeManagement.unknown") },
              { id: "health", header: t("admin.routeManagement.serverHealth"), cell: ({ chain }) => chain ? <SpaceBetween direction="horizontal" size="xs" alignItems="center">
                {chain.entry_node_id && <span>{t("admin.routeManagement.entryServer")}: {serverStatus(chain.entry_node_id)}</span>}
                <span>{t("admin.routeManagement.exitServer")}: {serverStatus(chain.exit_node_id)}</span>
              </SpaceBetween> : t("admin.routeManagement.unknown") },
              { id: "checks", header: t("admin.routeManagement.checks"), cell: ({ chain }) => {
                if (!chain) return t("admin.routeManagement.unknown");
                const issues = routeConfigurationIssues(chain, nodes).filter(
                  (issue) => issue !== "disabled" && issue !== "entryOffline" && issue !== "exitOffline",
                );
                return issues.length ? <Popover triggerType="custom" position="top" header={t("admin.routeManagement.checks")}
                  content={<SpaceBetween size="xs">{issues.map((issue) => <Box key={issue}>{t(`admin.routeManagement.issues.${issue}`)}</Box>)}</SpaceBetween>}>
                  <Button variant="inline-link" ariaLabel={t("admin.routeManagement.issueDetails", { count: issues.length })}>
                    {t("admin.routeManagement.issueCount", { count: issues.length })}
                  </Button>
                </Popover> : t("admin.routeManagement.noIssues");
              } },
            ]}
          />
          </div>
          <SpaceBetween direction="horizontal" size="xs">
            <Button disabled={disabled || applying || loading || dirty} onClick={() => void load()}>{t("admin.routeManagement.refresh")}</Button>
            {dirty && <Button disabled={disabled || applying || loading} onClick={() => {
              setSelectedIds(routes?.chain_ids ?? []);
              onDirtyChange(false);
              setApplied(false);
              setError(null);
            }}>{t("admin.plans.editor.discard")}</Button>}
            <Button variant="primary" loading={applying} disabled={blocked || !dirty} onClick={() => void apply()}>{t("admin.routeManagement.apply")}</Button>
          </SpaceBetween>
        </SpaceBetween>
      </Container>
    </section>
  );
}
