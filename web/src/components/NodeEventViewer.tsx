import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  Container,
  Header,
  Icon,
  type IconProps,
  Multiselect,
  type MultiselectProps,
  Pagination,
  Select,
  type SelectProps,
  SpaceBetween,
  StatusIndicator,
  Table,
} from "@cloudscape-design/components";
import { getNodeEventFilters, getNodeEvents } from "../api/admin";
import type { ActivityEntry } from "../api/types";

const PAGE_SIZE = 20;

const SEVERITY_ICON: Record<
  ActivityEntry["severity"],
  { name: IconProps.Name; variant: IconProps.Variant }
> = {
  error: { name: "status-negative", variant: "error" },
  warning: { name: "status-warning", variant: "warning" },
  success: { name: "status-positive", variant: "success" },
  info: { name: "status-info", variant: "subtle" },
};

const RANGE_HOURS: Record<string, number | undefined> = {
  all: undefined,
  "24h": 24,
  "7d": 24 * 7,
  "30d": 24 * 30,
};

function formatTime(iso: string): string {
  const date = new Date(iso);
  const time = date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  const isToday = date.toDateString() === new Date().toDateString();
  return isToday
    ? time
    : `${date.toLocaleDateString(undefined, { month: "short", day: "numeric" })} ${time}`;
}

/**
 * Renders the parts of an event's detail payload that are not already shown in
 * another column, so an operator sees what actually changed without a raw JSON
 * dump. Keys carrying bookkeeping rather than information are dropped.
 */
function formatDetail(detail: Record<string, unknown>): string {
  const skip = new Set(["node", "_seed", "n"]);
  return Object.entries(detail)
    .filter(([key, value]) => !skip.has(key) && value !== null && value !== "")
    .map(([key, value]) => `${key}=${Array.isArray(value) ? value.join("/") : String(value)}`)
    .join("  ");
}

export function NodeEventViewer({ nodeId }: { nodeId: string }) {
  const { t } = useTranslation();

  const [page, setPage] = useState<{
    items: ActivityEntry[];
    total: number;
  }>({ items: [], total: 0 });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [pageIndex, setPageIndex] = useState(1);

  const [availableTypes, setAvailableTypes] = useState<string[]>([]);
  const [selectedTypes, setSelectedTypes] = useState<readonly SelectProps.Option[]>([]);
  const [selectedSeverities, setSelectedSeverities] = useState<readonly SelectProps.Option[]>([]);
  const [range, setRange] = useState("all");

  useEffect(() => {
    let active = true;
    getNodeEventFilters()
      .then((options) => {
        if (active) setAvailableTypes(options.event_types);
      })
      .catch(() => {
        // The viewer still works with an empty type filter, so a failure here
        // must not blank the whole panel.
        if (active) setAvailableTypes([]);
      });
    return () => {
      active = false;
    };
  }, []);

  const eventTypes = useMemo(
    () => selectedTypes.map((o) => o.value ?? "").filter(Boolean),
    [selectedTypes],
  );
  const severities = useMemo(
    () => selectedSeverities.map((o) => o.value ?? "").filter(Boolean),
    [selectedSeverities],
  );
  const filtered = eventTypes.length > 0 || severities.length > 0 || range !== "all";

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await getNodeEvents(nodeId, {
        eventTypes,
        severities,
        hours: RANGE_HOURS[range],
        limit: PAGE_SIZE,
        offset: (pageIndex - 1) * PAGE_SIZE,
      });
      setPage({ items: result.items, total: result.total });
      setError(false);
    } catch {
      setError(true);
    } finally {
      setLoading(false);
    }
  }, [nodeId, eventTypes, severities, range, pageIndex]);

  useEffect(() => {
    void load();
  }, [load]);

  // A narrowed filter can leave the viewer on a page that no longer exists.
  useEffect(() => {
    const pages = Math.max(1, Math.ceil(page.total / PAGE_SIZE));
    if (pageIndex > pages) setPageIndex(pages);
  }, [page.total, pageIndex]);

  const eventLabel = (entry: ActivityEntry) => {
    const key = `admin.dashboard.event.${entry.event_type}`;
    const translated = t(key);
    return translated === key ? entry.event_type : translated;
  };

  const typeOptions: MultiselectProps.Options = availableTypes.map((type) => ({
    value: type,
    label: (() => {
      const key = `admin.dashboard.event.${type}`;
      const translated = t(key);
      return translated === key ? type : translated;
    })(),
    description: type,
  }));

  const severityOptions: MultiselectProps.Options = (
    ["info", "success", "warning", "error"] as const
  ).map((s) => ({ value: s, label: t(`admin.nodeEvents.severity.${s}`) }));

  const pagesCount = Math.max(1, Math.ceil(page.total / PAGE_SIZE));
  const from = page.total === 0 ? 0 : (pageIndex - 1) * PAGE_SIZE + 1;
  const to = Math.min(pageIndex * PAGE_SIZE, page.total);

  const clearFilters = () => {
    setSelectedTypes([]);
    setSelectedSeverities([]);
    setRange("all");
    setPageIndex(1);
  };

  return (
    <Container
      header={
        <Header
          variant="h2"
          counter={page.total > 0 ? `(${page.total})` : undefined}
          description={
            page.total > 0 ? t("admin.nodeEvents.showing", { from, to, total: page.total }) : undefined
          }
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                iconName="refresh"
                ariaLabel={t("admin.nodes.refresh")}
                loading={loading}
                onClick={() => void load()}
              />
              {filtered && <Button onClick={clearFilters}>{t("admin.nodeEvents.clearFilters")}</Button>}
            </SpaceBetween>
          }
        >
          {t("admin.nodeEvents.title")}
        </Header>
      }
    >
      <SpaceBetween size="m">
        <SpaceBetween direction="horizontal" size="xs">
          <Multiselect
            selectedOptions={selectedTypes}
            options={typeOptions}
            onChange={({ detail }) => {
              setSelectedTypes(detail.selectedOptions);
              setPageIndex(1);
            }}
            placeholder={t("admin.nodeEvents.filterTypeAll")}
            ariaLabel={t("admin.nodeEvents.filterType")}
            filteringType="auto"
            tokenLimit={2}
          />
          <Multiselect
            selectedOptions={selectedSeverities}
            options={severityOptions}
            onChange={({ detail }) => {
              setSelectedSeverities(detail.selectedOptions);
              setPageIndex(1);
            }}
            placeholder={t("admin.nodeEvents.filterSeverityAll")}
            ariaLabel={t("admin.nodeEvents.filterSeverity")}
            tokenLimit={2}
          />
          <Select
            selectedOption={{ value: range, label: t(`admin.nodeEvents.range${rangeSuffix(range)}`) }}
            options={Object.keys(RANGE_HOURS).map((key) => ({
              value: key,
              label: t(`admin.nodeEvents.range${rangeSuffix(key)}`),
            }))}
            onChange={({ detail }) => {
              setRange(detail.selectedOption.value ?? "all");
              setPageIndex(1);
            }}
            ariaLabel={t("admin.nodeEvents.filterRange")}
          />
        </SpaceBetween>

        {error ? (
          <Box textAlign="center" padding="l">
            <StatusIndicator type="error">{t("admin.nodeEvents.error")}</StatusIndicator>
          </Box>
        ) : (
          <Table
            variant="embedded"
            loading={loading}
            loadingText={t("admin.nodeEvents.title")}
            items={page.items}
            ariaLabels={{ tableLabel: t("admin.nodeEvents.title") }}
            empty={
              <Box textAlign="center" padding="l">
                <StatusIndicator type="info">
                  {filtered ? t("admin.nodeEvents.emptyFiltered") : t("admin.nodeEvents.empty")}
                </StatusIndicator>
              </Box>
            }
            columnDefinitions={[
              {
                id: "time",
                header: t("admin.nodeEvents.colTime"),
                width: 130,
                cell: (item) => (
                  <Box variant="small" color="text-body-secondary">
                    {formatTime(item.created_at)}
                  </Box>
                ),
              },
              {
                id: "severity",
                header: t("admin.nodeEvents.colSeverity"),
                width: 110,
                cell: (item) => (
                  <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
                    <Icon
                      name={SEVERITY_ICON[item.severity].name}
                      variant={SEVERITY_ICON[item.severity].variant}
                    />
                    <Box variant="small">{t(`admin.nodeEvents.severity.${item.severity}`)}</Box>
                  </SpaceBetween>
                ),
              },
              {
                id: "event",
                header: t("admin.nodeEvents.colEvent"),
                cell: (item) => eventLabel(item),
              },
              {
                id: "actor",
                header: t("admin.nodeEvents.colActor"),
                width: 170,
                cell: (item) => (
                  <Box variant="small">{item.actor_label || item.actor_type}</Box>
                ),
              },
              {
                id: "detail",
                header: t("admin.nodeEvents.colDetail"),
                cell: (item) => {
                  const text = formatDetail(item.detail);
                  return text ? (
                    <Box variant="small" color="text-body-secondary">
                      <span style={{ fontFamily: "monospace" }}>{text}</span>
                    </Box>
                  ) : (
                    <Box color="text-status-inactive">—</Box>
                  );
                },
              },
            ]}
          />
        )}

        {pagesCount > 1 && (
          <Box textAlign="center">
            <Pagination
              currentPageIndex={pageIndex}
              pagesCount={pagesCount}
              onChange={({ detail }) => setPageIndex(detail.currentPageIndex)}
              ariaLabels={{
                paginationLabel: t("admin.nodeEvents.pagination"),
                previousPageLabel: t("admin.nodeEvents.prevPage"),
                nextPageLabel: t("admin.nodeEvents.nextPage"),
                pageLabel: (page) => t("admin.nodeEvents.pageLabel", { page }),
              }}
            />
          </Box>
        )}
      </SpaceBetween>
    </Container>
  );
}

function rangeSuffix(key: string): string {
  return key === "all" ? "All" : key === "24h" ? "24h" : key === "7d" ? "7d" : "30d";
}
