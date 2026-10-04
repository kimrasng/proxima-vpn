import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Alert, Box, Button, ColumnLayout, Container, ContentLayout, FormField, Header, Select, SpaceBetween, Spinner, StatusIndicator, Table } from "@cloudscape-design/components";
import { listNodeChains, listNodeGroups, listNodes, listPlans, updateNodeChain } from "../../api/admin";
import type { Node, NodeChain, NodeGroup, Plan } from "../../api/types";
import { adminError } from "../../utils/adminError";
import { routeConfigurationIssues } from "../../utils/routeConfiguration";
import { NodeEndpointState } from "../../components/NodeEndpointState";
import NodeChainForm from "./NodeChainForm";
import EntryRouteBatchForm from "./EntryRouteBatchForm";

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
  const filterNode = nodes.find((node) => node.id === (nodeId || exitId));
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
      <Container header={<Header variant="h2">{t("admin.routeManagement.portMap")}</Header>}>
        <SpaceBetween size="m">
          <ColumnLayout columns={2}>
            <FormField label={t("admin.nodeChains.entryNode")}>
              <Select selectedOption={entry ? { value: entry.id, label: entry.name } : invalidEntry ? { value: entryId, label: t("admin.routeManagement.unavailableFilter", { id: entryId }) } : { value: "", label: t("admin.routeManagement.allEntries") }}
                options={[{ value: "", label: t("admin.routeManagement.allEntries") }, ...entries.map((node) => ({ value: node.id, label: node.name, description: `${node.entry_hostname ?? node.ip} · ${t(`admin.nodes.status${node.status === "online" ? "Online" : "Offline"}`)}` }))]}
                onChange={({ detail }) => setParams((current) => { current.delete("exit"); current.delete("node"); if (detail.selectedOption.value) current.set("entry", detail.selectedOption.value); else current.delete("entry"); return current; })} />
            </FormField>
            {entry ? <SpaceBetween size="xs"><NodeEndpointState node={entry} kind="dns" /><Link to={`/admin/nodes/${entry.id}`}>{t("admin.routeManagement.serverDetails")}</Link></SpaceBetween>
              : <Box color="text-body-secondary">{t("admin.routeManagement.selectEntryHint")}</Box>}
          </ColumnLayout>
          {(exitId || nodeId || invalidEntry) && <Alert type="info" action={<Button onClick={() => setParams({})}>{t("admin.nodes.clearFilters")}</Button>}>
            {filterNode ? t("admin.routeManagement.nodeFilter", { name: filterNode.name }) : t("admin.routeManagement.unavailableFilter", { id: nodeId || exitId || entryId })}
          </Alert>}
          <Box variant="small" color="text-body-secondary">{t("admin.routeManagement.configurationHint")}</Box>
        </SpaceBetween>
      </Container>
      <Table items={visible} trackBy="id" wrapLines ariaLabels={{ tableLabel: t("admin.routeManagement.portMap") }}
        empty={<Box textAlign="center">{t("admin.routeManagement.empty")}</Box>}
        columnDefinitions={[
          { id: "name", header: t("admin.nodeChains.name"), cell: (item) => item.name },
          { id: "entry", header: t("admin.routeManagement.publicEndpoint"), cell: (item) => <>
            <Box>{item.entry_node_name ?? item.relay_pool_name ?? t("admin.routeManagement.direct")}</Box>
            <Box variant="small" color="text-body-secondary">{item.entry_node_id || item.relay_pool_id ? `${item.entry_host}:${item.entry_port ?? "—"}` : `${nodes.find((node) => node.id === item.exit_node_id)?.ip ?? "—"}:${item.exit_port}`}</Box>
          </> },
          { id: "exit", header: t("admin.routeManagement.destination"), cell: (item) => <>
            <Link to={`/admin/nodes/${item.exit_node_id}`}>{item.exit_node_name}</Link>
            <Box variant="small" color="text-body-secondary">{t("admin.nodeChains.exitPort")}: {item.exit_port} · {t(`admin.nodeChains.${item.transport}`)}</Box>
          </> },
          { id: "plans", header: t("admin.routeManagement.providedPlans"), cell: (item) => <SpaceBetween size="xxs">
            <Box>{assignedPlans(item).map((plan) => plan.name).join(", ") || t("admin.nodeChains.unpublished")}</Box>
            {assignedPlans(item).some((plan) => Number(plan.speed_limit ?? 0) > 0) && <StatusIndicator type={nodes.find((node) => node.id === item.exit_node_id)?.shaping_mode === "device_global_v1" && nodes.find((node) => node.id === item.exit_node_id)?.shaping_ok ? "info" : "warning"}>
              {t(nodes.find((node) => node.id === item.exit_node_id)?.shaping_mode === "device_global_v1" && nodes.find((node) => node.id === item.exit_node_id)?.shaping_ok ? "admin.routeManagement.devicePolicyAssigned" : "admin.routeManagement.deviceAgentRequired")}
            </StatusIndicator>}
          </SpaceBetween> },
          { id: "configuration", header: t("admin.routeManagement.configuration"), cell: (item) => {
            const issues = routeConfigurationIssues(item, nodes);
            return <SpaceBetween size="xxs">{issues.length ? issues.map((issue) => <StatusIndicator key={issue} type="warning">{t(`admin.routeManagement.issues.${issue}`)}</StatusIndicator>)
              : <StatusIndicator type="pending">{t("admin.routeManagement.applyUnverified")}</StatusIndicator>}</SpaceBetween>;
          } },
          { id: "status", header: t("admin.routeManagement.enabledLabel"), cell: (item) => <StatusIndicator type={item.enabled ? "success" : "stopped"}>{t(`admin.nodeChains.${item.enabled ? "enabled" : "disabled"}`)}</StatusIndicator> },
          { id: "actions", header: t("admin.nodeChains.actions"), cell: (item) => <SpaceBetween direction="horizontal" size="xs">
            {(item.entry_node_id || item.relay_pool_id) && <Button variant="inline-link" onClick={() => setEditor(item)}>{t("admin.nodeChains.edit")}</Button>}
            <Button variant="inline-link" onClick={() => navigate("/admin/plans")}>{t("admin.routeManagement.managePlans")}</Button>
            <Button variant="inline-link" loading={busy === item.id} onClick={() => void toggle(item)}>{t(`admin.nodeChains.${item.enabled ? "disable" : "enable"}`)}</Button>
          </SpaceBetween> },
        ]} />
      {editor && <NodeChainForm key={editor === "create" ? "create" : editor.id} chain={editor === "create" ? undefined : editor} entries={entries} groups={groups} exits={exits}
        onClose={() => setEditor(null)} onSaved={() => { setEditor(null); void refresh(); }} />}
      {batch && entry && <EntryRouteBatchForm entry={entry} exits={exits} plans={plans} onClose={() => setBatch(false)} onSaved={() => { setBatch(false); void refresh(); }} />}
    </SpaceBetween>
  </ContentLayout>;
}
