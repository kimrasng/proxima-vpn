import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Box, Button, FormField, Input, Modal, Multiselect, Select, SpaceBetween, Table } from "@cloudscape-design/components";
import { createNodeChainBatch } from "../../api/admin";
import type { ChainTransport, Node, NodeChain, Plan } from "../../api/types";
import { adminError } from "../../utils/adminError";
import { NodeEndpointState } from "../../components/NodeEndpointState";

type RouteRow = { exitId: string; name: string; entryPort: string; exitPort: string; transport: ChainTransport };
type Props = { entry: Node; exits: Node[]; plans: Plan[]; onClose: () => void; onSaved: () => void };

export default function EntryRouteBatchForm({ entry, exits, plans, onClose, onSaved }: Props) {
  const { t } = useTranslation();
  const submitting = useRef(false);
  const [rows, setRows] = useState<RouteRow[]>([]);
  const [planIds, setPlanIds] = useState<string[]>([]);
  const [preview, setPreview] = useState<NodeChain[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const candidates = exits.filter((node) => node.id !== entry.id);
  const presentPort = (value: string) => /^\d+$/.test(value) && Number(value) >= 1 && Number(value) <= 65535;
  const valid = Boolean(entry.entry_hostname) && rows.length > 0 && rows.every((row) => row.name.trim() && (!row.entryPort || presentPort(row.entryPort)) && presentPort(row.exitPort));
  const update = (id: string, patch: Partial<RouteRow>) => {
    setRows((current) => current.map((row) => row.exitId === id ? { ...row, ...patch } : row));
    setPreview(null);
  };
  const submit = async () => {
    if (submitting.current) return;
    submitting.current = true;
    setBusy(true);
    setError("");
    try {
      const result = await createNodeChainBatch({
        entry_node_id: entry.id,
        plan_ids: planIds,
        preview: !preview,
        routes: rows.map((row, index) => ({
          name: row.name.trim(), exit_node_id: row.exitId, exit_port: Number(row.exitPort),
          entry_port: preview?.[index]?.entry_port ?? Number(row.entryPort || 0), transport: row.transport,
        })),
      });
      if (preview) onSaved();
      else {
        if (result.routes.length !== rows.length || result.routes.some((route, index) => route.exit_node_id !== rows[index]?.exitId || !route.entry_port)) {
          throw new Error(t("admin.routeManagement.invalidPreview"));
        }
        setPreview(result.routes);
      }
    } catch (cause) {
      setPreview(null);
      setError(adminError(cause, t("admin.routeManagement.error")));
    } finally { submitting.current = false; setBusy(false); }
  };

  return <Modal visible size="max" header={t("admin.routeManagement.batchTitle", { name: entry.name })} onDismiss={() => { if (!busy) onClose(); }}
    footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
      <Button disabled={busy} variant="link" onClick={onClose}>{t("admin.nodeChains.cancel")}</Button>
      <Button variant="primary" disabled={!valid} loading={busy} onClick={() => void submit()}>{t(`admin.routeManagement.${preview ? "createBatch" : "previewPorts"}`)}</Button>
    </SpaceBetween></Box>}>
    <SpaceBetween size="m">
      {error && <Alert type="error">{error}</Alert>}
      <NodeEndpointState node={entry} kind="dns" />
      {!entry.entry_hostname && <Alert type="warning">{t("admin.nodeChains.managedHostMissing")}</Alert>}
      {(entry.status !== "online" || rows.some((row) => candidates.find((node) => node.id === row.exitId)?.status !== "online")) && <Alert type="warning">{t("admin.routeManagement.offlineDraft")}</Alert>}
      <FormField label={t("admin.routeManagement.chooseExits")}>
        <Multiselect disabled={busy} selectedOptions={rows.map((row) => ({ value: row.exitId, label: candidates.find((node) => node.id === row.exitId)?.name ?? row.exitId }))}
          options={candidates.map((node) => ({ value: node.id, label: node.name, description: `${node.country} ${node.region} · ${t(`admin.nodes.status${node.status === "online" ? "Online" : "Offline"}`)}` }))}
          onChange={({ detail }) => {
            setRows(detail.selectedOptions.map((option) => {
              const existing = rows.find((row) => row.exitId === option.value);
              const node = candidates.find((candidate) => candidate.id === option.value)!;
              return existing ?? { exitId: node.id, name: `${entry.name} → ${node.name}`, entryPort: "", exitPort: String(node.port), transport: "tcp" };
            }));
            setPreview(null);
          }} />
      </FormField>
      <Table items={rows} trackBy="exitId" wrapLines empty={<Box>{t("admin.routeManagement.chooseExits")}</Box>}
        columnDefinitions={[
          { id: "name", header: t("admin.nodeChains.name"), cell: (row) => <Input disabled={busy} ariaLabel={t("admin.nodeChains.name")} value={row.name} onChange={({ detail }) => update(row.exitId, { name: detail.value })} /> },
          { id: "entry", header: t("admin.nodeChains.entryPort"), cell: (row) => <Input disabled={busy} type="number" ariaLabel={t("admin.nodeChains.entryPort")} value={row.entryPort} placeholder={t("admin.routeManagement.autoPort")} onChange={({ detail }) => update(row.exitId, { entryPort: detail.value })} /> },
          { id: "exit", header: t("admin.nodeChains.exit"), cell: (row) => candidates.find((node) => node.id === row.exitId)?.name },
          { id: "port", header: t("admin.nodeChains.exitPort"), cell: (row) => <Input disabled={busy} type="number" ariaLabel={t("admin.nodeChains.exitPort")} value={row.exitPort} onChange={({ detail }) => update(row.exitId, { exitPort: detail.value })} /> },
          { id: "transport", header: t("admin.nodeChains.transport"), cell: (row) => <Select ariaLabel={`${t("admin.nodeChains.transport")} — ${candidates.find((node) => node.id === row.exitId)?.name ?? row.exitId}`} disabled={busy} selectedOption={{ value: row.transport, label: t(`admin.nodeChains.${row.transport}`) }} options={(["tcp", "udp", "tcp_udp"] as const).map((value) => ({ value, label: t(`admin.nodeChains.${value}`) }))} onChange={({ detail }) => { const value = detail.selectedOption.value; if (value === "tcp" || value === "udp" || value === "tcp_udp") update(row.exitId, { transport: value }); }} /> },
        ]} />
      <FormField label={t("admin.routeManagement.providedPlans")} constraintText={t("admin.routeManagement.plansHint")}>
        <Multiselect disabled={busy} selectedOptions={plans.filter((plan) => planIds.includes(plan.id)).map((plan) => ({ value: plan.id, label: plan.name }))}
          options={plans.map((plan) => ({ value: plan.id, label: plan.name, description: plan.speed_limit ? `${plan.speed_limit} Mbps · ${t("admin.routeManagement.deviceAgentRequired")}` : t("admin.routeManagement.unlimited") }))}
          onChange={({ detail }) => { setPlanIds(detail.selectedOptions.map((option) => option.value!)); setPreview(null); }} />
      </FormField>
      {planIds.some((id) => Number(plans.find((plan) => plan.id === id)?.speed_limit ?? 0) > 0) && <Alert type="warning">{t("admin.routeManagement.devicePolicyWarning")}</Alert>}
      {preview && <Alert type="info" header={t("admin.routeManagement.portPreview")}>
        <SpaceBetween size="xs">
          <Box>{t("admin.routeManagement.previewHint")}</Box>
          {preview.map((route, index) => <Box key={`${route.exit_node_id}-${index}`}>{entry.entry_hostname}:{route.entry_port} → {candidates.find((node) => node.id === route.exit_node_id)?.name}:{route.exit_port} ({route.transport})</Box>)}
        </SpaceBetween>
      </Alert>}
    </SpaceBetween>
  </Modal>;
}
