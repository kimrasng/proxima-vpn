import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Badge,
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  Header,
  Link,
  Pagination,
  Select,
  type SelectProps,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  TextFilter,
} from "@cloudscape-design/components";
import { useCollection } from "@cloudscape-design/collection-hooks";
import { getDashboardAlerts, listNodes } from "../../api/admin";
import type { AlertSeverity, DashboardAlerts, Node, NodeIssue } from "../../api/types";
import { useManualRefresh } from "../../hooks/useManualRefresh";

// Matches the node agents' 10s heartbeat, so a change on a node reaches
// the screen within roughly one beat plus one poll.
const REFRESH_INTERVAL = 10000;
const PAGE_SIZE = 25;

const SEVERITY_RANK: Record<AlertSeverity, number> = {
  error: 0,
  warning: 1,
  info: 2,
  success: 3,
};

const badgeColor: Record<AlertSeverity, "red" | "severity-medium" | "blue" | "green"> = {
  error: "red",
  warning: "severity-medium",
  info: "blue",
  success: "green",
};

function indicatorType(severity: AlertSeverity) {
  return severity === "info" ? "info" : severity;
}

export default function Alerts() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const [alerts, setAlerts] = useState<DashboardAlerts | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [severityFilter, setSeverityFilter] = useState("all");
  const [kindFilter, setKindFilter] = useState("all");

  const fetchAll = useCallback(async () => {
    try {
      const [alertsData, nodesData] = await Promise.all([getDashboardAlerts(), listNodes()]);
      setAlerts(alertsData);
      setNodes(nodesData);
      setError(null);
    } catch {
      setError(t("admin.alerts.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  const { refreshing, lastUpdated, refresh } = useManualRefresh(fetchAll, REFRESH_INTERVAL);

  const issues = useMemo(
    () =>
      [...(alerts?.node_issues ?? [])].sort(
        (a, b) =>
          SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity] ||
          a.node_name.localeCompare(b.node_name),
      ),
    [alerts],
  );

  const summary = useMemo(() => {
    const counts = { error: 0, warning: 0, info: 0 };
    for (const issue of issues) {
      if (issue.severity === "error") counts.error++;
      else if (issue.severity === "warning") counts.warning++;
      else counts.info++;
    }
    const affected = new Set(issues.map((i) => i.node_id)).size;
    return { ...counts, affected };
  }, [issues]);

  const severityOptions = useMemo<SelectProps.Options>(
    () => [
      { value: "all", label: t("admin.alerts.filterSeverityAll") },
      { value: "error", label: t("admin.alerts.severityError") },
      { value: "warning", label: t("admin.alerts.severityWarning") },
      { value: "info", label: t("admin.alerts.severityInfo") },
    ],
    [t],
  );

  const kindOptions = useMemo<SelectProps.Options>(() => {
    const kinds = Array.from(new Set(issues.map((i) => i.kind))).sort();
    return [
      { value: "all", label: t("admin.alerts.filterKindAll") },
      ...kinds.map((kind) => ({ value: kind, label: t(`admin.dashboard.issue.${kind}`) })),
    ];
  }, [issues, t]);

  const { items, collectionProps, filterProps, filteredItemsCount, paginationProps, actions } =
    useCollection(issues, {
      filtering: {
        empty: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <SpaceBetween size="xxs">
              <Box variant="strong" color="inherit">
                {t("admin.alerts.empty")}
              </Box>
              <Box variant="small" color="inherit">
                {t("admin.alerts.emptyHint")}
              </Box>
            </SpaceBetween>
          </Box>
        ),
        noMatch: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <Box variant="strong" color="inherit">
              {t("admin.alerts.noMatch")}
            </Box>
          </Box>
        ),
        filteringFunction: (item, filteringText) => {
          if (severityFilter !== "all" && item.severity !== severityFilter) return false;
          if (kindFilter !== "all" && item.kind !== kindFilter) return false;
          const text = filteringText.trim().toLowerCase();
          if (!text) return true;
          return [item.node_name, item.country, item.region, item.kind].some((field) =>
            (field ?? "").toLowerCase().includes(text),
          );
        },
      },
      sorting: {},
      pagination: { pageSize: PAGE_SIZE },
    });

  const renderReading = (issue: NodeIssue) => {
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

  const nodeStatus = useMemo(() => new Map(nodes.map((n) => [n.id, n.status])), [nodes]);

  const filtersActive =
    Boolean(filterProps.filteringText) || severityFilter !== "all" || kindFilter !== "all";

  const clearFilters = () => {
    actions.setFiltering("");
    setSeverityFilter("all");
    setKindFilter("all");
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.alerts.title")}</Header>}>
        <Box textAlign="center" padding="xxl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={
            <SpaceBetween size="xxs" direction="horizontal" alignItems="center">
              <span>{t("admin.alerts.subtitle")}</span>
              {lastUpdated && (
                <Box variant="small" color="text-status-inactive">
                  {`· ${t("admin.alerts.lastUpdated", { time: lastUpdated.toLocaleTimeString() })}`}
                </Box>
              )}
            </SpaceBetween>
          }
          actions={
            <Button
              iconName="refresh"
              ariaLabel={t("admin.alerts.refresh")}
              loading={refreshing}
              onClick={refresh}
            />
          }
        >
          {t("admin.alerts.title")}
        </Header>
      }
    >
      <SpaceBetween size="m">
        {error && (
          <Flashbar
            items={[
              { type: "error", content: error, dismissible: true, onDismiss: () => setError(null) },
            ]}
          />
        )}

        <Container>
          <ColumnLayout columns={4} variant="text-grid" minColumnWidth={160}>
            <div>
              <Box variant="awsui-key-label">{t("admin.alerts.kpi.total")}</Box>
              <Box fontSize="heading-xl" fontWeight="bold">
                {alerts?.total ?? 0}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.alerts.kpi.error")}</Box>
              <Box
                fontSize="heading-xl"
                fontWeight="bold"
                color={summary.error > 0 ? "text-status-error" : "inherit"}
              >
                {summary.error}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.alerts.kpi.warning")}</Box>
              <Box
                fontSize="heading-xl"
                fontWeight="bold"
                color={summary.warning > 0 ? "text-status-warning" : "inherit"}
              >
                {summary.warning}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.alerts.kpi.affectedNodes")}</Box>
              <Box fontSize="heading-xl" fontWeight="bold">
                {summary.affected}
              </Box>
            </div>
          </ColumnLayout>
        </Container>

        <Container
          header={
            <Header
              variant="h2"
              counter={`(${alerts?.items.length ?? 0})`}
              description={t("admin.alerts.summaryHint")}
            >
              {t("admin.alerts.summaryTitle")}
            </Header>
          }
        >
          <Table
            variant="embedded"
            contentDensity="compact"
            items={alerts?.items ?? []}
            trackBy="kind"
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
                id: "count",
                header: t("admin.alerts.col.count"),
                minWidth: 90,
                cell: (item) => item.count,
              },
              {
                id: "action",
                header: t("admin.dashboard.col.action"),
                minWidth: 120,
                cell: (item) =>
                  item.kind === "pending_requests" ? (
                    <Link onFollow={() => navigate("/admin/plan-requests")}>
                      {t("admin.alerts.goToRequests")}
                    </Link>
                  ) : (
                    <Link onFollow={() => navigate("/admin/nodes")}>
                      {t("admin.alerts.goToNodes")}
                    </Link>
                  ),
              },
            ]}
            empty={
              <Box textAlign="center" padding="m">
                <StatusIndicator type="success">{t("admin.dashboard.noAlerts")}</StatusIndicator>
              </Box>
            }
          />
        </Container>

        <Table
          {...collectionProps}
          variant="container"
          contentDensity="compact"
          wrapLines
          items={items}
          trackBy={(item) => `${item.node_id}:${item.kind}`}
          header={
            <Header
              counter={
                filteredItemsCount !== undefined && filteredItemsCount !== issues.length
                  ? `(${filteredItemsCount}/${issues.length})`
                  : `(${issues.length})`
              }
              description={t("admin.alerts.tableHint")}
            >
              {t("admin.alerts.tableTitle")}
            </Header>
          }
          filter={
            <SpaceBetween direction="horizontal" size="xs" alignItems="center">
              <TextFilter
                {...filterProps}
                filteringPlaceholder={t("admin.alerts.searchPlaceholder")}
                filteringAriaLabel={t("admin.alerts.searchPlaceholder")}
                countText={
                  filtersActive
                    ? t("admin.alerts.matchCount", { count: filteredItemsCount ?? 0 })
                    : ""
                }
              />
              <Select
                selectedOption={
                  severityOptions.find((o) => "value" in o && o.value === severityFilter) ?? null
                }
                options={severityOptions}
                onChange={({ detail }) => {
                  setSeverityFilter(detail.selectedOption.value ?? "all");
                  actions.setCurrentPage(1);
                }}
                ariaLabel={t("admin.alerts.filterSeverityAll")}
              />
              <Select
                selectedOption={
                  kindOptions.find((o) => "value" in o && o.value === kindFilter) ?? null
                }
                options={kindOptions}
                onChange={({ detail }) => {
                  setKindFilter(detail.selectedOption.value ?? "all");
                  actions.setCurrentPage(1);
                }}
                ariaLabel={t("admin.alerts.filterKindAll")}
              />
              {filtersActive && (
                <Button variant="link" onClick={clearFilters}>
                  {t("admin.alerts.clearFilters")}
                </Button>
              )}
            </SpaceBetween>
          }
          pagination={<Pagination {...paginationProps} />}
          columnDefinitions={[
            {
              id: "node",
              header: t("admin.nodes.col.name"),
              sortingField: "node_name",
              minWidth: 180,
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
              id: "issue",
              header: t("admin.dashboard.col.issue"),
              sortingField: "kind",
              minWidth: 130,
              cell: (item) => (
                <Badge color={badgeColor[item.severity]}>
                  {t(`admin.dashboard.issue.${item.kind}`)}
                </Badge>
              ),
            },
            {
              id: "reading",
              header: t("admin.dashboard.col.reading"),
              sortingField: "value",
              cell: renderReading,
            },
            {
              id: "status",
              header: t("admin.nodes.col.status"),
              cell: (item) => {
                const status = nodeStatus.get(item.node_id) ?? item.status;
                if (status === "online") {
                  return (
                    <StatusIndicator type="success">
                      {t("admin.nodes.statusOnline")}
                    </StatusIndicator>
                  );
                }
                if (status === "offline") {
                  return (
                    <StatusIndicator type="error">{t("admin.nodes.statusOffline")}</StatusIndicator>
                  );
                }
                return (
                  <StatusIndicator type="pending">
                    {t("admin.nodes.statusPending")}
                  </StatusIndicator>
                );
              },
            },
          ]}
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
