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
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
} from "@cloudscape-design/components";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from "recharts";
import { getNode, getNodeMetrics } from "../../api/admin";
import type { Node, NodeMetricsEntry } from "../../api/types";

const REFRESH_INTERVAL = 30000;

const HOURS_OPTIONS = [
  { value: "1", label: "Last 1 hour" },
  { value: "6", label: "Last 6 hours" },
  { value: "24", label: "Last 24 hours" },
  { value: "72", label: "Last 3 days" },
  { value: "168", label: "Last 7 days" },
];

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

function formatChartTime(dateStr: string, hours: number): string {
  const d = new Date(dateStr);
  if (hours <= 24) {
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleDateString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
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

interface ChartPoint {
  time: string;
  cpu: number;
  memory: number;
  disk: number;
  load_avg: number;
  network_in: number;
  network_out: number;
}

function buildChartData(entries: NodeMetricsEntry[], hours: number): ChartPoint[] {
  return entries.map((e) => ({
    time: formatChartTime(e.recorded_at, hours),
    cpu: parseFloat(e.cpu_usage.toFixed(1)),
    memory: parseFloat(e.memory_usage.toFixed(1)),
    disk: parseFloat(e.disk_usage.toFixed(1)),
    load_avg: parseFloat(e.load_avg.toFixed(2)),
    network_in: e.network_in,
    network_out: e.network_out,
  }));
}

export default function NodeDetail() {
  const { nodeId } = useParams<{ nodeId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [node, setNode] = useState<Node | null>(null);
  const [metrics, setMetrics] = useState<NodeMetricsEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [lastRefreshed, setLastRefreshed] = useState<Date | null>(null);
  const [selectedHours, setSelectedHours] = useState(24);

  const fetchAll = useCallback(async () => {
    if (!nodeId) return;
    try {
      const [nodeData, metricsData] = await Promise.all([
        getNode(nodeId),
        getNodeMetrics(nodeId, selectedHours),
      ]);
      setNode(nodeData);
      setMetrics(metricsData);
      setLastRefreshed(new Date());
      setError(null);
    } catch {
      setError(t("admin.nodeDetail.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [nodeId, selectedHours, t]);

  useEffect(() => {
    void fetchAll();
    const interval = setInterval(() => void fetchAll(), REFRESH_INTERVAL);
    return () => clearInterval(interval);
  }, [fetchAll]);

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
              <Button onClick={() => void fetchAll()}>{t("admin.nodeDetail.refresh")}</Button>
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

        <Container header={<Header variant="h2">{t("admin.nodeDetail.currentMetrics")}</Header>}>
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

        {(() => {
          const chartData = buildChartData(metrics, selectedHours);
          const noData = (
            <Box textAlign="center" color="text-status-inactive" padding="xl">
              {t("admin.nodeDetail.noData")}
            </Box>
          );
          return (
            <>
              <Container
                header={
                  <Header
                    variant="h2"
                    actions={
                      <Select
                        selectedOption={HOURS_OPTIONS.find((o) => o.value === String(selectedHours)) ?? HOURS_OPTIONS[2] ?? null}
                        onChange={({ detail }) => setSelectedHours(Number(detail.selectedOption.value))}
                        options={HOURS_OPTIONS}
                      />
                    }
                  >
                    {t("admin.nodeDetail.cpuMemoryChart")}
                  </Header>
                }
              >
                {chartData.length === 0 ? noData : (
                  <ResponsiveContainer width="100%" height={260}>
                    <LineChart data={chartData}>
                      <CartesianGrid strokeDasharray="3 3" />
                      <XAxis dataKey="time" tick={{ fontSize: 11 }} interval="preserveStartEnd" />
                      <YAxis domain={[0, 100]} unit="%" tick={{ fontSize: 11 }} />
                      <Tooltip formatter={(v: number) => `${v.toFixed(1)}%`} />
                      <Legend />
                      <Line type="monotone" dataKey="cpu" name="CPU" stroke="#0073bb" dot={false} strokeWidth={2} />
                      <Line type="monotone" dataKey="memory" name="Memory" stroke="#e07941" dot={false} strokeWidth={2} />
                      <Line type="monotone" dataKey="disk" name="Disk" stroke="#2ea597" dot={false} strokeWidth={2} />
                    </LineChart>
                  </ResponsiveContainer>
                )}
              </Container>

              <Container header={<Header variant="h2">{t("admin.nodeDetail.loadChart")}</Header>}>
                {chartData.length === 0 ? noData : (
                  <ResponsiveContainer width="100%" height={200}>
                    <LineChart data={chartData}>
                      <CartesianGrid strokeDasharray="3 3" />
                      <XAxis dataKey="time" tick={{ fontSize: 11 }} interval="preserveStartEnd" />
                      <YAxis tick={{ fontSize: 11 }} />
                      <Tooltip formatter={(v: number) => v.toFixed(2)} />
                      <Legend />
                      <Line type="monotone" dataKey="load_avg" name="Load Avg" stroke="#8884d8" dot={false} strokeWidth={2} />
                    </LineChart>
                  </ResponsiveContainer>
                )}
              </Container>

              <Container header={<Header variant="h2">{t("admin.nodeDetail.networkChart")}</Header>}>
                {chartData.length === 0 ? noData : (
                  <ResponsiveContainer width="100%" height={200}>
                    <LineChart data={chartData}>
                      <CartesianGrid strokeDasharray="3 3" />
                      <XAxis dataKey="time" tick={{ fontSize: 11 }} interval="preserveStartEnd" />
                      <YAxis tickFormatter={(v: number) => formatBytes(v)} tick={{ fontSize: 11 }} width={80} />
                      <Tooltip formatter={(v: number) => formatBytes(v)} />
                      <Legend />
                      <Line type="monotone" dataKey="network_in" name="Network In" stroke="#1d8102" dot={false} strokeWidth={2} />
                      <Line type="monotone" dataKey="network_out" name="Network Out" stroke="#d13212" dot={false} strokeWidth={2} />
                    </LineChart>
                  </ResponsiveContainer>
                )}
              </Container>
            </>
          );
        })()}
      </SpaceBetween>
    </ContentLayout>
  );
}
