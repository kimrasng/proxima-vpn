import { useState, useCallback, useMemo } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  Grid,
  Header,
  KeyValuePairs,
  Popover,
  SegmentedControl,
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
import { NodeEventViewer } from "../../components/NodeEventViewer";
import type { Node, NodeMetricsEntry } from "../../api/types";
import { usePublishBreadcrumbLeaf } from "../../hooks/useBreadcrumbLeaf";
import { formatAbsoluteTime, formatRelativeTime } from "../../utils/relativeTime";
import { formatBytes } from "../../utils/format";
import { useManualRefresh } from "../../hooks/useManualRefresh";

// Matches the node agents' 10s heartbeat, so a change on a node reaches
// the screen within roughly one beat plus one poll.
const REFRESH_INTERVAL = 10000;


type ResourceMetric = "cpu" | "memory" | "disk";

function isResourceMetric(value: string): value is ResourceMetric {
  return value === "cpu" || value === "memory" || value === "disk";
}

const RESOURCE_STROKE: Record<ResourceMetric, string> = {
  cpu: "#0073bb",
  memory: "#e07941",
  disk: "#2ea597",
};

// Above this reading the node is treated as actively degraded, not merely busy,
// and gets an Alert rather than just a coloured indicator.
const ALERT_THRESHOLD = 90;

function formatChartTime(dateStr: string, hours: number): string {
  const d = new Date(dateStr);
  if (hours <= 24) {
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleDateString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}


function getUsageStatus(value: number): "success" | "warning" | "error" {
  if (value < 50) return "success";
  if (value <= 80) return "warning";
  return "error";
}

function getStatusIndicatorType(status: string): "success" | "error" | "pending" {
  if (status === "online") return "success";
  if (status === "offline") return "error";
  return "pending";
}

/** Agents report free-form `region`/`country` in arbitrary case ("seoul"), so every word gets an uppercase initial. */
function titleCase(value: string): string {
  return value.replace(/\b\p{Ll}/gu, (char) => char.toUpperCase());
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

function UsageTile({ label, value }: { label: string; value: number | undefined }) {
  return (
    <div>
      <Box variant="awsui-key-label">{label}</Box>
      {value == null ? (
        <Box color="text-status-inactive">—</Box>
      ) : (
        <StatusIndicator type={getUsageStatus(value)}>{`${value.toFixed(1)}%`}</StatusIndicator>
      )}
    </div>
  );
}

function ValueTile({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <Box variant="awsui-key-label">{label}</Box>
      <Box fontSize="heading-s">{value}</Box>
    </div>
  );
}

export default function NodeDetail() {
  const { nodeId } = useParams<{ nodeId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [node, setNode] = useState<Node | null>(null);
  const [metrics, setMetrics] = useState<NodeMetricsEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedHours, setSelectedHours] = useState(24);
  const [resourceMetric, setResourceMetric] = useState<ResourceMetric>("cpu");

  usePublishBreadcrumbLeaf(node?.name);

  const hoursOptions = useMemo(
    () => [
      { value: "1", label: t("admin.nodeDetail.range1h") },
      { value: "6", label: t("admin.nodeDetail.range6h") },
      { value: "24", label: t("admin.nodeDetail.range24h") },
      { value: "72", label: t("admin.nodeDetail.range3d") },
      { value: "168", label: t("admin.nodeDetail.range7d") },
    ],
    [t],
  );

  const fetchAll = useCallback(async () => {
    if (!nodeId) return;
    try {
      const [nodeData, metricsData] = await Promise.all([
        getNode(nodeId),
        getNodeMetrics(nodeId, selectedHours),
      ]);
      setNode(nodeData);
      setMetrics(metricsData);
      setError(null);
    } catch {
      setError(t("admin.nodeDetail.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [nodeId, selectedHours, t]);

  const {
    refreshing,
    lastUpdated: lastRefreshed,
    refresh,
  } = useManualRefresh(fetchAll, REFRESH_INTERVAL);

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

  const chartData = buildChartData(metrics, selectedHours);

  const noData = (
    <Box textAlign="center" color="text-status-inactive" padding="xl">
      {t("admin.nodeDetail.noData")}
    </Box>
  );



  const statusLabel = (status: string): string => {
    if (status === "online") return t("admin.nodes.statusOnline");
    if (status === "offline") return t("admin.nodes.statusOffline");
    return t("admin.nodes.statusPending");
  };

  const timeRangeSelect = (
    <Select
      ariaLabel={t("admin.nodeDetail.timeRange")}
      selectedOption={
        hoursOptions.find((o) => o.value === String(selectedHours)) ?? hoursOptions[2] ?? null
      }
      onChange={({ detail }) => setSelectedHours(Number(detail.selectedOption.value))}
      options={hoursOptions}
    />
  );

  const resourceLabel: Record<ResourceMetric, string> = {
    cpu: t("admin.nodeDetail.metricCpu"),
    memory: t("admin.nodeDetail.metricMemory"),
    disk: t("admin.nodeDetail.metricDisk"),
  };

  const breachAlerts: { key: string; type: "error" | "warning"; header: string; detail: string }[] = [];
  if (node.status !== "online") {
    breachAlerts.push({
      key: "offline",
      type: "error",
      header: t("admin.nodeDetail.alertOffline"),
      detail: t("admin.nodeDetail.alertOfflineDetail"),
    });
  }
  if (node.cpu_usage != null && node.cpu_usage > ALERT_THRESHOLD) {
    breachAlerts.push({
      key: "cpu",
      type: "warning",
      header: t("admin.nodeDetail.alertCpu"),
      detail: t("admin.nodeDetail.alertCpuDetail"),
    });
  }
  if (node.memory_usage != null && node.memory_usage > ALERT_THRESHOLD) {
    breachAlerts.push({
      key: "memory",
      type: "warning",
      header: t("admin.nodeDetail.alertMemory"),
      detail: t("admin.nodeDetail.alertMemoryDetail"),
    });
  }
  if (node.disk_usage != null && node.disk_usage > ALERT_THRESHOLD) {
    breachAlerts.push({
      key: "disk",
      type: "warning",
      header: t("admin.nodeDetail.alertDisk"),
      detail: t("admin.nodeDetail.alertDiskDetail"),
    });
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                iconName="refresh"
                ariaLabel={t("admin.nodeDetail.refresh")}
                loading={refreshing}
                onClick={refresh}
              />
              <Button onClick={() => navigate(`/admin/node-chains?${node.role === "both" ? "node" : node.role === "exit" ? "exit" : "entry"}=${encodeURIComponent(node.id)}`)}>
                {t("admin.routeManagement.portMap")}
              </Button>
              {node.role !== "relay" && <Button variant="primary" onClick={() => navigate(`/admin/nodes/${nodeId}/inbounds`)}>
                {t("admin.nodeDetail.manageInbounds")}
              </Button>}
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

        <ColumnLayout columns={2} minColumnWidth={400}>
          <Container fitHeight header={<Header variant="h2">{t("admin.nodeDetail.overview")}</Header>}>
            <KeyValuePairs
              columns={2}
              items={[
                { label: t("admin.nodes.role.label"), value: t(`admin.nodes.role.${node.role}`) },
                {
                  label: t("admin.nodes.col.status"),
                  value: (
                    <StatusIndicator type={getStatusIndicatorType(node.status)}>
                      {statusLabel(node.status)}
                    </StatusIndicator>
                  ),
                },
                { label: t("admin.nodes.col.country"), value: node.country ? node.country.toUpperCase() : "—" },
                { label: t("admin.nodes.col.region"), value: node.region ? titleCase(node.region) : "—" },
                { label: t("admin.nodeDetail.ipAddress"), value: `${node.ip}:${node.port}` },
                {
                  label: t("admin.nodes.col.lastSeen"),
                  value: (
                    <span title={formatAbsoluteTime(node.last_seen)}>
                      {formatRelativeTime(t, node.last_seen)}
                    </span>
                  ),
                },
                ...(node.role !== "relay" ? [{ label: t("admin.nodeDetail.xrayVersion"), value: node.xray_version ?? "—" }] : []),
                {
                  label: t("admin.nodeDetail.createdAt"),
                  value: new Date(node.created_at).toLocaleString(),
                },
              ]}
            />
          </Container>

          <Container fitHeight header={<Header variant="h2">{t("admin.nodeDetail.currentMetrics")}</Header>}>
            <SpaceBetween size="l">
              <Grid
                gridDefinition={[
                  { colspan: 4 },
                  { colspan: 4 },
                  { colspan: 4 },
                ]}
              >
                <UsageTile label={t("admin.nodes.col.cpu")} value={node.cpu_usage} />
                <UsageTile label={t("admin.nodes.col.memory")} value={node.memory_usage} />
                <UsageTile label={t("admin.nodes.col.disk")} value={node.disk_usage} />
              </Grid>
              <Grid
                gridDefinition={[
                  { colspan: 4 },
                  { colspan: 4 },
                  { colspan: 4 },
                ]}
              >
                <ValueTile
                  label={t("admin.nodeDetail.loadAvg")}
                  value={node.load_avg != null ? node.load_avg.toFixed(2) : "—"}
                />
                <ValueTile
                  label={t("admin.nodes.networkIn")}
                  value={node.network_in != null ? formatBytes(node.network_in) : "—"}
                />
                <ValueTile
                  label={t("admin.nodes.networkOut")}
                  value={node.network_out != null ? formatBytes(node.network_out) : "—"}
                />
              </Grid>
            </SpaceBetween>
          </Container>
        </ColumnLayout>

        <ColumnLayout columns={2} minColumnWidth={400}>
          <Container
            fitHeight
            header={
              <Header
                variant="h2"
                counter={breachAlerts.length > 0 ? `(${breachAlerts.length})` : undefined}
              >
                {t("admin.nodeDetail.healthSummary")}
              </Header>
            }
          >
            <SpaceBetween size="l">
              {breachAlerts.length > 0 ? (
                <SpaceBetween size="xs">
                  {breachAlerts.map((alert) => (
                    <Alert key={alert.key} type={alert.type} header={alert.header}>
                      {alert.detail}
                    </Alert>
                  ))}
                </SpaceBetween>
              ) : (
                <Alert type="success" header={t("admin.nodeDetail.healthAllClear")}>
                  {t("admin.nodeDetail.healthAllClearDetail")}
                </Alert>
              )}
              {node.role === "relay" ? <Box color="text-body-secondary">{t("admin.routeManagement.entryMetricsHint")}</Box> : <KeyValuePairs
                columns={1}
                items={[
                  {
                    label: t("admin.nodes.col.occupancy"),
                    info: (
                      <Popover
                        size="medium"
                        triggerType="text"
                        position="top"
                        header={t("admin.nodes.col.occupancy")}
                        content={t("admin.nodeDetail.occupancyHint")}
                      >
                        <Box variant="small" color="text-status-info">
                          {t("admin.nodeDetail.info")}
                        </Box>
                      </Popover>
                    ),
                    value: (
                      <SpaceBetween size="xxxs">
                        <Box variant="awsui-value-large">
                          {`${node.online_devices} / ${node.capacity}`}
                        </Box>
                        {node.online_devices > node.capacity ? (
                          <StatusIndicator type="warning">
                            {t("admin.nodeDetail.occupancyOverCapacity")}
                          </StatusIndicator>
                        ) : (
                          <Box variant="small" color="text-body-secondary">
                            {t("admin.nodeDetail.occupancyUnits")}
                          </Box>
                        )}
                      </SpaceBetween>
                    ),
                  },
                  {
                    label: t("admin.nodeDetail.shaping"),
                    value:
                      node.shaping_ok === false ? (
                        <SpaceBetween size="xxs">
                          <StatusIndicator type="warning">
                            {t("admin.nodes.shapingNotApplied")}
                          </StatusIndicator>
                          <Box variant="small" color="text-body-secondary">
                            {node.shaping_error
                              ? `${t("admin.nodeDetail.shapingImpact")} ${t("admin.nodeDetail.shapingReason", { reason: node.shaping_error })}`
                              : t("admin.nodeDetail.shapingImpact")}
                          </Box>
                        </SpaceBetween>
                      ) : node.shaping_ok === true ? (
                        <StatusIndicator type="success">
                          {t(node.shaping_mode === "device_global_v1" ? "admin.routeManagement.deviceLimiterActive" : "admin.nodeDetail.shapingActive", { tiers: node.shaping_tiers ?? 0 })}
                        </StatusIndicator>
                      ) : (
                        <StatusIndicator type="info">
                          {t("admin.nodeDetail.shapingUnknown")}
                        </StatusIndicator>
                      ),
                  },
                  {
                    label: t("admin.nodeDetail.xrayVersion"),
                    value: node.xray_too_old ? (
                      <SpaceBetween size="xxs">
                        <StatusIndicator type="warning">
                          {t("admin.nodes.xrayTooOld", { minimum: node.xray_minimum })}
                        </StatusIndicator>
                        {node.xray_version_warning && (
                          <Box variant="small" color="text-body-secondary">
                            {node.xray_version_warning}
                          </Box>
                        )}
                      </SpaceBetween>
                    ) : (
                      <StatusIndicator type="success">
                        {t("admin.nodeDetail.xrayOk", { minimum: node.xray_minimum })}
                      </StatusIndicator>
                    ),
                  },
                ]}
              />}
            </SpaceBetween>
          </Container>

        </ColumnLayout>

        <NodeEventViewer nodeId={node.id} />

        <Container
          header={
            <Header
              variant="h2"
              actions={
                <SpaceBetween size="xs" direction="horizontal">
                  <SegmentedControl
                    label={t("admin.nodeDetail.resourceHistory")}
                    selectedId={resourceMetric}
                    onChange={({ detail }) => {
                      if (isResourceMetric(detail.selectedId)) setResourceMetric(detail.selectedId);
                    }}
                    options={[
                      { id: "cpu", text: resourceLabel.cpu },
                      { id: "memory", text: resourceLabel.memory },
                      { id: "disk", text: resourceLabel.disk },
                    ]}
                  />
                  {timeRangeSelect}
                </SpaceBetween>
              }
            >
              {t("admin.nodeDetail.resourceHistory")}
            </Header>
          }
        >
          {chartData.length === 0 ? noData : (
            <ResponsiveContainer width="100%" height={280}>
              <LineChart data={chartData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="time" tick={{ fontSize: 11 }} interval="preserveStartEnd" />
                <YAxis domain={[0, 100]} unit="%" tick={{ fontSize: 11 }} />
                <Tooltip formatter={(v: number) => `${v.toFixed(1)}%`} />
                <Legend />
                <Line
                  type="monotone"
                  dataKey={resourceMetric}
                  name={resourceLabel[resourceMetric]}
                  stroke={RESOURCE_STROKE[resourceMetric]}
                  dot={false}
                  strokeWidth={2}
                />
              </LineChart>
            </ResponsiveContainer>
          )}
        </Container>

        <ColumnLayout columns={2} minColumnWidth={400}>
          <Container fitHeight header={<Header variant="h2">{t("admin.nodeDetail.loadChart")}</Header>}>
            {chartData.length === 0 ? noData : (
              <ResponsiveContainer width="100%" height={240}>
                <LineChart data={chartData}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" tick={{ fontSize: 11 }} interval="preserveStartEnd" />
                  <YAxis tick={{ fontSize: 11 }} />
                  <Tooltip formatter={(v: number) => v.toFixed(2)} />
                  <Legend />
                  <Line
                    type="monotone"
                    dataKey="load_avg"
                    name={t("admin.nodeDetail.loadAvg")}
                    stroke="#8884d8"
                    dot={false}
                    strokeWidth={2}
                  />
                </LineChart>
              </ResponsiveContainer>
            )}
          </Container>

          <Container fitHeight header={<Header variant="h2">{t("admin.nodeDetail.networkChart")}</Header>}>
            {chartData.length === 0 ? noData : (
              <ResponsiveContainer width="100%" height={240}>
                <LineChart data={chartData}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" tick={{ fontSize: 11 }} interval="preserveStartEnd" />
                  <YAxis tickFormatter={(v: number) => formatBytes(v)} tick={{ fontSize: 11 }} width={80} />
                  <Tooltip formatter={(v: number) => formatBytes(v)} />
                  <Legend />
                  <Line
                    type="monotone"
                    dataKey="network_in"
                    name={t("admin.nodes.networkIn")}
                    stroke="#1d8102"
                    dot={false}
                    strokeWidth={2}
                  />
                  <Line
                    type="monotone"
                    dataKey="network_out"
                    name={t("admin.nodes.networkOut")}
                    stroke="#d13212"
                    dot={false}
                    strokeWidth={2}
                  />
                </LineChart>
              </ResponsiveContainer>
            )}
          </Container>
        </ColumnLayout>
      </SpaceBetween>
    </ContentLayout>
  );
}
