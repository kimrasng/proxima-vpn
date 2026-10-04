import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Box, Button, Checkbox, ExpandableSection, FormField, Icon, Input, Modal, Multiselect, Select, SpaceBetween, StatusIndicator } from "@cloudscape-design/components";
import { createNodeChain, deleteNodeChain, setNodeChainGroups, updateNodeChain } from "../../api/admin";
import type { ChainTransport, Node, NodeChain, NodeGroup } from "../../api/types";
import { adminError } from "../../utils/adminError";
import { NodeEndpointState } from "../../components/NodeEndpointState";
import "./nodeChainForm.css";

type Props = {
  chain?: NodeChain;
  entries: Node[];
  groups: NodeGroup[];
  exits: Node[];
  onClose: () => void;
  onSaved: () => void;
};

export default function NodeChainForm({ chain, entries, groups, exits, onClose, onSaved }: Props) {
  const { t } = useTranslation();
  const [name, setName] = useState(chain?.name ?? "");
  const [nameEdited, setNameEdited] = useState(Boolean(chain));
  const [entryId, setEntryId] = useState(chain?.entry_node_id ?? "");
  const [exitId, setExitId] = useState(chain?.exit_node_id ?? "");
  const [host, setHost] = useState(chain?.entry_host ?? "");
  const [entryPort, setEntryPort] = useState(chain?.entry_port ? String(chain.entry_port) : "");
  const [exitPort, setExitPort] = useState(chain?.exit_port ? String(chain.exit_port) : "");
  const [transport, setTransport] = useState<ChainTransport>(chain?.transport ?? "tcp");
  const [priority, setPriority] = useState(String(chain?.priority ?? 0));
  const [enabled, setEnabled] = useState(chain?.enabled ?? true);
  const [groupIds, setGroupIds] = useState<string[]>(chain?.group_ids ?? []);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const selectedEntry = entries.find((node) => node.id === entryId);
  const selectedExit = exits.find((node) => node.id === exitId);
  const suggestedName = selectedEntry && selectedExit ? `${selectedEntry.name} → ${selectedExit.name}` : "";
  const displayName = nameEdited ? name : suggestedName;
  const legacyPool = Boolean(chain?.relay_pool_id);
  const entryHost = legacyPool ? host : (selectedEntry?.entry_hostname ?? "");
  const port = Number(entryPort || 0);
  const destinationPort = Number(exitPort || selectedExit?.port || 0);
  const rank = Number(priority);
  const valid = (Boolean(chain) && !legacyPool || entryHost.trim().length > 0) && (chain?.relay_pool_id || entryId !== "") && exitId !== "" && entryId !== exitId &&
    Number.isInteger(port) && port >= 0 && port <= 65535 &&
    Number.isInteger(destinationPort) && destinationPort > 0 && destinationPort <= 65535 &&
    Number.isInteger(rank);

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      if (chain) {
        await updateNodeChain(chain.id, { name: displayName.trim(), ...(legacyPool ? { entry_host: entryHost.trim() } : {}), priority: rank, enabled });
        await setNodeChainGroups(chain.id, groupIds);
      } else {
        const created = await createNodeChain({
          name: displayName.trim(), entry_node_id: entryId, entry_port: port,
          exit_node_id: exitId, exit_port: destinationPort, transport, priority: rank,
        });
        try {
          await setNodeChainGroups(created.id, groupIds);
        } catch (cause) {
          await deleteNodeChain(created.id);
          setError(adminError(cause, t("admin.nodeChains.error")));
          return;
        }
      }
      onSaved();
    } catch (cause) {
      setError(adminError(cause, t("admin.nodeChains.error")));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!chain) return;
    setBusy(true);
    setError("");
    try {
      await deleteNodeChain(chain.id);
      onSaved();
    } catch (cause) {
      setError(adminError(cause, t("admin.nodeChains.error")));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal visible size="large" header={t(chain ? "admin.nodeChains.edit" : "admin.nodeChains.create")}
      onDismiss={onClose} footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
        {chain && <Button onClick={() => setConfirmDelete(true)} disabled={busy}>{t("admin.nodeChains.delete")}</Button>}
        <Button variant="link" onClick={onClose} disabled={busy}>{t("admin.nodeChains.cancel")}</Button>
        <Button variant="primary" loading={busy} disabled={!valid} onClick={() => void save()}>{t(chain ? "admin.nodeChains.save" : "admin.nodeChains.create")}</Button>
      </SpaceBetween></Box>}>
      <div className="node-chain-form"><SpaceBetween size="m">
        {!chain && <Box color="text-body-secondary">{t("admin.nodeChains.formIntro")}</Box>}
        {error && <Alert type="error">{error}</Alert>}
        {confirmDelete ? <Alert type="warning" action={<Button onClick={() => void remove()} loading={busy}>{t("admin.nodeChains.confirmDelete")}</Button>}>{t("admin.nodeChains.deleteWarning", { name: chain?.name })}</Alert> : null}
        {chain && <Alert type="info">{t("admin.nodeChains.immutableHint")}</Alert>}
        {chain?.relay_pool_id && <Alert type="info">{t("admin.nodeChains.legacyPool", { name: chain.relay_pool_name })}</Alert>}
        <section className="node-chain-form__section" aria-label={t("admin.nodeChains.routeTitle")}>
          <Box variant="h3" fontSize="body-s" fontWeight="bold" padding="n" margin="n">{t("admin.nodeChains.routeTitle")}</Box>
          <div className="node-chain-form__route">
            <div className="node-chain-form__endpoint">
              <SpaceBetween size="xs">
                <FormField stretch label={t("admin.nodeChains.entryNode")}>
                  <Select disabled={Boolean(chain)} selectedOption={selectedEntry ? { value: entryId, label: selectedEntry.name } : null}
                    placeholder={t("admin.nodeChains.chooseEntry")}
                    options={entries.filter((node) => node.id !== exitId).map((node) => ({ value: node.id, label: node.name, description: `${node.ip} · ${node.status}` }))}
                    onChange={({ detail }) => setEntryId(detail.selectedOption.value ?? "")} />
                </FormField>
                {selectedEntry && <Box variant="small" color="text-body-secondary">
                  <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
                    <StatusIndicator type={selectedEntry.status === "online" ? "success" : "error"}>{t(`admin.nodes.status${selectedEntry.status === "online" ? "Online" : "Offline"}`)}</StatusIndicator>
                    <span>{selectedEntry.ip.split("/")[0]}</span>
                    {(selectedEntry.region || selectedEntry.country) && <span>· {[selectedEntry.region, selectedEntry.country].filter(Boolean).join(", ")}</span>}
                  </SpaceBetween>
                </Box>}
              </SpaceBetween>
            </div>
            <span className="node-chain-form__arrow" aria-hidden="true"><Icon name="arrow-right" variant="subtle" /></span>
            <div className="node-chain-form__endpoint">
              <SpaceBetween size="xs">
                <FormField stretch label={t("admin.nodeChains.exit")}>
                  <Select disabled={Boolean(chain)} selectedOption={selectedExit ? { value: exitId, label: selectedExit.name } : null}
                    placeholder={t("admin.nodeChains.chooseExit")}
                    options={exits.filter((node) => node.id !== entryId).map((node) => ({ value: node.id, label: node.name, description: `${node.ip}:${node.port} · ${node.status}` }))}
                    onChange={({ detail }) => setExitId(detail.selectedOption.value ?? "")} />
                </FormField>
                {selectedExit && <Box variant="small" color="text-body-secondary">
                  <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
                    <StatusIndicator type={selectedExit.status === "online" ? "success" : "error"}>{t(`admin.nodes.status${selectedExit.status === "online" ? "Online" : "Offline"}`)}</StatusIndicator>
                    <span>{selectedExit.ip.split("/")[0]}</span>
                    {(selectedExit.region || selectedExit.country) && <span>· {[selectedExit.region, selectedExit.country].filter(Boolean).join(", ")}</span>}
                  </SpaceBetween>
                </Box>}
              </SpaceBetween>
            </div>
          </div>
        </section>
        <section className="node-chain-form__section" aria-label={t("admin.nodeChains.detailsTitle")}>
          <Box variant="h3" fontSize="body-s" fontWeight="bold" padding="n" margin="n">{t("admin.nodeChains.detailsTitle")}</Box>
          <SpaceBetween size="s">
            <FormField stretch label={t("admin.nodeChains.name")}>
              <Input value={displayName} onChange={({ detail }) => { setName(detail.value); setNameEdited(true); }} />
            </FormField>
            <div className="node-chain-form__details">
              <FormField stretch label={t("admin.nodeChains.host")} constraintText={t(legacyPool ? "admin.nodeChains.legacyHostHint" : "admin.nodeChains.hostHint")} errorText={legacyPool ? (!entryHost.trim() ? t("admin.nodeChains.hostRequired") : undefined) : ((selectedEntry || chain) && !entryHost.trim() ? t("admin.nodeChains.managedHostMissing") : undefined)}>
                {legacyPool ? <Input value={entryHost} onChange={({ detail }) => setHost(detail.value)} placeholder="relay.example.com" />
                  : selectedEntry ? <NodeEndpointState node={selectedEntry} kind="dns" /> : <Box color="text-body-secondary">—</Box>}
              </FormField>
              <FormField stretch label={t("admin.nodeChains.exitPort")} constraintText={t("admin.nodeChains.exitPortHint")}>
                <Input disabled={Boolean(chain)} value={exitPort || (selectedExit ? String(selectedExit.port) : "")} type="number" onChange={({ detail }) => setExitPort(detail.value)} />
              </FormField>
            </div>
            {chain && <Checkbox checked={enabled} onChange={({ detail }) => setEnabled(detail.checked)}>{t("admin.nodeChains.enabled")}</Checkbox>}
          </SpaceBetween>
        </section>
        <section className="node-chain-form__section" aria-label={t("admin.nodeChains.accessTitle")}>
          <FormField stretch label={t("admin.nodeChains.groups")} constraintText={t("admin.nodeChains.groupsHint")}>
            <Multiselect selectedOptions={groups.filter((group) => groupIds.includes(group.id)).map((group) => ({ value: group.id, label: group.name }))}
              options={groups.map((group) => ({ value: group.id, label: group.name }))}
              placeholder={t("admin.nodeChains.chooseGroups")}
              onChange={({ detail }) => setGroupIds(detail.selectedOptions.map((option) => option.value).filter((value): value is string => Boolean(value)))} />
          </FormField>
        </section>
        <ExpandableSection variant="default" headingTagOverride="h3" headerText={t("admin.nodeChains.advanced")}>
            <div className="node-chain-form__advanced">
              <FormField stretch label={t("admin.nodeChains.entryPort")} constraintText={t("admin.nodeChains.autoPort")}>
                <Input disabled={Boolean(chain)} value={entryPort} type="number" onChange={({ detail }) => setEntryPort(detail.value)} />
              </FormField>
              <FormField stretch label={t("admin.nodeChains.transport")}>
                <Select disabled={Boolean(chain)} selectedOption={{ value: transport, label: t(`admin.nodeChains.${transport}`) }}
                  options={(["tcp", "udp", "tcp_udp"] as const).map((value) => ({ value, label: t(`admin.nodeChains.${value}`) }))}
                  onChange={({ detail }) => { const value = detail.selectedOption.value; if (value === "tcp" || value === "udp" || value === "tcp_udp") setTransport(value); }} />
              </FormField>
              <FormField stretch label={t("admin.nodeChains.priority")}><Input value={priority} type="number" onChange={({ detail }) => setPriority(detail.value)} /></FormField>
            </div>
        </ExpandableSection>
      </SpaceBetween></div>
    </Modal>
  );
}
