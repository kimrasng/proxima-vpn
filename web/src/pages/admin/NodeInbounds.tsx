import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  FormField,
  Header,
  Input,
  Modal,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  Toggle,
} from "@cloudscape-design/components";
import {
  listInbounds,
  createInbound,
  toggleInbound,
  deleteInbound,
  getNode,
  getNodeTLSStatus,
  issueNodeCertificate,
  getNodeXrayVersion,
  updateNodeXray,
} from "../../api/admin";
import type { Inbound, CreateInboundRequest, NodeTLSStatus, XrayVersionResponse } from "../../api/types";
import { usePublishBreadcrumbLeaf } from "../../hooks/useBreadcrumbLeaf";

const PROTOCOL_OPTIONS = [
  { value: "vless_reality", label: "VLESS Reality" },
  { value: "vmess_ws", label: "VMess WebSocket" },
  { value: "trojan_tls", label: "Trojan TLS" },
  { value: "shadowsocks", label: "Shadowsocks" },
  { value: "hysteria2", label: "Hysteria2" },
  { value: "wireguard", label: "WireGuard" },
];

const SS_METHOD_OPTIONS = [
  { value: "2022-blake3-aes-128-gcm", label: "2022-blake3-aes-128-gcm" },
  { value: "2022-blake3-aes-256-gcm", label: "2022-blake3-aes-256-gcm" },
  { value: "2022-blake3-chacha20-poly1305", label: "2022-blake3-chacha20-poly1305" },
  { value: "aes-128-gcm", label: "aes-128-gcm" },
  { value: "aes-256-gcm", label: "aes-256-gcm" },
  { value: "chacha20-ietf-poly1305", label: "chacha20-ietf-poly1305" },
];

export default function NodeInbounds() {
  const { nodeId } = useParams<{ nodeId: string }>();
  const { t } = useTranslation();
  const [nodeName, setNodeName] = useState<string | null>(null);
  const [inbounds, setInbounds] = useState<Inbound[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [deleteModal, setDeleteModal] = useState<Inbound | null>(null);
  const [actionLoading, setActionLoading] = useState(false);

  const [tlsStatus, setTlsStatus] = useState<NodeTLSStatus | null>(null);
  const [tlsModal, setTlsModal] = useState(false);
  const [tlsDomain, setTlsDomain] = useState("");
  const [tlsEmail, setTlsEmail] = useState("");
  const [tlsLoading, setTlsLoading] = useState(false);

  const [xrayVersion, setXrayVersion] = useState<XrayVersionResponse | null>(null);
  const [xrayModal, setXrayModal] = useState(false);
  const [xrayTargetVersion, setXrayTargetVersion] = useState("");
  const [xrayLoading, setXrayLoading] = useState(false);

  const [protocol, setProtocol] = useState("vless_reality");
  const [port, setPort] = useState("");
  const [tag, setTag] = useState("");
  const [dest, setDest] = useState("");
  const [serverNames, setServerNames] = useState("");
  const [wsPath, setWsPath] = useState("");
  const [ssMethod, setSsMethod] = useState("2022-blake3-aes-128-gcm");
  const [ssPassword, setSsPassword] = useState("");

  usePublishBreadcrumbLeaf(nodeName);

  // A node serves one protocol, so once an inbound exists the choice is fixed
  // to its protocol; the server rejects anything else with a 409.
  const lockedProtocol = inbounds[0]?.protocol ?? null;

  useEffect(() => {
    if (lockedProtocol) setProtocol(lockedProtocol);
  }, [lockedProtocol]);

  const fetchInbounds = async () => {
    if (!nodeId) return;
    try {
      const data = await listInbounds(nodeId);
      setInbounds(data);
      setError(null);
    } catch {
      setError(t("admin.nodeInbounds.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  const fetchNodeInfo = async () => {
    if (!nodeId) return;
    const [node, tls, xray] = await Promise.allSettled([
      getNode(nodeId),
      getNodeTLSStatus(nodeId),
      getNodeXrayVersion(nodeId),
    ]);
    if (node.status === "fulfilled") setNodeName(node.value.name);
    if (tls.status === "fulfilled") setTlsStatus(tls.value);
    if (xray.status === "fulfilled") setXrayVersion(xray.value);
  };

  useEffect(() => {
    void fetchInbounds();
    void fetchNodeInfo();
    const interval = setInterval(() => {
      void fetchInbounds();
      void fetchNodeInfo();
    }, 30000);
    return () => clearInterval(interval);
  }, [nodeId]);

  const handleIssueCert = async () => {
    if (!nodeId || !tlsDomain || !tlsEmail) return;
    setTlsLoading(true);
    try {
      await issueNodeCertificate(nodeId, { domain: tlsDomain, email: tlsEmail });
      setTlsModal(false);
      setTlsDomain("");
      setTlsEmail("");
      await fetchNodeInfo();
    } catch {
      setError(t("admin.nodeInbounds.tls.error"));
    } finally {
      setTlsLoading(false);
    }
  };

  const handleUpdateXray = async () => {
    if (!nodeId) return;
    setXrayLoading(true);
    try {
      await updateNodeXray(nodeId, { version: xrayTargetVersion || undefined });
      setXrayModal(false);
      setXrayTargetVersion("");
      await fetchNodeInfo();
    } catch {
      setError(t("admin.nodeInbounds.xray.error"));
    } finally {
      setXrayLoading(false);
    }
  };

  const resetForm = () => {
    setProtocol("vless_reality");
    setPort("");
    setTag("");
    setDest("");
    setServerNames("");
    setWsPath("");
    setSsMethod("2022-blake3-aes-128-gcm");
    setSsPassword("");
  };

  const buildSettings = (): Record<string, unknown> => {
    switch (protocol) {
      case "vless_reality":
        return { dest, server_names: serverNames.split(",").map((s) => s.trim()).filter(Boolean) };
      case "vmess_ws":
        return { ws_path: wsPath };
      case "shadowsocks":
        return { method: ssMethod, password: ssPassword };
      default:
        return {};
    }
  };

  const handleCreate = async () => {
    if (!nodeId || !port || !tag) return;
    setActionLoading(true);
    try {
      const req: CreateInboundRequest = {
        protocol,
        port: parseInt(port, 10),
        tag,
        settings: buildSettings(),
      };
      await createInbound(nodeId, req);
      setCreateModal(false);
      resetForm();
      await fetchInbounds();
    } catch {
      setError(t("admin.nodeInbounds.createError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleToggle = async (inbound: Inbound) => {
    try {
      await toggleInbound(inbound.id);
      await fetchInbounds();
    } catch {
      setError(t("admin.nodeInbounds.toggleError"));
    }
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deleteInbound(deleteModal.id);
      setDeleteModal(null);
      await fetchInbounds();
    } catch {
      setError(t("admin.nodeInbounds.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.nodeInbounds.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={nodeName ?? undefined}
        >
          {t("admin.nodeInbounds.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <ColumnLayout columns={2}>
          <Container
            header={
              <Header
                variant="h2"
                actions={
                  <Button onClick={() => setTlsModal(true)}>
                    {tlsStatus?.has_cert ? t("admin.nodeInbounds.tls.renew") : t("admin.nodeInbounds.tls.issue")}
                  </Button>
                }
              >
                {t("admin.nodeInbounds.tls.title")}
              </Header>
            }
          >
            {tlsStatus ? (
              <SpaceBetween size="s">
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodeInbounds.tls.status")}</Box>
                  <StatusIndicator type={tlsStatus.has_cert ? "success" : "warning"}>
                    {tlsStatus.has_cert ? t("admin.nodeInbounds.tls.installed") : t("admin.nodeInbounds.tls.missing")}
                  </StatusIndicator>
                </div>
                {tlsStatus.domain && (
                  <div>
                    <Box variant="awsui-key-label">{t("admin.nodeInbounds.tls.domain")}</Box>
                    <Box>{tlsStatus.domain}</Box>
                  </div>
                )}
                {tlsStatus.cert_file && (
                  <div>
                    <Box variant="awsui-key-label">{t("admin.nodeInbounds.tls.certFile")}</Box>
                    <Box variant="code">{tlsStatus.cert_file}</Box>
                  </div>
                )}
              </SpaceBetween>
            ) : (
              <Box color="text-status-inactive">{t("admin.nodeInbounds.tls.loading")}</Box>
            )}
          </Container>

          <Container
            header={
              <Header
                variant="h2"
                actions={
                  <Button onClick={() => setXrayModal(true)}>
                    {t("admin.nodeInbounds.xray.update")}
                  </Button>
                }
              >
                {t("admin.nodeInbounds.xray.title")}
              </Header>
            }
          >
            {xrayVersion ? (
              <SpaceBetween size="s">
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodeInbounds.xray.current")}</Box>
                  <Box>{xrayVersion.current_version || t("admin.nodeInbounds.xray.unknown")}</Box>
                </div>
              </SpaceBetween>
            ) : (
              <Box color="text-status-inactive">{t("admin.nodeInbounds.tls.loading")}</Box>
            )}
          </Container>
        </ColumnLayout>

        <Table
          header={
            <Header
              actions={
                <Button variant="primary" onClick={() => setCreateModal(true)}>
                  {t("admin.nodeInbounds.add")}
                </Button>
              }
              counter={`(${inbounds.length})`}
            >
              {t("admin.nodeInbounds.tableTitle")}
            </Header>
          }
          items={inbounds}
          columnDefinitions={[
            { id: "protocol", header: t("admin.nodeInbounds.col.protocol"), cell: (item) => item.protocol },
            { id: "port", header: t("admin.nodeInbounds.col.port"), cell: (item) => item.port },
            { id: "tag", header: t("admin.nodeInbounds.col.tag"), cell: (item) => item.tag },
            {
              id: "enabled",
              header: t("admin.nodeInbounds.col.enabled"),
              cell: (item) => (
                <StatusIndicator type={item.enabled ? "success" : "stopped"}>
                  {item.enabled ? t("admin.nodeInbounds.enabled") : t("admin.nodeInbounds.disabled")}
                </StatusIndicator>
              ),
            },
            {
              id: "actions",
              header: t("admin.nodeInbounds.col.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Toggle
                    checked={item.enabled}
                    onChange={() => void handleToggle(item)}
                  />
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>
                    {t("admin.nodeInbounds.delete")}
                  </Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.nodeInbounds.empty")}</Box>}
        />

        <Modal
          visible={createModal}
          onDismiss={() => { setCreateModal(false); resetForm(); }}
          header={t("admin.nodeInbounds.add")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setCreateModal(false); resetForm(); }}>{t("admin.nodeInbounds.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleCreate()}>
                  {t("admin.nodeInbounds.create")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <FormField
              label={t("admin.nodeInbounds.field.protocol")}
              description={
                lockedProtocol
                  ? t("admin.nodeInbounds.field.protocolLocked", { protocol: lockedProtocol })
                  : undefined
              }
            >
              <Select
                selectedOption={PROTOCOL_OPTIONS.find((o) => o.value === protocol) ?? null}
                options={PROTOCOL_OPTIONS}
                disabled={!!lockedProtocol}
                onChange={({ detail }) => setProtocol(detail.selectedOption.value ?? "vless_reality")}
              />
            </FormField>

            <FormField label={t("admin.nodeInbounds.field.port")}>
              <Input
                type="number"
                value={port}
                onChange={({ detail }) => setPort(detail.value)}
                placeholder={t("admin.nodeInbounds.field.portPlaceholder")}
              />
            </FormField>

            <FormField label={t("admin.nodeInbounds.field.tag")}>
              <Input
                value={tag}
                onChange={({ detail }) => setTag(detail.value)}
                placeholder={t("admin.nodeInbounds.field.tagPlaceholder")}
              />
            </FormField>

            {protocol === "vless_reality" && (
              <>
                <FormField label={t("admin.nodeInbounds.field.dest")}>
                  <Input
                    value={dest}
                    onChange={({ detail }) => setDest(detail.value)}
                    placeholder={t("admin.nodeInbounds.field.destPlaceholder")}
                  />
                </FormField>
                <FormField label={t("admin.nodeInbounds.field.serverNames")}>
                  <Input
                    value={serverNames}
                    onChange={({ detail }) => setServerNames(detail.value)}
                    placeholder={t("admin.nodeInbounds.field.serverNamesPlaceholder")}
                  />
                </FormField>
              </>
            )}

            {protocol === "vmess_ws" && (
              <FormField label={t("admin.nodeInbounds.field.wsPath")}>
                <Input
                  value={wsPath}
                  onChange={({ detail }) => setWsPath(detail.value)}
                  placeholder={t("admin.nodeInbounds.field.wsPathPlaceholder")}
                />
              </FormField>
            )}

            {protocol === "shadowsocks" && (
              <>
                <FormField label={t("admin.nodeInbounds.field.ssMethod")}>
                  <Select
                    selectedOption={SS_METHOD_OPTIONS.find((o) => o.value === ssMethod) ?? null}
                    options={SS_METHOD_OPTIONS}
                    onChange={({ detail }) => setSsMethod(detail.selectedOption.value ?? "2022-blake3-aes-128-gcm")}
                  />
                </FormField>
                <FormField label={t("admin.nodeInbounds.field.ssPassword")}>
                  <Input
                    type="password"
                    value={ssPassword}
                    onChange={({ detail }) => setSsPassword(detail.value)}
                    placeholder={t("admin.nodeInbounds.field.ssPasswordPlaceholder")}
                  />
                </FormField>
              </>
            )}
          </SpaceBetween>
        </Modal>

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header={t("admin.nodeInbounds.deleteTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.nodeInbounds.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.nodeInbounds.delete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.nodeInbounds.deleteConfirm", { tag: deleteModal?.tag ?? "", port: deleteModal?.port ?? "" })}
        </Modal>

        <Modal
          visible={tlsModal}
          onDismiss={() => { setTlsModal(false); setTlsDomain(""); setTlsEmail(""); }}
          header={t("admin.nodeInbounds.tls.modalTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setTlsModal(false); setTlsDomain(""); setTlsEmail(""); }}>{t("admin.nodeInbounds.cancel")}</Button>
                <Button
                  variant="primary"
                  loading={tlsLoading}
                  disabled={!tlsDomain || !tlsEmail}
                  onClick={() => void handleIssueCert()}
                >
                  {t("admin.nodeInbounds.tls.issue")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <FormField label={t("admin.nodeInbounds.tls.domain")} description={t("admin.nodeInbounds.tls.domainHint")}>
              <Input
                value={tlsDomain}
                onChange={({ detail }) => setTlsDomain(detail.value)}
                placeholder={t("admin.nodeInbounds.tls.domainPlaceholder")}
              />
            </FormField>
            <FormField label={t("admin.nodeInbounds.tls.email")} description={t("admin.nodeInbounds.tls.emailHint")}>
              <Input
                type="email"
                value={tlsEmail}
                onChange={({ detail }) => setTlsEmail(detail.value)}
                placeholder={t("admin.nodeInbounds.tls.emailPlaceholder")}
              />
            </FormField>
          </SpaceBetween>
        </Modal>

        <Modal
          visible={xrayModal}
          onDismiss={() => { setXrayModal(false); setXrayTargetVersion(""); }}
          header={t("admin.nodeInbounds.xray.modalTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setXrayModal(false); setXrayTargetVersion(""); }}>{t("admin.nodeInbounds.cancel")}</Button>
                <Button
                  variant="primary"
                  loading={xrayLoading}
                  onClick={() => void handleUpdateXray()}
                >
                  {t("admin.nodeInbounds.xray.submit")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <FormField
              label={t("admin.nodeInbounds.xray.targetVersion")}
              description={t("admin.nodeInbounds.xray.targetHint")}
            >
              <Input
                value={xrayTargetVersion}
                onChange={({ detail }) => setXrayTargetVersion(detail.value)}
                placeholder={t("admin.nodeInbounds.xray.targetPlaceholder")}
              />
            </FormField>
          </SpaceBetween>
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
