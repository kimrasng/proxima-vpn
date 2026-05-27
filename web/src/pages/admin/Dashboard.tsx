import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  ColumnLayout,
  Container,
  ContentLayout,
  Grid,
  Header,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  ProgressBar,
  Table,
} from "@cloudscape-design/components";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import { getDashboardStats, listNodes, getOnlineUsers, getTrafficHistory } from "../../api/admin";
import type { DashboardStats, Node, OnlineUser, TrafficHistoryEntry } from "../../api/types";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

export default function Dashboard() {
  const { t } = useTranslation();
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [onlineUsers, setOnlineUsers] = useState<OnlineUser[]>([]);
  const [trafficHistory, setTrafficHistory] = useState<TrafficHistoryEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [onlineNodeFilter, setOnlineNodeFilter] = useState("all");

  const fetchData = async () => {
    try {
      const [statsData, nodesData, onlineData, trafficData] = await Promise.all([
        getDashboardStats(),
        listNodes(),
        getOnlineUsers(),
        getTrafficHistory(),
      ]);
      setStats(statsData);
      setNodes(nodesData);
      setOnlineUsers(onlineData);
      setTrafficHistory(trafficData);
    } catch {
      // intentionally ignored
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchData();
    const interval = setInterval(() => void fetchData(), 30000);
    return () => clearInterval(interval);
  }, []);

  const onlineNodeOptions = useMemo(() => {
    const nodeNames = [...new Set(onlineUsers.map((u) => u.node_name).filter(Boolean))];
    return [
      { value: "all", label: "All nodes" },
      ...nodeNames.map((name) => ({ value: name, label: name })),
    ];
  }, [onlineUsers]);

  const filteredOnlineUsers = useMemo(() => {
    if (onlineNodeFilter === "all") return onlineUsers;
    return onlineUsers.filter((u) => u.node_name === onlineNodeFilter);
  }, [onlineUsers, onlineNodeFilter]);

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.dashboard.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  const trafficData = trafficHistory.map((entry) => ({
    day: new Date(entry.date).toLocaleDateString(undefined, { weekday: "short" }),
    upload: entry.upload,
    download: entry.download,
  }));

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.dashboard.title")}</Header>}>
      <SpaceBetween size="l">
        <Container>
          <ColumnLayout columns={4} variant="text-grid">
            <div>
              <Box variant="awsui-key-label">{t("admin.dashboard.totalUsers")}</Box>
              <Box variant="h1" padding={{ top: "xs" }}>{stats?.total_users ?? 0}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.dashboard.activeUsers")}</Box>
              <Box variant="h1" padding={{ top: "xs" }}>{stats?.active_users ?? 0}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.dashboard.onlineUsers")}</Box>
              <Box variant="h1" padding={{ top: "xs" }} color="text-status-success">{stats?.online_users ?? 0}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.dashboard.pendingRequests")}</Box>
              <Box variant="h1" padding={{ top: "xs" }}>{stats?.pending_requests ?? 0}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.dashboard.totalNodes")}</Box>
              <Box variant="h1" padding={{ top: "xs" }}>{stats?.total_nodes ?? 0}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.dashboard.onlineNodes")}</Box>
              <Box variant="h1" padding={{ top: "xs" }} color="text-status-success">{stats?.online_nodes ?? 0}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.dashboard.trafficToday")}</Box>
              <Box variant="h1" padding={{ top: "xs" }}>{formatBytes(stats?.total_traffic_today ?? 0)}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.dashboard.trafficMonth")}</Box>
              <Box variant="h1" padding={{ top: "xs" }}>{formatBytes(stats?.total_traffic_month ?? 0)}</Box>
            </div>
          </ColumnLayout>
        </Container>

        <Container
          header={
            <Header variant="h2" description={t("admin.dashboard.trafficDescription")}>
              {t("admin.dashboard.trafficOverview")}
            </Header>
          }
        >
          {trafficData.length > 0 ? (
            <Box padding={{ vertical: "m" }}>
              <ResponsiveContainer width="100%" height={240}>
                <BarChart data={trafficData} margin={{ top: 5, right: 20, bottom: 5, left: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border-divider-default, #e9ebed)" />
                  <XAxis dataKey="day" tick={{ fontSize: 12 }} />
                  <YAxis tickFormatter={(v: number) => formatBytes(v)} tick={{ fontSize: 12 }} />
                  <Tooltip
                    formatter={(value: number) => formatBytes(value)}
                    contentStyle={{
                      backgroundColor: "var(--color-background-container-content, #fff)",
                      border: "1px solid var(--color-border-divider-default, #e9ebed)",
                      borderRadius: "8px",
                    }}
                  />
                  <Legend />
                  <Bar dataKey="upload" name="Upload" fill="var(--color-charts-line-1, #0972d3)" radius={[4, 4, 0, 0]} />
                  <Bar dataKey="download" name="Download" fill="var(--color-charts-line-2, #eb5f07)" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </Box>
          ) : (
            <Box textAlign="center" padding="l">
              <StatusIndicator type="info">{t("admin.dashboard.noTrafficData")}</StatusIndicator>
            </Box>
          )}
        </Container>

        <Container
          header={
            <Header variant="h2" description={`${nodes.length} ${t("admin.dashboard.nodesRegistered")}`}>
              {t("admin.dashboard.nodeStatus")}
            </Header>
          }
        >
          <Grid
            gridDefinition={nodes.slice(0, 8).map(() => ({ colspan: { default: 12, s: 6, l: 3 } }))}
          >
            {nodes.slice(0, 8).map((node) => {
              const isOnline = node.status === "online";
              return (
                <Container key={node.id}>
                  <SpaceBetween size="xs">
                    <Box>
                      <SpaceBetween size="xxs" direction="horizontal">
                        <StatusIndicator type={isOnline ? "success" : "error"}>
                          {node.name}
                        </StatusIndicator>
                      </SpaceBetween>
                      <Box variant="small" color="text-body-secondary">
                        {node.country} · {node.region}
                      </Box>
                    </Box>
                    <ProgressBar
                      value={node.cpu_usage ?? 0}
                      label="CPU"
                      resultText={`${(node.cpu_usage ?? 0).toFixed(1)}%`}
                      variant="standalone"
                    />
                    <ProgressBar
                      value={node.memory_usage ?? 0}
                      label={t("admin.nodes.col.memory")}
                      resultText={`${(node.memory_usage ?? 0).toFixed(1)}%`}
                      variant="standalone"
                    />
                  </SpaceBetween>
                </Container>
              );
            })}
          </Grid>
        </Container>

        <Table
          header={
            <Header
              variant="h2"
              counter={`(${filteredOnlineUsers.length})`}
              actions={
                <Select
                  selectedOption={onlineNodeOptions.find((o) => o.value === onlineNodeFilter) ?? onlineNodeOptions[0] ?? null}
                  options={onlineNodeOptions}
                  onChange={({ detail }) => setOnlineNodeFilter(detail.selectedOption.value ?? "all")}
                />
              }
            >
              {t("admin.dashboard.onlineUsers")}
            </Header>
          }
          columnDefinitions={[
            { id: "email", header: t("admin.dashboard.col.email"), cell: (item: OnlineUser) => item.email },
            { id: "device", header: t("admin.dashboard.col.device"), cell: (item: OnlineUser) => item.device },
            { id: "node_name", header: t("admin.dashboard.col.node"), cell: (item: OnlineUser) => item.node_name },
          ]}
          items={filteredOnlineUsers}
          empty={
            <Box textAlign="center" padding="l">
              <StatusIndicator type="info">{t("admin.dashboard.noOnlineUsers")}</StatusIndicator>
            </Box>
          }
          variant="container"
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
