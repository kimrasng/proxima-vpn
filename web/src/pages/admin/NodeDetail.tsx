import { useEffect, useState, useCallback } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  Header,
  ProgressBar,
  SpaceBetween,
  Spinner,
  StatusIndicator,
} from "@cloudscape-design/components";
import { getNode } from "../../api/admin";
import type { Node } from "../../api/types";

const REFRESH_INTERVAL = 30000;

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function formatRelativeTime(dateStr: string | undefined | null): string {
  if (!dateStr) return "—";
  const diffMs = Date.now() - new Date(dateStr).getTime();
  if (diffMs < 0) return "Just now";
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function getUsageStatus(value: number): "success" | "in-progress" | "error" {
  if (value < 50) return "success";
  if (value <= 80) return "in-progress";
  return "error";
}

function getStatusIndicatorType(status: string): "success" | "error" | "pending" {
  if (status === "online") return "success";
  if (status === "offline") return "error";
  return "pending";
}

function MetricBar({ label, value }: { label: string; value: number | undefined }) {
  if (value == null) {
    return (
      <div>
        <Box variant="awsui-key-label">{label}</Box>
        <Box color="text-status-inactive">—</Box>
      </div>
    );
  }
  return (
    <div>
      <Box variant="awsui-key-label">{label}</Box>
      <ProgressBar
        value={value}
        status={getUsageStatus(value)}
        additionalInfo={`${value.toFixed(1)}%`}
      />
    </div>
  );
}

export default function NodeDetail() {
  const { nodeId } = useParams<{ nodeId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [node, setNode] = useState<Node | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [lastRefreshed, setLastRefreshed] = useState<Date | null>(null);

  const fetchNode = useCallback(async () => {
    if (!nodeId) return;
    try {
      const data = await getNode(nodeId);
      setNode(data);
      setLastRefreshed(new Date());
      setError(null);
    } catch {
      setError(t("admin.nodeDetail.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [nodeId, t]);

  useEffect(() => {
    void fetchNode();
    const interval = setInterval(() => void fetchNode(), REFRESH_INTERVAL);
    return () => clearInterval(interval);
  }, [fetchNode]);

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.nodeDetail.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  if (!node) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.nodeDetail.title")}</Header>}>
        <Flashbar items={[{ type: "error", content: error ?? t("admin.nodeDetail.fetchError") }]} />
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button onClick={() => void fetchNode()}>{t("admin.nodeDetail.refresh")}</Button>
              <Button onClick={() => navigate(`/admin/nodes/${nodeId}/inbounds`)}>
                {t("admin.nodeDetail.manageInbounds")}
              </Button>
              <Button onClick={() => navigate("/admin/nodes")}>{t("admin.nodeDetail.back")}</Button>
            </SpaceBetween>
          }
          description={
            lastRefreshed
              ? `${t("admin.nodeDetail.lastRefreshed")}: ${lastRefreshed.toLocaleTimeString()} (${t("admin.nodeDetail.autoRefresh")})`
              : undefined
          }
        >
          {node.name}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <Container header={<Header variant="h2">{t("admin.nodeDetail.overview")}</Header>}>
          <ColumnLayout columns={4} variant="text-grid">
            <div>
              <Box variant="awsui-key-label">{t("admin.nodes.col.status")}</Box>
              <StatusIndicator type={getStatusIndicatorType(node.status)}>
                {node.status}
              </StatusIndicator>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.nodes.col.lastSeen")}</Box>
              <Box>
                <span title={node.last_seen ? new Date(node.last_seen).toLocaleString() : ""}>
                  {formatRelativeTime(node.last_seen)}
                </span>
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">IP</Box>
              <Box>{node.ip}:{node.port}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.nodeDetail.xrayVersion")}</Box>
              <Box>{node.xray_version ?? "—"}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.nodes.col.country")}</Box>
              <Box>{node.country || "—"}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.nodes.col.region")}</Box>
              <Box>{node.region || "—"}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.nodeDetail.createdAt")}</Box>
              <Box>{new Date(node.created_at).toLocaleString()}</Box>
            </div>
          </ColumnLayout>
        </Container>

        <Container header={<Header variant="h2">{t("admin.nodeDetail.performance")}</Header>}>
          <ColumnLayout columns={2} variant="text-grid">
            <MetricBar label={t("admin.nodes.col.cpu")} value={node.cpu_usage} />
            <MetricBar label={t("admin.nodes.col.memory")} value={node.memory_usage} />
            <MetricBar label={t("admin.nodes.col.disk")} value={node.disk_usage} />
            <div>
              <Box variant="awsui-key-label">Load Avg (1m)</Box>
              <Box fontSize="heading-m">
                {node.load_avg != null ? node.load_avg.toFixed(2) : "—"}
              </Box>
            </div>
          </ColumnLayout>
        </Container>

        <Container header={<Header variant="h2">{t("admin.nodeDetail.network")}</Header>}>
          <ColumnLayout columns={2} variant="text-grid">
            <div>
              <Box variant="awsui-key-label">{t("admin.nodes.networkIn")}</Box>
              <Box fontSize="heading-m">
                {node.network_in != null ? formatBytes(node.network_in) : "—"}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.nodes.networkOut")}</Box>
              <Box fontSize="heading-m">
                {node.network_out != null ? formatBytes(node.network_out) : "—"}
              </Box>
            </div>
          </ColumnLayout>
        </Container>
      </SpaceBetween>
    </ContentLayout>
  );
}
