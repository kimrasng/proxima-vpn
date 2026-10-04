import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  Container,
  Header,
  SpaceBetween,
  StatusIndicator,
  Table,
} from "@cloudscape-design/components";
import { getPlanRoutes, listNodeChains, listNodes, setPlanRoutes } from "../../api/admin";
import type { Node, NodeChain, NodeGroup, Plan, PlanRoutesResponse } from "../../api/types";
import { adminError } from "../../utils/adminError";
import { routeConfigurationIssues } from "../../utils/routeConfiguration";

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

  const serverStatus = (nodeId: string) => {
    const node = nodes.find((item) => item.id === nodeId);
    return <StatusIndicator type={node?.status === "online" ? "success" : node ? "warning" : "info"}>
      {t(`admin.routeManagement.${node?.status === "online" ? "online" : node ? "offline" : "unknown"}`)}
    </StatusIndicator>;
  };

  return (
    <section id="plan-routes" aria-label={t("admin.routeManagement.planTitle")}>
      <Container header={<Header variant="h2" description={t("admin.routeManagement.planDescription")}>{t("admin.routeManagement.planTitle")}</Header>}>
        <SpaceBetween size="m">
          <Alert type="info">{t("admin.routeManagement.availabilityWarning")}</Alert>
          <Alert type="warning">{t(routes?.speed_enforcement === "device_global_v1" ? "admin.routeManagement.devicePolicyWarning" : "admin.routeManagement.perDeviceWarning")}</Alert>
          <Alert type="info">{t("admin.routeManagement.sharedGroupWarning")}</Alert>
          {policyChanged && <Alert type="warning">{t("admin.routeManagement.groupChanged")}</Alert>}
          {dirty && <Alert type="warning">{t("admin.routeManagement.pendingChanges", { defaultValue: "Apply or discard your route changes before saving plan settings. Refresh is disabled while route changes are unapplied." })}</Alert>}
          {error && <Alert type="error">{error}</Alert>}
          {applied && <Alert type="success">{t("admin.routeManagement.applied")}</Alert>}
          {routes && <>
            <Box>{t("admin.routeManagement.savedGroup", { group: groupName })}</Box>
            <Box>{t("admin.routeManagement.speedPolicy")}: {routes.speed_enforcement === "device_global_v1"
              ? t("admin.routeManagement.deviceSpeed", { speed: routes.speed_limit ?? plan.speed_limit ?? 0 })
              : routes.speed_enforcement === "shared_tier"
                ? t("admin.routeManagement.sharedTierSpeed", { speed: routes.speed_limit ?? plan.speed_limit ?? 0 })
                : t("admin.routeManagement.unlimitedSpeed")}</Box>
            {routes.warnings.map((warning, index) => <Alert key={`${index}-${warning}`} type="warning">{warning}</Alert>)}
          </>}
          {missingIds.length > 0 && <Alert type="warning">{t("admin.routeManagement.missingRoutesWarning")}</Alert>}
          <Table<RouteRow>
            items={rows}
            trackBy="id"
            wrapLines
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
              { id: "path", header: t("admin.routeManagement.path"), cell: ({ chain }) => chain ? <>
                <Box>{chain.entry_host}{chain.entry_port ? `:${chain.entry_port}` : ""} → {chain.exit_node_name}:{chain.exit_port}</Box>
                <Box variant="small" color="text-body-secondary">{chain.entry_node_name ?? chain.relay_pool_name ?? chain.exit_node_name} → {chain.exit_node_name}</Box>
              </> : "—" },
              { id: "enabled", header: t("admin.routeManagement.enabled"), cell: ({ chain }) => chain ? <StatusIndicator type={chain.enabled ? "success" : "stopped"}>
                {t(`admin.routeManagement.${chain.enabled ? "enabled" : "disabled"}`)}
              </StatusIndicator> : t("admin.routeManagement.unknown") },
              { id: "health", header: t("admin.routeManagement.serverHealth"), cell: ({ chain }) => chain ? <SpaceBetween size="xs">
                {chain.entry_node_id && <div>{t("admin.routeManagement.entryServer")}: {serverStatus(chain.entry_node_id)}</div>}
                <div>{t("admin.routeManagement.exitServer")}: {serverStatus(chain.exit_node_id)}</div>
                <div>{t("admin.routeManagement.routeHealth")}: <StatusIndicator type={chain.health === "healthy" ? "success" : !chain.health || chain.health === "unknown" ? "info" : "warning"}>
                  {t(`admin.routeManagement.${chain.health === "healthy" ? "healthHealthy" : !chain.health || chain.health === "unknown" ? "healthUnknown" : "healthUnhealthy"}`)}
                </StatusIndicator></div>
              </SpaceBetween> : t("admin.routeManagement.unknown") },
              { id: "checks", header: t("admin.routeManagement.checks"), cell: ({ chain }) => {
                if (!chain) return t("admin.routeManagement.unknown");
                const issues = routeConfigurationIssues(chain, nodes);
                return issues.length > 0 ? <SpaceBetween size="xs">{issues.map((issue) => <Box key={issue}>{t(`admin.routeManagement.issues.${issue}`)}</Box>)}</SpaceBetween> : t("admin.routeManagement.noIssues");
              } },
            ]}
          />
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
