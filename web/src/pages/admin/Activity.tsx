import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert, Box, Button, Cards, Container, ContentLayout, Header, Modal, Pagination,
  PropertyFilter, Select, SpaceBetween, StatusIndicator, Table, Toggle,
} from "@cloudscape-design/components";
import { useCollection } from "@cloudscape-design/collection-hooks";
import { getActivity } from "../../api/admin";
import type { ActivityEntry } from "../../api/types";
import { ActivityDetails } from "./ActivityDetails";
import "./activity.css";

const SEVERITIES = ["error", "warning", "info", "success"] as const;
const SUMMARY_TYPES = ["all", "error", "warning", "info"] as const;
const RANGE_HOURS: Readonly<Record<string, number>> = { all: 0, "24h": 24, "7d": 168, "30d": 720 };
const DETAIL_STACK_WIDTH = 1550;

export default function Activity() {
  const { t } = useTranslation();
  const [entries, setEntries] = useState<ActivityEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [range, setRange] = useState("all");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const fetching = useRef(false);
  const initialSelection = useRef(false);
  const workspaceRef = useRef<HTMLDivElement>(null);
  const selectionTrigger = useRef<HTMLElement | null>(null);
  const [compactDetails, setCompactDetails] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  useEffect(() => {
    const workspace = workspaceRef.current;
    if (!workspace) return;
    const observer = new ResizeObserver(([entry]) => {
      if (entry) setCompactDetails(entry.contentRect.width <= DETAIL_STACK_WIDTH);
    });
    observer.observe(workspace);
    return () => observer.disconnect();
  }, []);
  const fetchEntries = useCallback(async () => {
    if (fetching.current) return;
    fetching.current = true;
    setRefreshing(true);
    try {
      const fetched = await getActivity(200);
      setEntries(fetched);
      const firstEntry = fetched[0];
      if (!initialSelection.current && firstEntry) {
        setSelectedId(firstEntry.id);
        initialSelection.current = true;
      }
      setLastUpdated(new Date());
      setError(false);
    } catch {
      setError(true);
    } finally {
      fetching.current = false;
      setLoading(false);
      setRefreshing(false);
    }
  }, []);
  useEffect(() => { void fetchEntries(); }, [fetchEntries]);
  useEffect(() => {
    if (!autoRefresh) return;
    const timer = setInterval(() => void fetchEntries(), 10000);
    return () => clearInterval(timer);
  }, [autoRefresh, fetchEntries]);

  const eventLabel = (entry: ActivityEntry) => {
    const key = `admin.dashboard.event.${entry.event_type}`;
    return t(key, { defaultValue: entry.event_type });
  };
  const severityLabel = (severity: string) => t(`admin.nodeEvents.severity.${severity}`);
  const timeEntries = useMemo(() => {
    const hours = RANGE_HOURS[range] ?? 0;
    const cutoff = (lastUpdated?.getTime() ?? Date.now()) - hours * 3600000;
    return entries.filter((entry) => hours === 0 || Date.parse(entry.created_at) >= cutoff);
  }, [entries, range, lastUpdated]);
  const searchableEntries = timeEntries.map((entry) => ({ ...entry, eventLabel: eventLabel(entry) }));
  const { items, allPageItems, collectionProps, propertyFilterProps, paginationProps, filteredItemsCount, actions } =
    useCollection<ActivityEntry>(searchableEntries, {
      propertyFiltering: {
        filteringProperties: [
          { key: "severity", propertyLabel: t("admin.activity.col.severity"), groupValuesLabel: t("admin.activity.col.severity"),
            operators: ["=", "!="].map((operator) => ({ operator, format: (value: unknown) => typeof value === "string" ? severityLabel(value) : "" })) },
          { key: "target_type", propertyLabel: t("admin.activity.col.target"), groupValuesLabel: t("admin.activity.col.target"), operators: ["=", "!="] },
          { key: "event_type", propertyLabel: t("admin.dashboard.col.event"), groupValuesLabel: t("admin.dashboard.col.event"), operators: ["=", "!="] },
        ],
        filteringOptions: [
          ...SEVERITIES.map((severity) => ({ propertyKey: "severity", value: severity, label: severityLabel(severity) })),
          ...Array.from(new Set(entries.flatMap((entry) => entry.target_type ? [entry.target_type] : [])))
            .map((value) => ({ propertyKey: "target_type", value })),
          ...Array.from(new Set(entries.map((entry) => entry.event_type))).map((value) => ({ propertyKey: "event_type", value })),
        ],
        empty: <Box padding="l" textAlign="center">{t("admin.activity.empty")}</Box>,
        noMatch: <Box padding="l" textAlign="center">{t("admin.activity.noMatch")}</Box>,
      },
      sorting: { defaultState: { sortingColumn: { sortingField: "created_at" }, isDescending: true } },
      pagination: { pageSize: 25 },
    });
  const selected = allPageItems.find((entry) => entry.id === selectedId);
  useEffect(() => {
    if (selectedId && !loading && !selected) setSelectedId(null);
  }, [selectedId, selected, loading]);
  const selectedItems = selected ? [selected] : [];
  const severityTokens = propertyFilterProps.query.tokens.filter((token) => token.propertyKey === "severity");
  const activeSummary = severityTokens.length === 0 ? "all"
    : severityTokens.length === 1 && severityTokens[0]?.operator === "=" ? severityTokens[0].value
    : severityTokens.length === 2 && severityTokens.every((token) => token.operator === "!=")
      && severityTokens.some((token) => token.value === "error")
      && severityTokens.some((token) => token.value === "warning") ? "info" : null;
  const summary = SUMMARY_TYPES.map((severity) => ({
    severity,
    label: severity === "all" ? t("admin.activity.kpi.total") : severityLabel(severity),
    count: timeEntries.filter((entry) => severity === "all" || (severity === "info"
      ? entry.severity === "info" || entry.severity === "success"
      : entry.severity === severity)).length,
  }));
  const severityOptions = [
    { value: "all", label: t("admin.activity.filterSeverityAll") },
    ...SEVERITIES.map((severity) => ({ value: severity, label: severityLabel(severity) })),
  ];
  const header = <Header counter={`(${filteredItemsCount ?? 0})`} description={t("admin.activity.tableHint")}
    actions={<Pagination {...paginationProps} />}>{t("admin.activity.tableTitle")}</Header>;
  const selectEntry = (id: string | null) => {
    if (id) {
      selectionTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    }
    setSelectedId(id);
    setDetailsOpen(id !== null);
  };
  const closeDetails = () => {
    setDetailsOpen(false);
    setSelectedId(null);
    requestAnimationFrame(() => selectionTrigger.current?.focus());
  };
  const selection = {
    selectedItems,
    onSelectionChange: ({ detail }: { readonly detail: { readonly selectedItems: readonly ActivityEntry[] } }) => selectEntry(detail.selectedItems[0]?.id ?? null),
    ariaLabels: { selectionGroupLabel: t("admin.activity.tableTitle"), itemSelectionLabel: (_data: unknown, item: ActivityEntry) => `${t("admin.activity.details")}: ${eventLabel(item)}` },
  };
  const columns = [
    { id: "time", header: t("admin.dashboard.col.time"), sortingField: "created_at", minWidth: 200, cell: (item: ActivityEntry) => <span className="activity-log__nowrap">{new Date(item.created_at).toLocaleString()}</span> },
    { id: "severity", header: t("admin.activity.col.severity"), sortingField: "severity", minWidth: 110, cell: (item: ActivityEntry) => <span className="activity-log__nowrap"><StatusIndicator type={item.severity}>{severityLabel(item.severity)}</StatusIndicator></span> },
    { id: "event", header: t("admin.dashboard.col.event"), sortingField: "event_type", minWidth: 280, cell: (item: ActivityEntry) => <Button variant="inline-link" onClick={() => selectEntry(item.id)}>{eventLabel(item)}</Button> },
    { id: "actor", header: t("admin.activity.col.actor"), sortingField: "actor_label", minWidth: 185, cell: (item: ActivityEntry) => item.actor_label || item.actor_type },
    { id: "target", header: t("admin.activity.col.target"), minWidth: 105, cell: (item: ActivityEntry) => item.target_type || "—" },
  ];

  return (
    <ContentLayout header={<Header variant="h1" description={t("admin.activity.subtitle")} actions={
      <SpaceBetween size="s" direction="horizontal" alignItems="center">
        <Button iconName="refresh" loading={refreshing} onClick={() => void fetchEntries()}>{t("admin.activity.refresh")}</Button>
        <Toggle checked={autoRefresh} onChange={({ detail }) => setAutoRefresh(detail.checked)}>{t("admin.activity.autoRefresh")}</Toggle>
      </SpaceBetween>
    }>{t("admin.activity.title")}</Header>}>
      <div className="activity-screen">
        <SpaceBetween size="m">
          {error && <Alert type="error">{t("admin.activity.fetchError")}</Alert>}
          <Cards items={summary} trackBy="severity"
            cardsPerRow={[{ cards: 1 }, { minWidth: 360, cards: 2 }, { minWidth: 700, cards: 4 }]}
            cardDefinition={{ header: (item) => item.label, sections: [{ id: "count", content: (item) => <Box fontSize="display-l" fontWeight="bold">{item.count}</Box> }] }} />
          <Container>
            <div className="activity-toolbar">
              <PropertyFilter {...propertyFilterProps} filteringPlaceholder={t("admin.activity.searchPlaceholder")}
                filteringAriaLabel={t("admin.activity.searchPlaceholder")}
                countText={t("admin.activity.matchCount", { count: filteredItemsCount ?? 0 })}
                onChange={propertyFilterProps.onChange}
                i18nStrings={{ filteringAriaLabel: t("admin.activity.searchPlaceholder"), dismissAriaLabel: t("common.close"),
                  clearFiltersText: t("admin.activity.clearFilters"), applyActionText: t("admin.activity.apply"),
                  cancelActionText: t("common.cancel"), operationAndText: t("admin.activity.and"), operationOrText: t("admin.activity.or"),
                  groupPropertiesText: t("admin.activity.addFilter"), groupValuesText: t("admin.activity.values") }} />
              <Select selectedOption={severityOptions.find((option) => option.value === activeSummary) ?? null}
                options={severityOptions} placeholder={t("admin.activity.filterSeverityAll")}
                ariaLabel={t("admin.activity.filterSeverityAll")}
                onChange={({ detail }) => {
                  const severity = detail.selectedOption.value ?? "all";
                  actions.setPropertyFiltering({ operation: "and", tokens: [
                    ...propertyFilterProps.query.tokens.filter((token) => token.propertyKey !== "severity"),
                    ...(severity === "all" ? [] : severity === "info" ? [
                      { propertyKey: "severity", operator: "!=" as const, value: "error" },
                      { propertyKey: "severity", operator: "!=" as const, value: "warning" },
                    ] : [{ propertyKey: "severity", operator: "=" as const, value: severity }]),
                  ] });
                  actions.setCurrentPage(1);
                }} />
              <Select selectedOption={{ value: range, label: t(`admin.nodeEvents.range${range === "all" ? "All" : range}`) }}
                options={Object.keys(RANGE_HOURS).map((value) => ({ value, label: t(`admin.nodeEvents.range${value === "all" ? "All" : value}`) }))}
                ariaLabel={t("admin.nodeEvents.filterRange")} onChange={({ detail }) => {
                  setRange(detail.selectedOption.value ?? "all"); actions.setCurrentPage(1);
                }} />
            </div>
          </Container>
          <div ref={workspaceRef} className={selected && !compactDetails ? "activity-workspace activity-workspace--selected" : "activity-workspace"}>
            <div className="activity-log">
              <div className="activity-log__table">
                <Table {...collectionProps} {...selection} items={items} trackBy="id" selectionType="single"
                  onRowClick={({ detail }) => selectEntry(detail.item.id)} loading={loading}
                  loadingText={t("admin.activity.tableTitle")} header={header} contentDensity="compact" wrapLines
                  columnDefinitions={columns} />
              </div>
              <div className="activity-log__cards">
                <Cards {...selection} items={items} trackBy="id" selectionType="single" loading={loading}
                  header={header} empty={collectionProps.empty}
                  cardsPerRow={[{ cards: 1 }]} cardDefinition={{
                    header: (item) => <Button variant="inline-link" onClick={() => selectEntry(item.id)}>{eventLabel(item)}</Button>,
                    sections: columns.filter((column) => column.id !== "event").map((column) => ({ id: column.id, header: column.header, content: column.cell })),
                  }} />
              </div>
            </div>
            {selected && !compactDetails && <ActivityDetails entry={entries.find((entry) => entry.id === selected.id) ?? selected}
              eventLabel={eventLabel(selected)} onClose={closeDetails} />}
          </div>
          <Modal visible={compactDetails && detailsOpen && !!selected} onDismiss={closeDetails}
            header={t("admin.activity.details")} size="large">
            {selected && <ActivityDetails entry={entries.find((entry) => entry.id === selected.id) ?? selected}
              eventLabel={eventLabel(selected)} onClose={closeDetails} />}
          </Modal>
          {lastUpdated && <Box variant="small" color="text-body-secondary">{t("admin.activity.lastUpdated", { time: lastUpdated.toLocaleTimeString() })}</Box>}
        </SpaceBetween>
      </div>
    </ContentLayout>
  );
}
