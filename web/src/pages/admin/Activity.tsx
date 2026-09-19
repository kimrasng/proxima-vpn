import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Badge,
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  Header,
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
import { getActivity } from "../../api/admin";
import type { ActivityEntry, AlertSeverity } from "../../api/types";
import { useManualRefresh } from "../../hooks/useManualRefresh";

// Matches the node agents' 10s heartbeat, so a change on a node reaches
// the screen within roughly one beat plus one poll.
const REFRESH_INTERVAL = 10000;
const PAGE_SIZE = 25;
// The activity endpoint caps at 200 rows, so asking for more silently returns 20.
const FETCH_LIMIT = 200;

const badgeColor: Record<AlertSeverity, "red" | "severity-medium" | "blue" | "green"> = {
  error: "red",
  warning: "severity-medium",
  info: "blue",
  success: "green",
};

function indicatorType(severity: AlertSeverity) {
  return severity === "info" ? "info" : severity;
}

function formatDateTime(iso: string): string {
  const date = new Date(iso);
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })}`;
}

export default function Activity() {
  const { t } = useTranslation();

  const [entries, setEntries] = useState<ActivityEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [severityFilter, setSeverityFilter] = useState("all");
  const [eventFilter, setEventFilter] = useState("all");

  const fetchEntries = useCallback(async () => {
    try {
      setEntries(await getActivity(FETCH_LIMIT));
      setError(null);
    } catch {
      setError(t("admin.activity.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  const { refreshing, lastUpdated, refresh } = useManualRefresh(fetchEntries, REFRESH_INTERVAL);

  const describeEvent = useCallback(
    (entry: ActivityEntry) => {
      const key = `admin.dashboard.event.${entry.event_type}`;
      const translated = t(key);
      return translated === key ? t("admin.dashboard.event.unknown") : translated;
    },
    [t],
  );

  const summary = useMemo(() => {
    const counts = { error: 0, warning: 0, today: 0 };
    const startOfDay = new Date();
    startOfDay.setHours(0, 0, 0, 0);
    for (const entry of entries) {
      if (entry.severity === "error") counts.error++;
      else if (entry.severity === "warning") counts.warning++;
      if (new Date(entry.created_at) >= startOfDay) counts.today++;
    }
    return counts;
  }, [entries]);

  const severityOptions = useMemo<SelectProps.Options>(
    () => [
      { value: "all", label: t("admin.activity.filterSeverityAll") },
      { value: "error", label: t("admin.activity.severityError") },
      { value: "warning", label: t("admin.activity.severityWarning") },
      { value: "success", label: t("admin.activity.severitySuccess") },
      { value: "info", label: t("admin.activity.severityInfo") },
    ],
    [t],
  );

  const eventOptions = useMemo<SelectProps.Options>(() => {
    const types = Array.from(new Set(entries.map((e) => e.event_type))).sort();
    return [
      { value: "all", label: t("admin.activity.filterEventAll") },
      ...types.map((type) => {
        const key = `admin.dashboard.event.${type}`;
        const translated = t(key);
        return { value: type, label: translated === key ? type : translated };
      }),
    ];
  }, [entries, t]);

  const { items, collectionProps, filterProps, filteredItemsCount, paginationProps, actions } =
    useCollection(entries, {
      filtering: {
        empty: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <SpaceBetween size="xxs">
              <Box variant="strong" color="inherit">
                {t("admin.activity.empty")}
              </Box>
              <Box variant="small" color="inherit">
                {t("admin.activity.emptyHint")}
              </Box>
            </SpaceBetween>
          </Box>
        ),
        noMatch: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <Box variant="strong" color="inherit">
              {t("admin.activity.noMatch")}
            </Box>
          </Box>
        ),
        filteringFunction: (item, filteringText) => {
          if (severityFilter !== "all" && item.severity !== severityFilter) return false;
          if (eventFilter !== "all" && item.event_type !== eventFilter) return false;
          const text = filteringText.trim().toLowerCase();
          if (!text) return true;
          return [item.actor_label, item.actor_type, item.event_type, describeEvent(item)].some(
            (field) => (field ?? "").toLowerCase().includes(text),
          );
        },
      },
      sorting: {},
      pagination: { pageSize: PAGE_SIZE },
    });

  const filtersActive =
    Boolean(filterProps.filteringText) || severityFilter !== "all" || eventFilter !== "all";

  const clearFilters = () => {
    actions.setFiltering("");
    setSeverityFilter("all");
    setEventFilter("all");
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.activity.title")}</Header>}>
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
              <span>{t("admin.activity.subtitle")}</span>
              {lastUpdated && (
                <Box variant="small" color="text-status-inactive">
                  {`· ${t("admin.activity.lastUpdated", { time: lastUpdated.toLocaleTimeString() })}`}
                </Box>
              )}
            </SpaceBetween>
          }
          actions={
            <Button
              iconName="refresh"
              ariaLabel={t("admin.activity.refresh")}
              loading={refreshing}
              onClick={refresh}
            />
          }
        >
          {t("admin.activity.title")}
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
              <Box variant="awsui-key-label">{t("admin.activity.kpi.total")}</Box>
              <Box fontSize="heading-xl" fontWeight="bold">
                {entries.length}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.activity.kpi.today")}</Box>
              <Box fontSize="heading-xl" fontWeight="bold">
                {summary.today}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.activity.kpi.error")}</Box>
              <Box
                fontSize="heading-xl"
                fontWeight="bold"
                color={summary.error > 0 ? "text-status-error" : "inherit"}
              >
                {summary.error}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.activity.kpi.warning")}</Box>
              <Box
                fontSize="heading-xl"
                fontWeight="bold"
                color={summary.warning > 0 ? "text-status-warning" : "inherit"}
              >
                {summary.warning}
              </Box>
            </div>
          </ColumnLayout>
        </Container>

        <Table
          {...collectionProps}
          variant="container"
          contentDensity="compact"
          wrapLines
          items={items}
          trackBy="id"
          header={
            <Header
              counter={
                filteredItemsCount !== undefined && filteredItemsCount !== entries.length
                  ? `(${filteredItemsCount}/${entries.length})`
                  : `(${entries.length})`
              }
              description={t("admin.activity.tableHint")}
            >
              {t("admin.activity.tableTitle")}
            </Header>
          }
          filter={
            <SpaceBetween direction="horizontal" size="xs" alignItems="center">
              <TextFilter
                {...filterProps}
                filteringPlaceholder={t("admin.activity.searchPlaceholder")}
                filteringAriaLabel={t("admin.activity.searchPlaceholder")}
                countText={
                  filtersActive
                    ? t("admin.activity.matchCount", { count: filteredItemsCount ?? 0 })
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
                ariaLabel={t("admin.activity.filterSeverityAll")}
              />
              <Select
                selectedOption={
                  eventOptions.find((o) => "value" in o && o.value === eventFilter) ?? null
                }
                options={eventOptions}
                onChange={({ detail }) => {
                  setEventFilter(detail.selectedOption.value ?? "all");
                  actions.setCurrentPage(1);
                }}
                ariaLabel={t("admin.activity.filterEventAll")}
              />
              {filtersActive && (
                <Button variant="link" onClick={clearFilters}>
                  {t("admin.activity.clearFilters")}
                </Button>
              )}
            </SpaceBetween>
          }
          pagination={<Pagination {...paginationProps} />}
          columnDefinitions={[
            {
              id: "severity",
              header: t("admin.activity.col.severity"),
              sortingField: "severity",
              minWidth: 110,
              cell: (item) => (
                <Badge color={badgeColor[item.severity]}>
                  {t(`admin.activity.severity${item.severity.charAt(0).toUpperCase()}${item.severity.slice(1)}`)}
                </Badge>
              ),
            },
            {
              id: "actor",
              header: t("admin.activity.col.actor"),
              sortingField: "actor_label",
              minWidth: 160,
              cell: (item) => (
                <SpaceBetween size="xxxs">
                  <Box variant="strong" fontSize="body-s">
                    {item.actor_label || item.actor_type}
                  </Box>
                  <Box variant="small" color="text-body-secondary">
                    {item.actor_type}
                  </Box>
                </SpaceBetween>
              ),
            },
            {
              id: "event",
              header: t("admin.dashboard.col.event"),
              sortingField: "event_type",
              minWidth: 240,
              cell: (item) => (
                <StatusIndicator type={indicatorType(item.severity)}>
                  <Box variant="span" fontSize="body-s">
                    {describeEvent(item)}
                  </Box>
                </StatusIndicator>
              ),
            },
            {
              id: "target",
              header: t("admin.activity.col.target"),
              cell: (item) =>
                item.target_type ? (
                  <Box variant="small" color="text-body-secondary">
                    {item.target_type}
                  </Box>
                ) : (
                  <Box color="text-status-inactive">—</Box>
                ),
            },
            {
              id: "time",
              header: t("admin.dashboard.col.time"),
              sortingField: "created_at",
              minWidth: 170,
              cell: (item) => formatDateTime(item.created_at),
            },
          ]}
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
