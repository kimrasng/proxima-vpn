import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Badge,
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  Header,
  Link,
  Modal,
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
import { getOnlineUsers, terminateSession } from "../../api/admin";
import type { OnlineUser } from "../../api/types";
import { formatRelativeTime } from "../../utils/relativeTime";
import { useManualRefresh } from "../../hooks/useManualRefresh";

// Matches the node agents' 10s heartbeat, so a change on a node reaches
// the screen within roughly one beat plus one poll.
const REFRESH_INTERVAL = 10000;
const PAGE_SIZE = 25;

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

export default function Connections() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const [sessions, setSessions] = useState<OnlineUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [nodeFilter, setNodeFilter] = useState("all");
  const [capFilter, setCapFilter] = useState("all");
  const [terminating, setTerminating] = useState<OnlineUser | null>(null);
  const [terminateBusy, setTerminateBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const fetchSessions = useCallback(async () => {
    try {
      setSessions(await getOnlineUsers());
      setError(null);
    } catch {
      setError(t("admin.connections.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  const { refreshing, lastUpdated, refresh } = useManualRefresh(fetchSessions, REFRESH_INTERVAL);

  const summary = useMemo(() => {
    const users = new Set(sessions.map((s) => s.email));
    const nodes = new Set(sessions.map((s) => s.node_name).filter(Boolean));
    const addresses = new Set(
      sessions.flatMap((s) => (s.addresses ?? []).map((a) => `${s.email}|${a.ip}`)),
    );
    const overCap = new Set(sessions.filter((s) => s.over_cap).map((s) => s.email));
    return {
      users: users.size,
      devices: sessions.length,
      nodes: nodes.size,
      addresses: addresses.size,
      overCap: overCap.size,
    };
  }, [sessions]);

  const nodeOptions = useMemo<SelectProps.Options>(() => {
    const names = Array.from(new Set(sessions.map((s) => s.node_name).filter(Boolean))).sort();
    return [
      { value: "all", label: t("admin.connections.filterNodeAll") },
      ...names.map((name) => ({ value: name, label: name })),
    ];
  }, [sessions, t]);

  const capOptions = useMemo<SelectProps.Options>(
    () => [
      { value: "all", label: t("admin.connections.filterCapAll") },
      { value: "over", label: t("admin.connections.filterCapOver") },
      { value: "within", label: t("admin.connections.filterCapWithin") },
    ],
    [t],
  );

  const { items, collectionProps, filterProps, filteredItemsCount, paginationProps, actions } =
    useCollection(sessions, {
      filtering: {
        empty: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <SpaceBetween size="xxs">
              <Box variant="strong" color="inherit">
                {t("admin.connections.empty")}
              </Box>
              <Box variant="small" color="inherit">
                {t("admin.connections.emptyHint")}
              </Box>
            </SpaceBetween>
          </Box>
        ),
        noMatch: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <Box variant="strong" color="inherit">
              {t("admin.connections.noMatch")}
            </Box>
          </Box>
        ),
        filteringFunction: (item, filteringText) => {
          if (nodeFilter !== "all" && item.node_name !== nodeFilter) return false;
          if (capFilter === "over" && !item.over_cap) return false;
          if (capFilter === "within" && item.over_cap) return false;
          const text = filteringText.trim().toLowerCase();
          if (!text) return true;
          const haystack = [
            item.email,
            item.name,
            item.device,
            item.node_name,
            ...(item.addresses ?? []).map((a) => a.ip),
          ];
          return haystack.some((field) => (field ?? "").toLowerCase().includes(text));
        },
      },
      sorting: {},
      pagination: { pageSize: PAGE_SIZE },
    });

  const formatDuration = (iso: string | null): string => {
    if (!iso) return t("admin.connections.unknownTime");
    const diffMs = Date.now() - new Date(iso).getTime();
    if (diffMs < 0) return t("admin.connections.justNow");
    const minutes = Math.floor(diffMs / 60000);
    if (minutes < 60) return t("admin.connections.durationMinutes", { count: minutes });
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return t("admin.connections.durationHours", { count: hours });
    return t("admin.connections.durationDays", { count: Math.floor(hours / 24) });
  };

  const filtersActive =
    Boolean(filterProps.filteringText) || nodeFilter !== "all" || capFilter !== "all";

  const clearFilters = () => {
    actions.setFiltering("");
    setNodeFilter("all");
    setCapFilter("all");
  };

  const confirmTerminate = async () => {
    if (!terminating) return;
    setTerminateBusy(true);
    try {
      const result = await terminateSession(terminating.device_id);
      setNotice(
        t("admin.connections.terminateSuccess", {
          device: terminating.device || terminating.email,
          minutes: result.cooldown_minutes,
        }),
      );
      setTerminating(null);
      await fetchSessions();
    } catch {
      setError(t("admin.connections.terminateError"));
    } finally {
      setTerminateBusy(false);
    }
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.connections.title")}</Header>}>
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
              <span>{t("admin.connections.subtitle")}</span>
              {lastUpdated && (
                <Box variant="small" color="text-status-inactive">
                  {`· ${t("admin.connections.lastUpdated", { time: lastUpdated.toLocaleTimeString() })}`}
                </Box>
              )}
            </SpaceBetween>
          }
          actions={
            <Button
              iconName="refresh"
              ariaLabel={t("admin.connections.refresh")}
              loading={refreshing}
              onClick={refresh}
            />
          }
        >
          {t("admin.connections.title")}
        </Header>
      }
    >
      <SpaceBetween size="m">
        {error && (
          <Flashbar
            items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]}
          />
        )}
        {notice && (
          <Flashbar
            items={[
              {
                type: "success",
                content: notice,
                dismissible: true,
                onDismiss: () => setNotice(null),
              },
            ]}
          />
        )}

        <Container>
          <ColumnLayout columns={4} variant="text-grid" minColumnWidth={160}>
            <div>
              <Box variant="awsui-key-label">{t("admin.connections.kpi.users")}</Box>
              <Box fontSize="heading-xl" fontWeight="bold">
                {summary.users}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.connections.kpi.devices")}</Box>
              <Box fontSize="heading-xl" fontWeight="bold">
                {summary.devices}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.connections.kpi.addresses")}</Box>
              <Box fontSize="heading-xl" fontWeight="bold">
                {summary.addresses}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.connections.kpi.overCap")}</Box>
              <Box
                fontSize="heading-xl"
                fontWeight="bold"
                color={summary.overCap > 0 ? "text-status-warning" : "inherit"}
              >
                {summary.overCap}
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
          trackBy="device_id"
          header={
            <Header
              counter={
                filteredItemsCount !== undefined && filteredItemsCount !== sessions.length
                  ? `(${filteredItemsCount}/${sessions.length})`
                  : `(${sessions.length})`
              }
              description={t("admin.connections.tableHint")}
            >
              {t("admin.connections.tableTitle")}
            </Header>
          }
          filter={
            <SpaceBetween direction="horizontal" size="xs" alignItems="center">
              <TextFilter
                {...filterProps}
                filteringPlaceholder={t("admin.connections.searchPlaceholder")}
                filteringAriaLabel={t("admin.connections.searchPlaceholder")}
                countText={
                  filtersActive
                    ? t("admin.connections.matchCount", { count: filteredItemsCount ?? 0 })
                    : ""
                }
              />
              <Select
                selectedOption={
                  nodeOptions.find((o) => "value" in o && o.value === nodeFilter) ?? null
                }
                options={nodeOptions}
                onChange={({ detail }) => {
                  setNodeFilter(detail.selectedOption.value ?? "all");
                  actions.setCurrentPage(1);
                }}
                ariaLabel={t("admin.connections.filterNodeAll")}
              />
              <Select
                selectedOption={
                  capOptions.find((o) => "value" in o && o.value === capFilter) ?? null
                }
                options={capOptions}
                onChange={({ detail }) => {
                  setCapFilter(detail.selectedOption.value ?? "all");
                  actions.setCurrentPage(1);
                }}
                ariaLabel={t("admin.connections.filterCapAll")}
              />
              {filtersActive && (
                <Button variant="link" onClick={clearFilters}>
                  {t("admin.connections.clearFilters")}
                </Button>
              )}
            </SpaceBetween>
          }
          pagination={<Pagination {...paginationProps} />}
          columnDefinitions={[
            {
              id: "user",
              header: t("admin.connections.col.user"),
              sortingField: "email",
              minWidth: 200,
              cell: (item) => (
                <SpaceBetween size="xxxs">
                  <StatusIndicator type={item.over_cap ? "warning" : "success"}>
                    {item.email}
                  </StatusIndicator>
                  {item.name && (
                    <Box variant="small" color="text-body-secondary">
                      {item.name}
                    </Box>
                  )}
                </SpaceBetween>
              ),
            },
            {
              id: "device",
              header: t("admin.connections.col.device"),
              sortingField: "device",
              cell: (item) => item.device || "—",
            },
            {
              id: "node",
              header: t("admin.connections.col.node"),
              sortingField: "node_name",
              cell: (item) =>
                item.node_id ? (
                  <Link onFollow={() => navigate(`/admin/nodes/${item.node_id}`)}>
                    {item.node_name}
                  </Link>
                ) : (
                  item.node_name
                ),
            },
            {
              id: "addresses",
              header: t("admin.connections.col.addresses"),
              minWidth: 230,
              cell: (item) =>
                (item.addresses ?? []).length > 0 ? (
                  <SpaceBetween size="xxxs">
                    {(item.addresses ?? []).map((address) => (
                      <div
                        key={address.ip}
                        style={{ display: "flex", alignItems: "baseline", gap: "8px" }}
                      >
                        <Box fontSize="body-s">
                          <span style={{ fontFamily: "monospace" }}>{address.ip}</span>
                        </Box>
                        <Box variant="small" color="text-body-secondary">
                          {formatRelativeTime(t, address.last_seen)}
                        </Box>
                      </div>
                    ))}
                  </SpaceBetween>
                ) : (
                  <Box color="text-status-inactive">—</Box>
                ),
            },
            {
              id: "connected",
              header: t("admin.connections.col.connectedFor"),
              sortingField: "connected_since",
              cell: (item) => (
                <span title={item.connected_since ? new Date(item.connected_since).toLocaleString() : ""}>
                  {formatDuration(item.connected_since)}
                </span>
              ),
            },
            {
              id: "cap",
              header: t("admin.connections.col.cap"),
              sortingField: "online_ips",
              cell: (item) =>
                item.max_concurrent > 0 ? (
                  <Badge color={item.over_cap ? "severity-medium" : "grey"}>
                    {`${item.online_ips} / ${item.max_concurrent}`}
                  </Badge>
                ) : (
                  <Box variant="small" color="text-body-secondary">
                    {t("admin.connections.noCap", { count: item.online_ips })}
                  </Box>
                ),
            },
            {
              id: "traffic",
              header: t("admin.connections.col.traffic"),
              sortingField: "traffic_today",
              cell: (item) => formatBytes(item.traffic_today),
            },
            {
              id: "actions",
              header: t("admin.connections.col.actions"),
              minWidth: 110,
              cell: (item) => (
                <Button variant="inline-link" onClick={() => setTerminating(item)}>
                  {t("admin.connections.terminate")}
                </Button>
              ),
            },
          ]}
        />
      </SpaceBetween>

      <Modal
        visible={terminating !== null}
        onDismiss={() => setTerminating(null)}
        header={t("admin.connections.terminateTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setTerminating(null)}>
                {t("admin.connections.cancel")}
              </Button>
              <Button
                variant="primary"
                loading={terminateBusy}
                onClick={() => void confirmTerminate()}
              >
                {t("admin.connections.terminate")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        {terminating && (
          <SpaceBetween size="m">
            <Box>
              {t("admin.connections.terminateConfirm", {
                device: terminating.device || t("admin.connections.unnamedDevice"),
                email: terminating.email,
              })}
            </Box>
            <Alert type="info">{t("admin.connections.terminateHint")}</Alert>
          </SpaceBetween>
        )}
      </Modal>
    </ContentLayout>
  );
}
