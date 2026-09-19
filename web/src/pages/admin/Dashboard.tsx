import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useManualRefresh } from "../../hooks/useManualRefresh";
import {
  Badge,
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Header,
  KeyValuePairs,
  Link,
  SegmentedControl,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
} from "@cloudscape-design/components";
import {
  getActivity,
  getDashboardAlerts,
  getDashboardStats,
  getNodeTraffic,
  getOnlineUsers,
  listNodes,
  listPlanRequests,
} from "../../api/admin";
import type {
  ActivityEntry,
  AlertSeverity,
  DashboardAlerts,
  DashboardStats,
  Node,
  NodeIssue,
  NodeTraffic,
  OnlineUser,
  PlanRequest,
  TrafficWindow,
} from "../../api/types";

const PREVIEW_ROWS = 5;

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

function formatDateTime(iso: string): string {
  return `${new Date(iso).toLocaleDateString()} ${formatTime(iso)}`;
}

// The stamp doubles as refresh feedback, so it needs seconds: two refreshes in
// the same minute would otherwise look like nothing happened.
// Matches the node agents' 10s heartbeat, so a change on a node reaches
// the screen within roughly one beat plus one poll.
const REFRESH_INTERVAL = 10000;

function formatStamp(iso: string): string {
  const date = new Date(iso);
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })}`;
}

/** Cloudscape has no neutral indicator, so an unchanged value uses the info type. */
function deltaIndicator(delta: number, format: (value: number) => string = String) {
  if (delta === 0) return <StatusIndicator type="info">{format(0)}</StatusIndicator>;
  return (
    <StatusIndicator type={delta > 0 ? "success" : "error"}>
      {`${delta > 0 ? "+" : "−"}${format(Math.abs(delta))}`}
    </StatusIndicator>
  );
}

const alertActionKey: Record<AlertSeverity, string> = {
  error: "actionNow",
  warning: "actionCheck",
  info: "actionReview",
  success: "actionReview",
};

const badgeColor: Record<AlertSeverity, "red" | "severity-medium" | "blue" | "green"> = {
  error: "red",
  warning: "severity-medium",
  info: "blue",
  success: "green",
};

/** StatusIndicator has no neutral variant, so info stands in for it. */
function indicatorType(severity: AlertSeverity) {
  return severity === "info" ? "info" : severity;
}

function MetricValue({
  value,
  suffix,
  delta,
  emphasis,
}: {
  value: string;
  suffix?: string;
  delta?: React.ReactNode;
  emphasis?: boolean;
}) {
  return (
    <SpaceBetween size="xxxs">
      <SpaceBetween size="xs" direction="horizontal" alignItems="center">
        <Box
          fontSize="display-l"
          fontWeight="bold"
          color={emphasis ? "text-status-error" : "inherit"}
        >
          {value}
        </Box>
        {delta}
      </SpaceBetween>
      {suffix && (
        <Box variant="small" color="text-body-secondary">
          {suffix}
        </Box>
      )}
    </SpaceBetween>
  );
}

export default function Dashboard() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [alerts, setAlerts] = useState<DashboardAlerts | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [nodeTraffic, setNodeTraffic] = useState<NodeTraffic[]>([]);
  const [activity, setActivity] = useState<ActivityEntry[]>([]);
  const [onlineUsers, setOnlineUsers] = useState<OnlineUser[]>([]);
  const [planRequests, setPlanRequests] = useState<PlanRequest[]>([]);
  const [loading, setLoading] = useState(true);

  const [trafficMetric, setTrafficMetric] = useState<"download" | "upload">("download");
  const [trafficWindow, setTrafficWindow] = useState<TrafficWindow>("today");

  const fetchData = useCallback(async () => {
    try {
      const [statsData, alertsData, nodesData, trafficData, activityData, onlineData, requestsData] =
        await Promise.all([
          getDashboardStats(),
          getDashboardAlerts(),
          listNodes(),
          getNodeTraffic(trafficWindow, PREVIEW_ROWS),
          getActivity(PREVIEW_ROWS),
          getOnlineUsers(),
          listPlanRequests("pending"),
        ]);
      setStats(statsData);
      setAlerts(alertsData);
      setNodes(nodesData);
      setNodeTraffic(trafficData);
      setActivity(activityData);
      setOnlineUsers(onlineData);
      setPlanRequests(requestsData);
    } catch {
      // intentionally ignored
    } finally {
      setLoading(false);
    }
  }, [trafficWindow]);

  // The timestamp shown is the server's generated_at, not when the fetch
  // landed, so the hook's lastUpdated is deliberately unused here.
  const { refreshing, refresh } = useManualRefresh(fetchData, REFRESH_INTERVAL);

  const nodeCounts = useMemo(() => {
    let healthy = 0;
    let warning = 0;
    let offline = 0;
    const issueByNode = new Map(alerts?.node_issues.map((i) => [i.node_id, i]) ?? []);

    for (const node of nodes) {
      if (node.status === "pending") continue;
      if (node.status === "offline") offline++;
      else if (issueByNode.has(node.id)) warning++;
      else healthy++;
    }
    return { healthy, warning, offline };
  }, [nodes, alerts]);

  const totalNodeTraffic = useMemo(
    () => nodeTraffic.reduce((sum, n) => sum + n[trafficMetric], 0),
    [nodeTraffic, trafficMetric],
  );

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.dashboard.title")}</Header>}>
        <Box textAlign="center" padding="xxl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  const deltas = stats?.deltas;

  const renderIssueLabel = (issue: NodeIssue) => {
    switch (issue.kind) {
      case "offline":
        return t("admin.dashboard.issue.offlineLabel");
      case "cpu":
        return t("admin.dashboard.issue.cpuLabel", { value: issue.value.toFixed(0) });
      case "memory":
        return t("admin.dashboard.issue.memoryLabel", { value: issue.value.toFixed(0) });
      case "disk":
        return t("admin.dashboard.issue.diskLabel", { value: issue.value.toFixed(0) });
      default:
        return issue.kind;
    }
  };

  const renderActivitySentence = (entry: ActivityEntry) => {
    const key = `admin.dashboard.event.${entry.event_type}`;
    const translated = t(key);
    return translated === key ? t("admin.dashboard.event.unknown") : translated;
  };

  const renderConnections = (item: OnlineUser) =>
    item.max_concurrent > 0
      ? t("admin.dashboard.connectionsOfCap", { current: item.online_ips, cap: item.max_concurrent })
      : t("admin.dashboard.connectionsNoCap", { current: item.online_ips });

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={
            <SpaceBetween size="xxs" direction="horizontal" alignItems="center">
              <span>{t("admin.dashboard.subtitle")}</span>
              {stats?.generated_at && (
                <Box variant="small" color="text-status-inactive">
                  {`· ${t("admin.dashboard.asOf", { time: formatStamp(stats.generated_at) })}`}
                </Box>
              )}
            </SpaceBetween>
          }
          actions={
            <Button
              iconName="refresh"
              ariaLabel={t("admin.dashboard.refresh")}
              loading={refreshing}
              onClick={refresh}
            />
          }
        >
          {t("admin.dashboard.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        <Container>
          <ColumnLayout columns={4} variant="text-grid" minColumnWidth={180}>
            <KeyValuePairs
              items={[
                {
                  label: t("admin.dashboard.activeAlerts"),
                  value: (
                    <MetricValue
                      value={String(stats?.active_alerts ?? 0)}
                      suffix={t("admin.dashboard.alertsNeedAction")}
                      delta={deltas?.available ? deltaIndicator(deltas.active_alerts) : undefined}
                      emphasis={Boolean(stats?.active_alerts)}
                    />
                  ),
                },
              ]}
            />
            <KeyValuePairs
              items={[
                {
                  label: t("admin.dashboard.onlineNodes"),
                  value: (
                    <MetricValue
                      value={`${stats?.online_nodes ?? 0} / ${stats?.total_nodes ?? 0}`}
                      suffix={t("admin.dashboard.onlineNodesOf")}
                      delta={deltas?.available ? deltaIndicator(deltas.online_nodes) : undefined}
                    />
                  ),
                },
              ]}
            />
            <KeyValuePairs
              items={[
                {
                  label: t("admin.dashboard.connectedUsers"),
                  value: (
                    <MetricValue
                      value={String(stats?.online_users ?? 0)}
                      suffix={t("admin.dashboard.ofTotalUsers", { total: stats?.total_users ?? 0 })}
                      delta={deltas?.available ? deltaIndicator(deltas.online_users) : undefined}
                    />
                  ),
                },
              ]}
            />
            <KeyValuePairs
              items={[
                {
                  label: t("admin.dashboard.trafficTodayTotal"),
                  value: (
                    <MetricValue
                      value={formatBytes(stats?.total_traffic_today ?? 0)}
                      suffix={t("admin.dashboard.uploadDownloadSplit", {
                        upload: formatBytes(stats?.upload_today ?? 0),
                        download: formatBytes(stats?.download_today ?? 0),
                      })}
                      delta={
                        deltas?.available
                          ? deltaIndicator(deltas.traffic_today, formatBytes)
                          : undefined
                      }
                    />
                  ),
                },
              ]}
            />
          </ColumnLayout>
        </Container>

        <ColumnLayout columns={2} minColumnWidth={400}>
          <Container
            fitHeight
            header={
              <Header
                variant="h2"
                counter={`(${alerts?.items.length ?? 0})`}
                actions={
                  <Link onFollow={() => navigate("/admin/alerts")}>{t("admin.dashboard.viewAll")}</Link>
                }
              >
                {t("admin.dashboard.alertsTitle")}
              </Header>
            }
          >
            <Table
              variant="embedded"
              contentDensity="compact"
              columnDefinitions={[
                {
                  id: "alert",
                  header: t("admin.dashboard.col.alert"),
                  cell: (item) => (
                    <StatusIndicator type={indicatorType(item.severity)}>
                      {t(`admin.dashboard.alert.${item.kind}`, { count: item.count })}
                    </StatusIndicator>
                  ),
                },
                {
                  id: "detail",
                  header: t("admin.dashboard.col.detail"),
                  cell: (item) => (
                    <Box variant="small" color="text-body-secondary">
                      {t(`admin.dashboard.alert.${item.kind}_desc`)}
                    </Box>
                  ),
                },
                {
                  id: "action",
                  header: t("admin.dashboard.col.action"),
                  minWidth: 120,
                  cell: (item) => (
                    <Badge color={badgeColor[item.severity]}>
                      {t(`admin.dashboard.alert.${alertActionKey[item.severity]}`)}
                    </Badge>
                  ),
                },
              ]}
              items={alerts?.items ?? []}
              empty={
                <Box textAlign="center" padding="m">
                  <StatusIndicator type="success">{t("admin.dashboard.noAlerts")}</StatusIndicator>
                </Box>
              }
            />
          </Container>

          <Container
            fitHeight
            header={
              <Header
                variant="h2"
                description={t("admin.dashboard.nodeSummaryCounts", {
                  healthy: nodeCounts.healthy,
                  warning: nodeCounts.warning,
                  offline: nodeCounts.offline,
                })}
                counter={`(${alerts?.node_issues.length ?? 0})`}
                actions={
                  <Link onFollow={() => navigate("/admin/nodes")}>
                    {t("admin.dashboard.viewAllNodes")}
                  </Link>
                }
              >
                {t("admin.dashboard.problemNodes")}
              </Header>
            }
          >
            <Table
              variant="embedded"
              contentDensity="compact"
              columnDefinitions={[
                {
                  id: "node",
                  header: t("admin.nodes.col.name"),
                  cell: (item) => (
                    <StatusIndicator type={indicatorType(item.severity)}>
                      <Link onFollow={() => navigate(`/admin/nodes/${item.node_id}`)}>
                        {item.node_name}
                      </Link>
                    </StatusIndicator>
                  ),
                },
                {
                  id: "location",
                  header: t("admin.nodes.col.countryRegion"),
                  cell: (item) => [item.country, item.region].filter(Boolean).join(" · ") || "—",
                },
                {
                  id: "reading",
                  header: t("admin.dashboard.col.reading"),
                  cell: renderIssueLabel,
                },
                {
                  id: "kind",
                  header: t("admin.dashboard.col.issue"),
                  minWidth: 130,
                  cell: (item) => (
                    <Badge color={badgeColor[item.severity]}>
                      {t(`admin.dashboard.issue.${item.kind}`)}
                    </Badge>
                  ),
                },
              ]}
              items={alerts?.node_issues.slice(0, PREVIEW_ROWS) ?? []}
              empty={
                <Box textAlign="center" padding="m">
                  <StatusIndicator type="success">
                    {t("admin.dashboard.noProblemNodes")}
                  </StatusIndicator>
                </Box>
              }
            />
          </Container>
        </ColumnLayout>

        <ColumnLayout columns={2} minColumnWidth={400}>
          <Container
            fitHeight
            header={
              <Header
                variant="h2"
                actions={
                  <SpaceBetween size="xs" direction="horizontal">
                    <SegmentedControl
                      selectedId={trafficMetric}
                      onChange={({ detail }) =>
                        setTrafficMetric(detail.selectedId as "download" | "upload")
                      }
                      options={[
                        { id: "download", text: t("admin.dashboard.download") },
                        { id: "upload", text: t("admin.dashboard.upload") },
                      ]}
                    />
                    <Select
                      selectedOption={{
                        value: trafficWindow,
                        label: t(
                          `admin.dashboard.window${trafficWindow.charAt(0).toUpperCase()}${trafficWindow.slice(1)}`,
                        ),
                      }}
                      options={[
                        { value: "today", label: t("admin.dashboard.windowToday") },
                        { value: "week", label: t("admin.dashboard.windowWeek") },
                        { value: "month", label: t("admin.dashboard.windowMonth") },
                      ]}
                      onChange={({ detail }) =>
                        setTrafficWindow((detail.selectedOption.value ?? "today") as TrafficWindow)
                      }
                    />
                  </SpaceBetween>
                }
              >
                {t("admin.dashboard.nodeTrafficTitle")}
              </Header>
            }
          >
            {nodeTraffic.length > 0 ? (
              <SpaceBetween size="xs">
                {nodeTraffic.map((item) => {
                  const value = item[trafficMetric];
                  const share = totalNodeTraffic ? (value / totalNodeTraffic) * 100 : 0;
                  return (
                    <div
                      key={item.node_id}
                      style={{ display: "flex", alignItems: "center", gap: "12px" }}
                    >
                      <div style={{ flex: "0 0 108px", minWidth: 0 }}>
                        <Link
                          fontSize="body-s"
                          onFollow={() => navigate(`/admin/nodes/${item.node_id}`)}
                        >
                          {item.node_name}
                        </Link>
                      </div>
                      <div
                        style={{
                          flex: 1,
                          height: "6px",
                          borderRadius: "3px",
                          background: "var(--color-background-layout-main, rgba(128,128,128,0.18))",
                          overflow: "hidden",
                        }}
                      >
                        <div
                          style={{
                            width: `${share}%`,
                            height: "100%",
                            borderRadius: "3px",
                            background: "var(--color-background-progress-bar-content-default, #0972d3)",
                          }}
                        />
                      </div>
                      <Box variant="small" textAlign="right">
                        <span style={{ display: "inline-block", minWidth: "62px" }}>
                          {formatBytes(value)}
                        </span>
                      </Box>
                      <Box variant="small" color="text-body-secondary" textAlign="right">
                        <span style={{ display: "inline-block", minWidth: "32px" }}>
                          {totalNodeTraffic ? `${share.toFixed(0)}%` : "—"}
                        </span>
                      </Box>
                    </div>
                  );
                })}
                <Box variant="small" color="text-body-secondary">
                  {t("admin.dashboard.trafficTotal", { total: formatBytes(totalNodeTraffic) })}
                </Box>
              </SpaceBetween>
            ) : (
              <Box textAlign="center" padding="m">
                <StatusIndicator type="info">{t("admin.dashboard.noNodeTraffic")}</StatusIndicator>
              </Box>
            )}
          </Container>

          <Container
            fitHeight
            header={
              <Header
                variant="h2"
                actions={
                  <Link onFollow={() => navigate("/admin/activity")}>{t("admin.dashboard.viewAll")}</Link>
                }
              >
                {t("admin.dashboard.recentActivity")}
              </Header>
            }
          >
            <Table
              variant="embedded"
              contentDensity="compact"
              columnDefinitions={[
                {
                  id: "event",
                  header: t("admin.dashboard.col.event"),
                  cell: (item) => (
                    <StatusIndicator type={indicatorType(item.severity)}>
                      <Box variant="span" fontSize="body-s">
                        <Box variant="strong" fontSize="body-s" display="inline">
                          {item.actor_label || item.actor_type}
                        </Box>
                        {` ${renderActivitySentence(item)}`}
                      </Box>
                    </StatusIndicator>
                  ),
                },
                {
                  id: "time",
                  header: t("admin.dashboard.col.time"),
                  minWidth: 90,
                  cell: (item) => (
                    <Box variant="small" color="text-body-secondary">
                      {formatTime(item.created_at)}
                    </Box>
                  ),
                },
              ]}
              items={activity}
              empty={
                <Box textAlign="center" padding="m">
                  <StatusIndicator type="info">{t("admin.dashboard.noActivity")}</StatusIndicator>
                </Box>
              }
            />
          </Container>
        </ColumnLayout>

        <ColumnLayout columns={2} minColumnWidth={400}>
          <Container
            fitHeight
            header={
              <Header
                variant="h2"
                counter={`(${onlineUsers.length})`}
                actions={
                  <Link onFollow={() => navigate("/admin/connections")}>
                    {t("admin.dashboard.viewAll")}
                  </Link>
                }
              >
                {t("admin.dashboard.onlineUsersPreview")}
              </Header>
            }
          >
            <Table
              variant="embedded"
              contentDensity="compact"
              columnDefinitions={[
                {
                  id: "email",
                  header: t("admin.dashboard.col.email"),
                  cell: (item) => (
                    <StatusIndicator type={item.over_cap ? "warning" : "success"}>
                      {item.email}
                    </StatusIndicator>
                  ),
                },
                {
                  id: "device",
                  header: t("admin.dashboard.col.device"),
                  cell: (item) => item.device || "—",
                },
                {
                  id: "node",
                  header: t("admin.dashboard.col.node"),
                  cell: (item) => item.node_name || "—",
                },
                {
                  id: "addresses",
                  header: t("admin.connections.col.addresses"),
                  cell: (item) =>
                    (item.addresses ?? []).length > 0 ? (
                      <Box fontSize="body-s">
                        <span style={{ fontFamily: "monospace" }}>
                          {(item.addresses ?? []).map((a) => a.ip).join(", ")}
                        </span>
                      </Box>
                    ) : (
                      <Box color="text-status-inactive">—</Box>
                    ),
                },
                {
                  id: "connections",
                  header: t("admin.dashboard.col.connections"),
                  cell: renderConnections,
                },
              ]}
              items={onlineUsers.slice(0, PREVIEW_ROWS)}
              empty={
                <Box textAlign="center" padding="m">
                  <StatusIndicator type="info">{t("admin.dashboard.noOnlineUsers")}</StatusIndicator>
                </Box>
              }
            />
          </Container>

          <Container
            fitHeight
            header={
              <Header
                variant="h2"
                counter={`(${planRequests.length})`}
                actions={
                  <Link onFollow={() => navigate("/admin/plan-requests")}>
                    {t("admin.dashboard.viewAll")}
                  </Link>
                }
              >
                {t("admin.dashboard.approvalRequests")}
              </Header>
            }
          >
            <Table
              variant="embedded"
              contentDensity="compact"
              columnDefinitions={[
                {
                  id: "email",
                  header: t("admin.dashboard.col.email"),
                  cell: (item) => item.user_email,
                },
                {
                  id: "plan",
                  header: t("admin.dashboard.col.plan"),
                  cell: (item) => item.plan_name,
                },
                {
                  id: "requested",
                  header: t("admin.dashboard.col.requestedAt"),
                  cell: (item) => formatDateTime(item.created_at),
                },
                {
                  id: "status",
                  header: t("admin.dashboard.col.status"),
                  minWidth: 100,
                  cell: () => <Badge color="blue">{t("admin.dashboard.statusPending")}</Badge>,
                },
              ]}
              items={planRequests.slice(0, PREVIEW_ROWS)}
              empty={
                <Box textAlign="center" padding="m">
                  <StatusIndicator type="success">
                    {t("admin.dashboard.noPendingRequests")}
                  </StatusIndicator>
                </Box>
              }
            />
          </Container>
        </ColumnLayout>
      </SpaceBetween>
    </ContentLayout>
  );
}
