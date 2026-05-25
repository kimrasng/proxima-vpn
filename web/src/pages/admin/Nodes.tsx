import { useEffect, useState, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
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
  ProgressBar,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  Textarea,
} from "@cloudscape-design/components";
import { listNodes, generateNodeToken, deleteNode, updateNode } from "../../api/admin";
import type { Node, GenerateTokenResponse, UpdateNodeRequest } from "../../api/types";

function formatRelativeTime(dateStr: string | undefined | null): string {
  if (!dateStr) return "Never";
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diffMs = now - then;
  if (diffMs < 0) return "Just now";

  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function getUsageStatus(value: number): "success" | "warning" | "error" {
  if (value < 50) return "success";
  if (value <= 80) return "warning";
  return "error";
}

function getStatusIndicatorType(status: string): "success" | "error" | "pending" {
  switch (status) {
    case "online":
      return "success";
    case "offline":
      return "error";
    default:
      return "pending";
  }
}

export default function Nodes() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [nodes, setNodes] = useState<Node[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tokenModal, setTokenModal] = useState(false);
  const [tokenData, setTokenData] = useState<GenerateTokenResponse | null>(null);
  const [deleteModal, setDeleteModal] = useState<Node | null>(null);
  const [editModal, setEditModal] = useState<Node | null>(null);
  const [metricsModal, setMetricsModal] = useState<Node | null>(null);
  const [editForm, setEditForm] = useState<{ name: string; country: string; region: string }>({ name: "", country: "", region: "" });
  const [editSuccess, setEditSuccess] = useState(false);
  const [actionLoading, setActionLoading] = useState(false);

  const fetchNodes = async () => {
    try {
      const data = await listNodes();
      setNodes(data);
      setError(null);
    } catch {
      setError(t("admin.nodes.fetchError"));
    } finally {
      setLoading(false);
    }
  };

    useEffect(() => {
      void fetchNodes();
      const interval = setInterval(() => void fetchNodes(), 30000);
      return () => clearInterval(interval);
    }, []);

  const healthSummary = useMemo(() => {
    const online = nodes.filter((n) => n.status === "online").length;
    const offline = nodes.filter((n) => n.status === "offline").length;
    const pending = nodes.length - online - offline;
    return { online, offline, pending };
  }, [nodes]);

  const handleGenerateToken = async () => {
    setActionLoading(true);
    try {
      const data = await generateNodeToken();
      setTokenData(data);
      setTokenModal(true);
    } catch {
      setError(t("admin.nodes.tokenError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deleteNode(deleteModal.id);
      setDeleteModal(null);
      await fetchNodes();
    } catch {
      setError(t("admin.nodes.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEditOpen = (node: Node) => {
    setEditModal(node);
    setEditForm({ name: node.name, country: node.country, region: node.region });
  };

  const handleEditSubmit = async () => {
    if (!editModal) return;
    setActionLoading(true);
    try {
      const req: UpdateNodeRequest = {
        name: editForm.name,
        country: editForm.country,
        region: editForm.region,
      };
      await updateNode(editModal.id, req);
      setEditModal(null);
      setEditSuccess(true);
      await fetchNodes();
    } catch {
      setError(t("admin.nodes.editError"));
    } finally {
      setActionLoading(false);
    }
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.nodes.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.nodes.title")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}
        {editSuccess && (
          <Flashbar items={[{ type: "success", content: t("admin.nodes.editSuccess"), dismissible: true, onDismiss: () => setEditSuccess(false) }]} />
        )}

        <Container>
          <ColumnLayout columns={3} variant="text-grid">
            <div>
              <Box variant="awsui-key-label">Online</Box>
              <StatusIndicator type="success">
                {healthSummary.online} {healthSummary.online === 1 ? "node" : "nodes"}
              </StatusIndicator>
            </div>
            <div>
              <Box variant="awsui-key-label">Offline</Box>
              <StatusIndicator type="error">
                {healthSummary.offline} {healthSummary.offline === 1 ? "node" : "nodes"}
              </StatusIndicator>
            </div>
            <div>
              <Box variant="awsui-key-label">Pending</Box>
              <StatusIndicator type="pending">
                {healthSummary.pending} {healthSummary.pending === 1 ? "node" : "nodes"}
              </StatusIndicator>
            </div>
          </ColumnLayout>
        </Container>

        <Table
          header={
            <Header
              actions={
                <Button variant="primary" loading={actionLoading} onClick={() => void handleGenerateToken()}>
                  {t("admin.nodes.generateToken")}
                </Button>
              }
              counter={`(${nodes.length})`}
            >
              {t("admin.nodes.title")}
            </Header>
          }
          items={nodes}
          columnDefinitions={[
            { id: "name", header: t("admin.nodes.col.name"), cell: (item) => (
              <Button variant="inline-link" onClick={() => navigate(`/admin/nodes/${item.id}`)}>
                {item.name}
              </Button>
            ) },
            { id: "country", header: t("admin.nodes.col.country"), cell: (item) => item.country },
            { id: "ip", header: t("admin.nodes.col.ip"), cell: (item) => item.ip },
            {
              id: "status",
              header: t("admin.nodes.col.status"),
              cell: (item) => (
                <StatusIndicator type={getStatusIndicatorType(item.status)}>
                  {item.status}
                </StatusIndicator>
              ),
            },
            {
              id: "cpu",
              header: t("admin.nodes.col.cpu"),
              cell: (item) =>
                item.cpu_usage != null ? (
                  <ProgressBar
                    value={item.cpu_usage}
                    status={getUsageStatus(item.cpu_usage) === "error" ? "error" : "in-progress"}
                    variant="standalone"
                    additionalInfo={`${item.cpu_usage.toFixed(1)}%`}
                  />
                ) : (
                  <Box color="text-status-inactive">—</Box>
                ),
            },
            {
              id: "memory",
              header: t("admin.nodes.col.memory"),
              cell: (item) =>
                item.memory_usage != null ? (
                  <ProgressBar
                    value={item.memory_usage}
                    status={getUsageStatus(item.memory_usage) === "error" ? "error" : "in-progress"}
                    variant="standalone"
                    additionalInfo={`${item.memory_usage.toFixed(1)}%`}
                  />
                ) : (
                  <Box color="text-status-inactive">—</Box>
                ),
            },
            {
              id: "disk",
              header: t("admin.nodes.col.disk"),
              cell: (item) =>
                item.disk_usage != null ? (
                  <ProgressBar
                    value={item.disk_usage}
                    status={getUsageStatus(item.disk_usage) === "error" ? "error" : "in-progress"}
                    variant="standalone"
                    additionalInfo={`${item.disk_usage.toFixed(1)}%`}
                  />
                ) : (
                  <Box color="text-status-inactive">—</Box>
                ),
            },
            {
              id: "network",
              header: t("admin.nodes.col.network"),
              cell: (item) =>
                item.network_in != null && item.network_out != null ? (
                  <SpaceBetween size="xxxs">
                    <Box fontSize="body-s">↓ {formatBytes(item.network_in)}</Box>
                    <Box fontSize="body-s">↑ {formatBytes(item.network_out)}</Box>
                  </SpaceBetween>
                ) : (
                  <Box color="text-status-inactive">—</Box>
                ),
            },
            {
              id: "lastSeen",
              header: t("admin.nodes.col.lastSeen"),
              cell: (item) => (
                <span title={item.last_seen ? new Date(item.last_seen).toLocaleString() : ""}>
                  {formatRelativeTime(item.last_seen)}
                </span>
              ),
            },
            {
              id: "actions",
              header: t("admin.nodes.col.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button
                    variant="inline-link"
                    onClick={() => setMetricsModal(item)}
                  >
                    {t("admin.nodes.details")}
                  </Button>
                  <Button
                    variant="inline-link"
                    disabled={item.status === "pending"}
                    onClick={() => handleEditOpen(item)}
                  >
                    {t("admin.nodes.edit")}
                  </Button>
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>
                    {t("admin.nodes.delete")}
                  </Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.nodes.empty")}</Box>}
        />

        <Modal
          visible={metricsModal !== null}
          onDismiss={() => setMetricsModal(null)}
          header={metricsModal ? `${metricsModal.name} — ${t("admin.nodes.metricsTitle")}` : ""}
          size="large"
          footer={
            <Box float="right">
              <Button variant="primary" onClick={() => setMetricsModal(null)}>{t("admin.nodes.close")}</Button>
            </Box>
          }
        >
          {metricsModal && (
            <SpaceBetween size="l">
              <ColumnLayout columns={2} variant="text-grid">
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.col.status")}</Box>
                  <StatusIndicator type={getStatusIndicatorType(metricsModal.status)}>
                    {metricsModal.status}
                  </StatusIndicator>
                </div>
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.col.lastSeen")}</Box>
                  <Box>{metricsModal.last_seen ? new Date(metricsModal.last_seen).toLocaleString() : "—"}</Box>
                </div>
                <div>
                  <Box variant="awsui-key-label">IP</Box>
                  <Box>{metricsModal.ip}:{metricsModal.port}</Box>
                </div>
                <div>
                  <Box variant="awsui-key-label">Xray</Box>
                  <Box>{metricsModal.xray_version ?? "—"}</Box>
                </div>
              </ColumnLayout>

              <ColumnLayout columns={2} variant="text-grid">
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.col.cpu")}</Box>
                  {metricsModal.cpu_usage != null ? (
                    <ProgressBar
                      value={metricsModal.cpu_usage}
                      status={getUsageStatus(metricsModal.cpu_usage) === "error" ? "error" : "in-progress"}
                      additionalInfo={`${metricsModal.cpu_usage.toFixed(1)}%`}
                    />
                  ) : <Box color="text-status-inactive">—</Box>}
                </div>
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.col.memory")}</Box>
                  {metricsModal.memory_usage != null ? (
                    <ProgressBar
                      value={metricsModal.memory_usage}
                      status={getUsageStatus(metricsModal.memory_usage) === "error" ? "error" : "in-progress"}
                      additionalInfo={`${metricsModal.memory_usage.toFixed(1)}%`}
                    />
                  ) : <Box color="text-status-inactive">—</Box>}
                </div>
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.col.disk")}</Box>
                  {metricsModal.disk_usage != null ? (
                    <ProgressBar
                      value={metricsModal.disk_usage}
                      status={getUsageStatus(metricsModal.disk_usage) === "error" ? "error" : "in-progress"}
                      additionalInfo={`${metricsModal.disk_usage.toFixed(1)}%`}
                    />
                  ) : <Box color="text-status-inactive">—</Box>}
                </div>
                <div>
                  <Box variant="awsui-key-label">Load Avg (1m)</Box>
                  <Box>{metricsModal.load_avg != null ? metricsModal.load_avg.toFixed(2) : "—"}</Box>
                </div>
              </ColumnLayout>

              <ColumnLayout columns={2} variant="text-grid">
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.networkIn")}</Box>
                  <Box>{metricsModal.network_in != null ? formatBytes(metricsModal.network_in) : "—"}</Box>
                </div>
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.networkOut")}</Box>
                  <Box>{metricsModal.network_out != null ? formatBytes(metricsModal.network_out) : "—"}</Box>
                </div>
              </ColumnLayout>
            </SpaceBetween>
          )}
        </Modal>

        <Modal
          visible={tokenModal}
          onDismiss={() => setTokenModal(false)}
          header={t("admin.nodes.tokenModalTitle")}
          footer={
            <Box float="right">
              <Button variant="primary" onClick={() => setTokenModal(false)}>{t("admin.nodes.close")}</Button>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <Box variant="awsui-key-label">{t("admin.nodes.token")}</Box>
            <Textarea value={tokenData?.token ?? ""} readOnly rows={2} />
            <Box variant="awsui-key-label">{t("admin.nodes.installCommand")}</Box>
            <Textarea value={tokenData?.install_command ?? ""} readOnly rows={3} />
          </SpaceBetween>
        </Modal>

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header={t("admin.nodes.deleteConfirmTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.nodes.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.nodes.confirmDelete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.nodes.deleteConfirmMessage", { name: deleteModal?.name })}
        </Modal>

        <Modal
          visible={editModal !== null}
          onDismiss={() => setEditModal(null)}
          header={t("admin.nodes.editModalTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setEditModal(null)}>{t("admin.nodes.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleEditSubmit()}>
                  {t("admin.nodes.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <FormField label={t("admin.nodes.ipReadOnly")}>
              <Box>{editModal?.ip}</Box>
            </FormField>
            <FormField label={t("admin.nodes.portReadOnly")}>
              <Box>{editModal?.port}</Box>
            </FormField>
            <FormField label={t("admin.nodes.col.name")}>
              <Input
                value={editForm.name}
                onChange={({ detail }) => setEditForm((f) => ({ ...f, name: detail.value }))}
              />
            </FormField>
            <FormField label={t("admin.nodes.col.country")}>
              <Input
                value={editForm.country}
                onChange={({ detail }) => setEditForm((f) => ({ ...f, country: detail.value }))}
              />
            </FormField>
            <FormField label={t("admin.nodes.region")}>
              <Input
                value={editForm.region}
                onChange={({ detail }) => setEditForm((f) => ({ ...f, region: detail.value }))}
              />
            </FormField>
          </SpaceBetween>
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
