import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Badge,
  Box,
  ButtonDropdown,
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
import { getDashboardAlerts, acknowledgeAlert, silenceAlert } from "../../api/admin";
import type { AlertSeverity, DashboardAlerts, NodeIssue } from "../../api/types";
import { useManualRefresh } from "../../hooks/useManualRefresh";
import { formatDuration } from "../../utils/relativeTime";

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
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [severityFilter, setSeverityFilter] = useState("all");
  const [kindFilter, setKindFilter] = useState("all");

  const fetchAll = useCallback(async () => {
    try {
      // The alerts response already carries each row's node name, location, and
      // status, so the second request this used to make was redundant.
      setAlerts(await getDashboardAlerts());
      setError(null);
    } catch {
      setError(t("admin.alerts.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  const { refreshing, lastUpdated, refresh } = useManualRefresh(fetchAll, REFRESH_INTERVAL);
  const [actionBusy, setActionBusy] = useState<string | null>(null);

  // Mutating an alert refetches rather than patching local state: the evaluator
  // owns the row, so the server's version is the only one worth trusting.
  const runAction = useCallback(
    async (alertId: string, action: () => Promise<void>) => {
      setActionBusy(alertId);
      try {
        await action();
        await fetchAll();
      } catch {
        setError(t("admin.alerts.actionError"));
      } finally {
        setActionBusy(null);
      }
    },
    [fetchAll, t],
  );

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
      case "xray_down":
        return t("admin.dashboard.issue.xray_downLabel");
      case "shaping_failed":
        return t("admin.dashboard.issue.shaping_failedLabel");
      default:
        return issue.kind;
    }
  };


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
                cell: () => (
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

        <Container
          header={
            <Header variant="h2" description={t("admin.alerts.approvalsHint")}>
              {t("admin.alerts.approvalsTitle")}
            </Header>
          }
        >
          <SpaceBetween direction="horizontal" size="s" alignItems="center">
            {(alerts?.pending_requests ?? 0) > 0 ? (
              <>
                <Badge color="blue">
                  {t("admin.alerts.approvalsCount", { count: alerts?.pending_requests ?? 0 })}
                </Badge>
                <Link onFollow={() => navigate("/admin/plan-requests")}>
                  {t("admin.alerts.goToRequests")}
                </Link>
              </>
            ) : (
              <StatusIndicator type="success">{t("admin.alerts.approvalsNone")}</StatusIndicator>
            )}
          </SpaceBetween>
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
              minWidth: 130,
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
              minWidth: 115,
              cell: (item) => [item.country, item.region].filter(Boolean).join(" · ") || "—",
            },
            {
              id: "issue",
              header: t("admin.dashboard.col.issue"),
              sortingField: "kind",
              minWidth: 130,
              maxWidth: 170,
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
              id: "duration",
              header: t("admin.alerts.col.duration"),
              sortingField: "duration_seconds",
              maxWidth: 90,
              cell: (item) =>
                item.duration_seconds > 0 ? (
                  formatDuration(t, item.duration_seconds)
                ) : (
                  <Box color="text-status-inactive">—</Box>
                ),
            },
            {
              id: "lifecycle",
              header: t("admin.alerts.col.lifecycle"),
              minWidth: 150,
              cell: (item) => {
                const silenced =
                  item.silenced_until != null && new Date(item.silenced_until) > new Date();
                if (item.state === "stale") {
                  return (
                    <StatusIndicator type="pending">{t("admin.alerts.stateStale")}</StatusIndicator>
                  );
                }
                if (silenced) {
                  return <Badge color="grey">{t("admin.alerts.stateSilenced")}</Badge>;
                }
                if (item.acked) {
                  return <Badge color="blue">{t("admin.alerts.stateAcked")}</Badge>;
                }
                // One colour per lifecycle state. Keying this off severity gave two
                // different colours to the identical word "Firing", which reads as
                // an undocumented sub-state; severity is already the Issue badge's
                // job one column over.
                return <Badge color="red">{t("admin.alerts.stateFiring")}</Badge>;
              },
            },
            {
              id: "actions",
              header: "",
              minWidth: 60,
              cell: (item) => (
                <ButtonDropdown
                  variant="inline-icon"
                  ariaLabel={t("admin.alerts.col.actions")}
                  expandToViewport
                  loading={actionBusy === item.alert_id}
                  items={[
                    {
                      id: item.acked ? "unack" : "ack",
                      text: item.acked ? t("admin.alerts.unack") : t("admin.alerts.ack"),
                    },
                    { id: "silence-15", text: t("admin.alerts.silenceFor", { minutes: 15 }) },
                    { id: "silence-60", text: t("admin.alerts.silenceFor", { minutes: 60 }) },
                    { id: "silence-240", text: t("admin.alerts.silenceFor", { minutes: 240 }) },
                    { id: "silence-1440", text: t("admin.alerts.silenceForDay") },
                    {
                      id: "unsilence",
                      text: t("admin.alerts.unsilence"),
                      disabled:
                        item.silenced_until == null ||
                        new Date(item.silenced_until) <= new Date(),
                    },
                  ]}
                  onItemClick={({ detail }) => {
                    if (detail.id === "ack" || detail.id === "unack") {
                      void runAction(item.alert_id, () =>
                        acknowledgeAlert(item.alert_id, detail.id === "ack"),
                      );
                      return;
                    }
                    if (detail.id === "unsilence") {
                      void runAction(item.alert_id, () => silenceAlert(item.alert_id, 0));
                      return;
                    }
                    const minutes = Number(detail.id.replace("silence-", ""));
                    void runAction(item.alert_id, () => silenceAlert(item.alert_id, minutes));
                  }}
                />
              ),
            },
          ]}
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
