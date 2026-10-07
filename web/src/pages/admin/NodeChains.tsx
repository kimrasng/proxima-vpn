import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Alert, Box, Button, ButtonDropdown, ContentLayout, FormField, Header, Popover, Select, SpaceBetween, Spinner, StatusIndicator, Table } from "@cloudscape-design/components";
import { listNodeChains, listNodeGroups, listNodes, listPlans, updateNodeChain } from "../../api/admin";
import type { Node, NodeChain, NodeGroup, Plan } from "../../api/types";
import { adminError } from "../../utils/adminError";
import { routeConfigurationIssues } from "../../utils/routeConfiguration";
import { NodeEndpointState } from "../../components/NodeEndpointState";
import NodeChainForm from "./NodeChainForm";
import EntryRouteBatchForm from "./EntryRouteBatchForm";
import "./nodeChainsTable.css";

export default function NodeChains() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [chains, setChains] = useState<NodeChain[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [groups, setGroups] = useState<NodeGroup[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [editor, setEditor] = useState<NodeChain | "create" | null>(null);
  const [batch, setBatch] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [chainList, nodeList, groupList, planList] = await Promise.all([listNodeChains(), listNodes(), listNodeGroups(), listPlans()]);
      setChains(chainList); setNodes(nodeList); setGroups(groupList); setPlans(planList); setError("");
    } catch (cause) { setError(adminError(cause, t("admin.nodeChains.fetchError"))); }
    finally { setLoading(false); }
  }, [t]);
  useEffect(() => { void refresh(); }, [refresh]);

  const entries = nodes.filter((node) => node.status !== "pending" && (node.role === "relay" || node.role === "both"));
  const exits = nodes.filter((node) => node.status !== "pending" && (node.role === "exit" || node.role === "both"));
  const entryId = params.get("entry") ?? "";
  const exitId = params.get("exit") ?? "";
  const nodeId = params.get("node") ?? "";
  const entry = entries.find((node) => node.id === entryId);
  const visible = chains.filter((chain) => (!entryId || chain.entry_node_id === entryId) && (!exitId || chain.exit_node_id === exitId) && (!nodeId || chain.entry_node_id === nodeId || chain.exit_node_id === nodeId));
  const invalidEntry = Boolean(entryId && !entry);
  const exitFilter = nodes.find((node) => node.id === exitId);
  const nodeFilter = nodes.find((node) => node.id === nodeId);
  const assignedPlans = (chain: NodeChain) => plans.filter((plan) => chain.group_ids.includes(plan.node_group_id));

  const toggle = async (chain: NodeChain) => {
    setBusy(chain.id); setError("");
    try { await updateNodeChain(chain.id, { enabled: !chain.enabled }); await refresh(); }
    catch (cause) { setError(adminError(cause, t("admin.nodeChains.error"))); }
    finally { setBusy(""); }
  };
  if (loading) return <ContentLayout header={<Header variant="h1">{t("admin.routeManagement.title")}</Header>}><Spinner /></ContentLayout>;

  return <ContentLayout header={<Header variant="h1" counter={`(${visible.length})`} description={t("admin.routeManagement.description")}
    actions={<SpaceBetween direction="horizontal" size="xs">
      <Button iconName="refresh" ariaLabel={t("admin.nodes.refresh")} onClick={() => void refresh()} />
      <Button onClick={() => setEditor("create")} disabled={!entries.length || !exits.length}>{t("admin.nodeChains.create")}</Button>
      <Button variant="primary" onClick={() => setBatch(true)} disabled={!entry || !exits.some((node) => node.id !== entry.id)}>{t("admin.routeManagement.addExits")}</Button>
    </SpaceBetween>}>{t("admin.routeManagement.title")}</Header>}>
    <SpaceBetween size="l">
      {error && <Alert type="error" dismissible onDismiss={() => setError("")}>{error}</Alert>}
      {(!entries.length || !exits.length) && <Alert type="info">{t("admin.nodeChains.setupHint")} <Link to="/admin/nodes">{t("admin.nav.nodes")}</Link></Alert>}
      <div className="intrinsic-table route-table">
        <Table items={visible} trackBy="id" wrapLines={false} resizableColumns={false} ariaLabels={{ tableLabel: t("admin.routeManagement.routes") }}
        filter={<div className="route-table__filter">
          <div className="route-table__select">
            <FormField label={t("admin.routeManagement.entryScope")}>
              <Select selectedOption={entry ? { value: entry.id, label: entry.name } : invalidEntry ? { value: entryId, label: t("admin.routeManagement.unavailableFilter", { id: entryId }) } : { value: "", label: t("admin.routeManagement.allEntries") }}
                options={[{ value: "", label: t("admin.routeManagement.allEntries") }, ...entries.map((node) => ({ value: node.id, label: node.name, description: `${node.entry_hostname ?? node.ip} · ${t(`admin.nodes.status${node.status === "online" ? "Online" : "Offline"}`)}` }))]}
                onChange={({ detail }) => setParams((current) => { current.delete("exit"); current.delete("node"); if (detail.selectedOption.value) current.set("entry", detail.selectedOption.value); else current.delete("entry"); return current; })} />
            </FormField>
          </div>
          {entry && <div className="route-table__filter-context">
            <NodeEndpointState node={entry} kind="dns" />
            <Link to={`/admin/nodes/${entry.id}`}>{t("admin.routeManagement.serverDetails")}</Link>
          </div>}
          {(exitId || nodeId || invalidEntry) && <div className="route-table__filter-context">
            <Box color="text-body-secondary">
              {[
                invalidEntry && t("admin.routeManagement.unavailableFilter", { id: entryId }),
                exitId && (exitFilter ? t("admin.routeManagement.exitFilter", { name: exitFilter.name }) : t("admin.routeManagement.unavailableFilter", { id: exitId })),
                nodeId && (nodeFilter ? t("admin.routeManagement.nodeFilter", { name: nodeFilter.name }) : t("admin.routeManagement.unavailableFilter", { id: nodeId })),
              ].filter(Boolean).join(" · ")}
            </Box>
            <Button onClick={() => setParams({})}>{t("admin.nodes.clearFilters")}</Button>
          </div>}
        </div>}
        empty={<Box textAlign="center">{t("admin.routeManagement.empty")}</Box>}
        columnDefinitions={[
          { id: "route", header: t("admin.nodeChains.name"), cell: (item) => <Box variant="strong">{item.name}</Box> },
          { id: "entry", header: t("admin.routeManagement.publicEndpoint"), cell: (item) => (
            <SpaceBetween size="xxxs">
              <Box>{item.entry_node_name ?? item.relay_pool_name ?? t("admin.routeManagement.direct")}</Box>
              <Box variant="small" color="text-body-secondary">
                {item.entry_node_id || item.relay_pool_id ? item.entry_host : nodes.find((node) => node.id === item.exit_node_id)?.ip ?? "—"}
              </Box>
            </SpaceBetween>
          ) },
          { id: "port", header: t("admin.routeManagement.ports"), cell: (item) => (
            item.entry_node_id || item.relay_pool_id
              ? `${item.entry_port ?? "—"} → ${item.exit_port}`
              : String(item.exit_port)
          ) },
          { id: "transport", header: t("admin.routeManagement.transport"), cell: (item) => t(`admin.nodeChains.${item.transport}`) },
          { id: "exit", header: t("admin.routeManagement.destination"), cell: (item) => (
            <Link to={`/admin/nodes/${item.exit_node_id}`}>{item.exit_node_name}</Link>
          ) },
          { id: "plans", header: t("admin.routeManagement.providedPlans"), cell: (item) => <SpaceBetween size="xxs">
            <Box>{assignedPlans(item).map((plan) => plan.name).join(", ") || t("admin.nodeChains.unpublished")}</Box>
            {assignedPlans(item).some((plan) => Number(plan.speed_limit ?? 0) > 0) && <StatusIndicator type={nodes.find((node) => node.id === item.exit_node_id)?.shaping_mode === "device_global_v1" && nodes.find((node) => node.id === item.exit_node_id)?.shaping_ok ? "info" : "warning"}>
              {t(nodes.find((node) => node.id === item.exit_node_id)?.shaping_mode === "device_global_v1" && nodes.find((node) => node.id === item.exit_node_id)?.shaping_ok ? "admin.routeManagement.devicePolicyAssigned" : "admin.routeManagement.deviceAgentRequired")}
            </StatusIndicator>}
          </SpaceBetween> },
          { id: "enabled", header: t("admin.routeManagement.enabledLabel"), cell: (item) => (
            <StatusIndicator type={item.enabled ? "success" : "stopped"}>
              {t(`admin.nodeChains.${item.enabled ? "enabled" : "disabled"}`)}
            </StatusIndicator>
          ) },
          { id: "configuration", header: t("admin.routeManagement.configuration"), cell: (item) => {
            // Enablement and group assignment already have dedicated columns.
            const issues = routeConfigurationIssues(item, nodes).filter(
              (issue) => issue !== "disabled" && issue !== "unassigned",
            );
            return <Popover
              triggerType="custom"
              position="top"
              header={t("admin.routeManagement.configuration")}
              content={<SpaceBetween size="xxs">
                {issues.length ? issues.map((issue) => <StatusIndicator key={issue} type="warning">{t(`admin.routeManagement.issues.${issue}`)}</StatusIndicator>)
                  : <StatusIndicator type="pending">{t("admin.routeManagement.applyUnverified")}</StatusIndicator>}
              </SpaceBetween>}
            >
              <Button variant="inline-link" ariaLabel={t("admin.routeManagement.issueDetails", { count: issues.length })}>
                {issues.length ? t("admin.routeManagement.issueCount", { count: issues.length }) : t("admin.routeManagement.applyUnverified")}
              </Button>
            </Popover>;
          } },
          { id: "actions", header: t("admin.nodeChains.actions"), cell: (item) => <ButtonDropdown
            variant="inline-icon" ariaLabel={`${t("admin.nodeChains.actions")}: ${item.name}`} expandToViewport
            disabled={busy === item.id}
            items={[
              ...((item.entry_node_id || item.relay_pool_id) ? [{ id: "edit", text: t("admin.nodeChains.edit") }] : []),
              { id: "plans", text: t("admin.routeManagement.managePlans") },
              { id: "toggle", text: t(`admin.nodeChains.${item.enabled ? "disable" : "enable"}`) },
            ]}
            onItemClick={({ detail }) => {
              if (detail.id === "edit") setEditor(item);
              if (detail.id === "plans") navigate("/admin/plans");
              if (detail.id === "toggle") void toggle(item);
            }} /> },
        ]} />
      </div>
      {editor && <NodeChainForm key={editor === "create" ? "create" : editor.id} chain={editor === "create" ? undefined : editor} entries={entries} groups={groups} exits={exits}
        onClose={() => setEditor(null)} onSaved={() => { setEditor(null); void refresh(); }} />}
      {batch && entry && <EntryRouteBatchForm entry={entry} exits={exits} plans={plans} onClose={() => setBatch(false)} onSaved={() => { setBatch(false); void refresh(); }} />}
    </SpaceBetween>
  </ContentLayout>;
}
