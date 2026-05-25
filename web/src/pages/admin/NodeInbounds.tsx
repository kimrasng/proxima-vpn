import { useEffect, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
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
  getNodeTLSStatus,
  issueNodeCertificate,
  getNodeXrayVersion,
  updateNodeXray,
} from "../../api/admin";
import type { Inbound, CreateInboundRequest, NodeTLSStatus, XrayVersionResponse } from "../../api/types";

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
  const navigate = useNavigate();
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

  const fetchInbounds = async () => {
    if (!nodeId) return;
    try {
      const data = await listInbounds(nodeId);
      setInbounds(data);
      setError(null);
    } catch {
      setError("Failed to fetch inbounds");
    } finally {
      setLoading(false);
    }
  };

  const fetchNodeInfo = async () => {
    if (!nodeId) return;
    const [tls, xray] = await Promise.allSettled([
      getNodeTLSStatus(nodeId),
      getNodeXrayVersion(nodeId),
    ]);
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
      setError("Failed to issue certificate");
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
      setError("Failed to trigger Xray update");
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
      setError("Failed to create inbound");
    } finally {
      setActionLoading(false);
    }
  };

  const handleToggle = async (inbound: Inbound) => {
    try {
      await toggleInbound(inbound.id);
      await fetchInbounds();
    } catch {
      setError("Failed to toggle inbound");
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
      setError("Failed to delete inbound");
    } finally {
      setActionLoading(false);
    }
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">Node Inbounds</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          actions={
            <Button variant="link" onClick={() => navigate("/admin/nodes")}>
              ← Back to Nodes
            </Button>
          }
        >
          Node Inbounds
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
                    {tlsStatus?.has_cert ? "Renew Certificate" : "Issue Certificate"}
                  </Button>
                }
              >
                TLS Certificate
              </Header>
            }
          >
            {tlsStatus ? (
              <SpaceBetween size="s">
                <div>
                  <Box variant="awsui-key-label">Status</Box>
                  <StatusIndicator type={tlsStatus.has_cert ? "success" : "warning"}>
                    {tlsStatus.has_cert ? "Certificate installed" : "No certificate"}
                  </StatusIndicator>
                </div>
                {tlsStatus.domain && (
                  <div>
                    <Box variant="awsui-key-label">Domain</Box>
                    <Box>{tlsStatus.domain}</Box>
                  </div>
                )}
                {tlsStatus.cert_file && (
                  <div>
                    <Box variant="awsui-key-label">Cert file</Box>
                    <Box variant="code">{tlsStatus.cert_file}</Box>
                  </div>
                )}
              </SpaceBetween>
            ) : (
              <Box color="text-status-inactive">Loading…</Box>
            )}
          </Container>

          <Container
            header={
              <Header
                variant="h2"
                actions={
                  <Button onClick={() => setXrayModal(true)}>
                    Update Xray
                  </Button>
                }
              >
                Xray Version
              </Header>
            }
          >
            {xrayVersion ? (
              <SpaceBetween size="s">
                <div>
                  <Box variant="awsui-key-label">Current version</Box>
                  <Box>{xrayVersion.current_version || "Unknown"}</Box>
                </div>
              </SpaceBetween>
            ) : (
              <Box color="text-status-inactive">Loading…</Box>
            )}
          </Container>
        </ColumnLayout>

        <Table
          header={
            <Header
              actions={
                <Button variant="primary" onClick={() => setCreateModal(true)}>
                  Add Inbound
                </Button>
              }
              counter={`(${inbounds.length})`}
            >
              Inbounds
            </Header>
          }
          items={inbounds}
          columnDefinitions={[
            { id: "protocol", header: "Protocol", cell: (item) => item.protocol },
            { id: "port", header: "Port", cell: (item) => item.port },
            { id: "tag", header: "Tag", cell: (item) => item.tag },
            {
              id: "enabled",
              header: "Enabled",
              cell: (item) => (
                <StatusIndicator type={item.enabled ? "success" : "stopped"}>
                  {item.enabled ? "Enabled" : "Disabled"}
                </StatusIndicator>
              ),
            },
            {
              id: "actions",
              header: "Actions",
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Toggle
                    checked={item.enabled}
                    onChange={() => void handleToggle(item)}
                  />
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>
                    Delete
                  </Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">No inbounds configured for this node.</Box>}
        />

        <Modal
          visible={createModal}
          onDismiss={() => { setCreateModal(false); resetForm(); }}
          header="Add Inbound"
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setCreateModal(false); resetForm(); }}>Cancel</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleCreate()}>
                  Create
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <FormField label="Protocol">
              <Select
                selectedOption={PROTOCOL_OPTIONS.find((o) => o.value === protocol) ?? null}
                options={PROTOCOL_OPTIONS}
                onChange={({ detail }) => setProtocol(detail.selectedOption.value ?? "vless_reality")}
              />
            </FormField>

            <FormField label="Port">
              <Input
                type="number"
                value={port}
                onChange={({ detail }) => setPort(detail.value)}
                placeholder="e.g. 443"
              />
            </FormField>

            <FormField label="Tag">
              <Input
                value={tag}
                onChange={({ detail }) => setTag(detail.value)}
                placeholder="e.g. vless-in"
              />
            </FormField>

            {protocol === "vless_reality" && (
              <>
                <FormField label="Destination (dest)">
                  <Input
                    value={dest}
                    onChange={({ detail }) => setDest(detail.value)}
                    placeholder="e.g. www.google.com:443"
                  />
                </FormField>
                <FormField label="Server Names (comma-separated)">
                  <Input
                    value={serverNames}
                    onChange={({ detail }) => setServerNames(detail.value)}
                    placeholder="e.g. www.google.com,google.com"
                  />
                </FormField>
              </>
            )}

            {protocol === "vmess_ws" && (
              <FormField label="WebSocket Path">
                <Input
                  value={wsPath}
                  onChange={({ detail }) => setWsPath(detail.value)}
                  placeholder="e.g. /ws"
                />
              </FormField>
            )}

            {protocol === "shadowsocks" && (
              <>
                <FormField label="Method">
                  <Select
                    selectedOption={SS_METHOD_OPTIONS.find((o) => o.value === ssMethod) ?? null}
                    options={SS_METHOD_OPTIONS}
                    onChange={({ detail }) => setSsMethod(detail.selectedOption.value ?? "2022-blake3-aes-128-gcm")}
                  />
                </FormField>
                <FormField label="Password">
                  <Input
                    type="password"
                    value={ssPassword}
                    onChange={({ detail }) => setSsPassword(detail.value)}
                    placeholder="Password"
                  />
                </FormField>
              </>
            )}
          </SpaceBetween>
        </Modal>

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header="Delete Inbound"
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>Cancel</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  Delete
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          Are you sure you want to delete the inbound "{deleteModal?.tag}" (port {deleteModal?.port})?
        </Modal>

        <Modal
          visible={tlsModal}
          onDismiss={() => { setTlsModal(false); setTlsDomain(""); setTlsEmail(""); }}
          header="Issue TLS Certificate"
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setTlsModal(false); setTlsDomain(""); setTlsEmail(""); }}>Cancel</Button>
                <Button
                  variant="primary"
                  loading={tlsLoading}
                  disabled={!tlsDomain || !tlsEmail}
                  onClick={() => void handleIssueCert()}
                >
                  Issue Certificate
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <FormField label="Domain" description="The domain name to issue a certificate for">
              <Input
                value={tlsDomain}
                onChange={({ detail }) => setTlsDomain(detail.value)}
                placeholder="e.g. vpn.example.com"
              />
            </FormField>
            <FormField label="Email" description="Email address for Let's Encrypt notifications">
              <Input
                type="email"
                value={tlsEmail}
                onChange={({ detail }) => setTlsEmail(detail.value)}
                placeholder="e.g. admin@example.com"
              />
            </FormField>
          </SpaceBetween>
        </Modal>

        <Modal
          visible={xrayModal}
          onDismiss={() => { setXrayModal(false); setXrayTargetVersion(""); }}
          header="Update Xray"
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setXrayModal(false); setXrayTargetVersion(""); }}>Cancel</Button>
                <Button
                  variant="primary"
                  loading={xrayLoading}
                  onClick={() => void handleUpdateXray()}
                >
                  Update
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <FormField
              label="Target version"
              description="Leave blank to update to the latest version"
            >
              <Input
                value={xrayTargetVersion}
                onChange={({ detail }) => setXrayTargetVersion(detail.value)}
                placeholder="e.g. v1.8.4 (leave blank for latest)"
              />
            </FormField>
          </SpaceBetween>
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
