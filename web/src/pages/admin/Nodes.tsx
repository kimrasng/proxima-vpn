import { useState, useMemo, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  Badge,
  Box,
  type BoxProps,
  Button,
  ButtonDropdown,
  CollectionPreferences,
  type CollectionPreferencesProps,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  FormField,
  Header,
  Input,
  Modal,
  Pagination,
  Popover,
  ProgressBar,
  Select,
  type SelectProps,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  Tabs,
  TextFilter,
} from "@cloudscape-design/components";
import { useCollection } from "@cloudscape-design/collection-hooks";
import { listNodes, listNodeChains, deleteNode, updateNode, getNode } from "../../api/admin";
import type {
  Node,
  NodeChain,
  NodeFirewallPreset,
  NodeOSFamily,
  NodeRole,
  UpdateNodeRequest,
} from "../../api/types";
import { formatAbsoluteTime, formatRelativeTime } from "../../utils/relativeTime";
import { formatPorts, parsePortInput, resolvePorts } from "../../utils/nodePorts";
import { HoverTooltip } from "../../components/HoverTooltip";
import { NodeProvisionWizard } from "../../components/NodeProvisionWizard";
import { NodeEndpointState } from "../../components/NodeEndpointState";
import { NodeSNIEditor } from "../../components/NodeSNIEditor";
import { useManualRefresh } from "../../hooks/useManualRefresh";
import "./nodesTable.css";

// Matches the node agents' 10s heartbeat, so a change on a node reaches
// the screen within roughly one beat plus one poll.
const REFRESH_INTERVAL = 10000;
const DEFAULT_PAGE_SIZE = 20;
const NODE_VIEWS = ["all", "entry", "exit"] as const;
type NodeView = (typeof NODE_VIEWS)[number];
const ROLE_BADGE_COLORS = { relay: "green", exit: "blue", both: "grey" } as const;

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
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

/**
 * Converts an ISO 3166-1 alpha-2 country code to its flag emoji. Node `country`
 * is free-form text, so anything that is not exactly two letters yields no flag.
 */
function countryFlag(code: string): string {
  const trimmed = code.trim();
  if (!/^[A-Za-z]{2}$/.test(trimmed)) return "";
  return String.fromCodePoint(
    ...trimmed
      .toUpperCase()
      .split("")
      .map((char) => 0x1f1e6 + char.charCodeAt(0) - 65),
  );
}

/**
 * Compact inline usage bar: label on the left, thin bar with its percentage on
 * the right.
 *
 * `stale` is for a node that has stopped reporting. The figure is whatever it
 * last sent - possibly hours old - so it is dimmed, drops its severity colour
 * (a red bar on a node that is not running reads as a live problem), and says
 * on hover how old it actually is.
 */
function UsageCell({
  label,
  value,
  stale,
  ageLabel,
}: {
  label: string;
  value: number | undefined;
  stale?: boolean;
  /** Age of the figure, shown on hover. Only meaningful when stale. */
  ageLabel?: string;
}) {
  return (
    <div
      style={{ display: "flex", alignItems: "center", gap: "8px" }}
      title={stale ? ageLabel : undefined}
    >
      <Box variant="small" color="text-body-secondary">
        <span style={{ display: "inline-block", minWidth: "44px" }}>{label}</span>
      </Box>
      {value != null ? (
        <div style={{ flex: 1, minWidth: "72px", opacity: stale ? 0.4 : 1 }}>
          <ProgressBar
            value={value}
            status={!stale && getUsageStatus(value) === "error" ? "error" : "in-progress"}
            variant="key-value"
            ariaLabel={label}
          />
        </div>
      ) : (
        <Box variant="small" color="text-status-inactive">
          —
        </Box>
      )}
    </div>
  );
}

function KpiTile({
  label,
  value,
  color,
  caption,
}: {
  label: string;
  value: number;
  color?: BoxProps.Color;
  caption?: string;
}) {
  return (
    <SpaceBetween size="xxxs">
      <Box variant="awsui-key-label">{label}</Box>
      <Box fontSize="display-l" fontWeight="bold" color={color ?? "inherit"}>
        {value}
      </Box>
      {caption && (
        <Box variant="small" color="text-body-secondary">
          {caption}
        </Box>
      )}
    </SpaceBetween>
  );
}

export default function Nodes() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [nodes, setNodes] = useState<Node[]>([]);
  const [chains, setChains] = useState<NodeChain[]>([]);
  const [searchParams, setSearchParams] = useSearchParams();
  const nodeView: NodeView = NODE_VIEWS.find((view) => view === searchParams.get("view")) ?? "all";
  const [publicationFilter, setPublicationFilter] = useState("all");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [provisionWizard, setProvisionWizard] = useState(false);
  const [deleteModal, setDeleteModal] = useState<Node | null>(null);
  const [editModal, setEditModal] = useState<Node | null>(null);
  const [sniNode, setSniNode] = useState<Node | null>(null);
  const [sniSaved, setSniSaved] = useState(false);
  const [editForm, setEditForm] = useState({
    name: "",
    country: "",
    region: "",
    trafficMultiplier: "1",
    osFamily: "",
    role: "exit" as NodeRole,
    publishDirect: true,
    maxConns: "0",
    firewallPreset: "standard",
    customPorts: "",
    labels: {} as Record<string, string>,
  });
  const [editSuccess, setEditSuccess] = useState(false);
  const [actionLoading, setActionLoading] = useState(false);

  const [statusFilter, setStatusFilter] = useState<string>("all");
  const [regionFilter, setRegionFilter] = useState<string>("all");
  const [preferences, setPreferences] = useState<CollectionPreferencesProps.Preferences>({
    pageSize: DEFAULT_PAGE_SIZE,
    wrapLines: false,
    contentDisplay: [
      { id: "name", visible: true },
      { id: "role", visible: true },
      { id: "country", visible: true },
      { id: "ip", visible: false },
      { id: "entryDns", visible: false },
      { id: "realitySni", visible: false },
      { id: "routes", visible: true },
      { id: "status", visible: true },
      { id: "resources", visible: false },
      { id: "traffic", visible: true },
      { id: "connections", visible: true },
      { id: "multiplier", visible: true },
      { id: "lastCheck", visible: true },
      { id: "health", visible: false },
      { id: "actions", visible: true },
    ],
  });

  const fetchNodes = useCallback(async () => {
    try {
      const [nodeList, chainList] = await Promise.all([listNodes(), listNodeChains()]);
      setNodes(nodeList);
      setChains(chainList);
      setError(null);
    } catch {
      setError(t("admin.nodes.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  const { refreshing, lastUpdated, refresh } = useManualRefresh(fetchNodes, REFRESH_INTERVAL);

  const healthSummary = useMemo(() => {
    const online = nodes.filter((n) => n.status === "online").length;
    const offline = nodes.filter((n) => n.status === "offline").length;
    return { total: nodes.length, online, offline, pending: nodes.length - online - offline };
  }, [nodes]);

  const nodeViews = useMemo(() => {
    const assigned = new Set<string>();
    for (const chain of chains) {
      if (chain.group_ids.length === 0) continue;
      assigned.add(chain.exit_node_id);
      if (chain.entry_node_id) assigned.add(chain.entry_node_id);
    }
    return {
      assigned,
      all: nodes,
      entry: nodes.filter((node) => node.role === "relay" || node.role === "both"),
      exit: nodes.filter((node) => node.role === "exit" || node.role === "both"),
    };
  }, [nodes, chains]);
  const visibleNodes = nodeViews[nodeView];

  const regionOptions = useMemo<SelectProps.Options>(() => {
    const regions = Array.from(new Set(visibleNodes.map((n) => n.region).filter(Boolean))).sort();
    return [
      { value: "all", label: t("admin.nodes.filterRegionAll") },
      ...regions.map((region) => ({ value: region, label: region })),
    ];
  }, [visibleNodes, t]);

  const statusOptions = useMemo<SelectProps.Options>(
    () => [
      { value: "all", label: t("admin.nodes.filterStatusAll") },
      { value: "online", label: t("admin.nodes.statusOnline") },
      { value: "offline", label: t("admin.nodes.statusOffline") },
      { value: "pending", label: t("admin.nodes.statusPending") },
    ],
    [t],
  );

  const statusLabel = useCallback(
    (status: string) => {
      if (status === "online") return t("admin.nodes.statusOnline");
      if (status === "offline") return t("admin.nodes.statusOffline");
      return t("admin.nodes.statusPending");
    },
    [t],
  );

  const { items, collectionProps, filterProps, filteredItemsCount, paginationProps, actions } =
    useCollection(visibleNodes, {
      filtering: {
        empty: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <Box variant="strong" color="inherit">
              {t(nodeView === "all" ? "admin.nodes.empty" : "admin.nodes.views.empty")}
            </Box>
          </Box>
        ),
        noMatch: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <SpaceBetween size="xxs">
              <Box variant="strong" color="inherit">
                {t("admin.nodes.noMatch")}
              </Box>
              <Box variant="small" color="inherit">
                {t("admin.nodes.noMatchSubtitle")}
              </Box>
            </SpaceBetween>
          </Box>
        ),
        filteringFunction: (item, filteringText) => {
          if (publicationFilter === "assigned" && !nodeViews.assigned.has(item.id)) return false;
          if (publicationFilter === "unassigned" && nodeViews.assigned.has(item.id)) return false;
          if (statusFilter !== "all") {
            const normalized =
              item.status === "online" || item.status === "offline" ? item.status : "pending";
            if (normalized !== statusFilter) return false;
          }
          if (regionFilter !== "all" && item.region !== regionFilter) return false;
          const text = filteringText.trim().toLowerCase();
          if (!text) return true;
          return [item.name, item.ip, item.country, item.region].some((field) =>
            (field ?? "").toLowerCase().includes(text),
          );
        },
      },
      sorting: {},
      pagination: { pageSize: preferences.pageSize ?? DEFAULT_PAGE_SIZE },
    });

  const filtersActive =
    Boolean(filterProps.filteringText) || statusFilter !== "all" || regionFilter !== "all" || publicationFilter !== "all";

  const clearFilters = () => {
    actions.setFiltering("");
    setStatusFilter("all");
    setRegionFilter("all");
    setPublicationFilter("all");
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deleteNode(deleteModal.id);
      setDeleteModal(null);
      await fetchNodes();
    } catch {
      setError(t("admin.nodes.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEditOpen = (node: Node) => {
    setEditModal(node);
    setEditForm({
      name: node.name,
      country: node.country,
      region: node.region,
      trafficMultiplier: String(node.traffic_multiplier ?? 1),
      osFamily: node.os_family ?? "",
      role: node.role,
      publishDirect: node.publish_direct,
      maxConns: String(node.max_concurrent_conns ?? 0),
      firewallPreset: node.firewall_preset || "standard",
      customPorts: "",
      labels: {},
    });
    // The row from the list endpoint never carries labels; fetch the node
    // detail response that does.
    void getNode(node.id).then((full) => {
      setEditForm((f) => ({ ...f, labels: full.labels ?? {} }));
    });
  };

  const handleEditSubmit = async () => {
    if (!editModal) return;
    setActionLoading(true);
    try {
      const multiplier = Number(editForm.trafficMultiplier);
      if (!Number.isFinite(multiplier) || multiplier <= 0 || multiplier > 100) {
        setError(t("admin.nodes.multiplierInvalid"));
        return;
      }
      const maxConns = Number(editForm.maxConns);
      if (!Number.isInteger(maxConns) || maxConns < 0) {
        setError(t("admin.nodes.wizard.maxConnsInvalid"));
        return;
      }

      const preset = editForm.firewallPreset as NodeFirewallPreset;
      const parsedCustom = parsePortInput(editForm.customPorts);
      if (preset === "custom") {
        if (parsedCustom.invalid.length > 0) {
          setError(
            t("admin.nodes.wizard.portsInvalid", { entries: parsedCustom.invalid.join(", ") }),
          );
          return;
        }
        if (parsedCustom.specs.length === 0) {
          setError(t("admin.nodes.wizard.portsRequired"));
          return;
        }
      }

      const req: UpdateNodeRequest = {
        name: editForm.name,
        country: editForm.country,
        region: editForm.region,
        traffic_multiplier: multiplier,
        max_concurrent_conns: maxConns,
        role: editForm.role,
        publish_direct: editForm.publishDirect,
        firewall_preset: preset,
        custom_ports: preset === "custom" ? parsedCustom.specs : undefined,
        labels: editForm.labels,
      };
      if (editForm.osFamily !== "") {
        req.os_family = editForm.osFamily as NodeOSFamily;
      }
      await updateNode(editModal.id, req);
      setEditModal(null);
      setEditSuccess(true);
      await fetchNodes();
    } catch {
      setError(t("admin.nodes.editError"));
    } finally {
      setActionLoading(false);
    }
  };

  const nodeHealthProblems = (node: Node): string[] => {
    const problems: string[] = [];
    if (node.shaping_ok === false) problems.push(t("admin.nodes.shapingNotApplied"));
    if (node.xray_too_old) problems.push(t("admin.nodes.xrayTooOld", { minimum: node.xray_minimum }));
    return problems;
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.nodes.title")}</Header>}>
        <Box textAlign="center" padding="xxl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  const kpiRatio = (count: number) =>
    healthSummary.total ? Math.round((count / healthSummary.total) * 100) : 0;

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={
            <SpaceBetween size="xxs" direction="horizontal" alignItems="center">
              <span>{t("admin.nodes.subtitle")}</span>
              {lastUpdated && (
                <Box variant="small" color="text-status-inactive">
                  {`· ${t("admin.nodes.lastUpdated", { time: lastUpdated.toLocaleString() })}`}
                </Box>
              )}
            </SpaceBetween>
          }
          actions={
            <SpaceBetween direction="horizontal" size="xs" alignItems="center">
              <Button
                iconName="refresh"
                ariaLabel={t("admin.nodes.refresh")}
                loading={refreshing}
                onClick={refresh}
              />
              <Button
                variant="primary"
                iconName="add-plus"
                onClick={() => setProvisionWizard(true)}
              >
                {t("admin.nodes.createNode")}
              </Button>
            </SpaceBetween>
          }
        >
          {t("admin.nodes.title")}
        </Header>
      }
    >
      <SpaceBetween size="m">
        {sniSaved && <Flashbar items={[{ type: "success", content: t("admin.nodes.endpoints.saved"), dismissible: true, onDismiss: () => setSniSaved(false) }]} />}
        {error && (
          <Flashbar
            items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]}
          />
        )}
        {editSuccess && (
          <Flashbar
            items={[
              {
                type: "success",
                content: t("admin.nodes.editSuccess"),
                dismissible: true,
                onDismiss: () => setEditSuccess(false),
              },
            ]}
          />
        )}

        <Container>
          <ColumnLayout columns={4} variant="text-grid">
            <KpiTile
              label={t("admin.nodes.kpi.total")}
              value={healthSummary.total}
              caption={t("admin.nodes.kpi.totalCaption")}
            />
            <KpiTile
              label={t("admin.nodes.kpi.online")}
              value={healthSummary.online}
              color="text-status-success"
              caption={t("admin.nodes.kpi.shareOfTotal", { percent: kpiRatio(healthSummary.online) })}
            />
            <KpiTile
              label={t("admin.nodes.kpi.offline")}
              value={healthSummary.offline}
              color={healthSummary.offline > 0 ? "text-status-error" : "inherit"}
              caption={t("admin.nodes.kpi.shareOfTotal", { percent: kpiRatio(healthSummary.offline) })}
            />
            <KpiTile
              label={t("admin.nodes.kpi.pending")}
              value={healthSummary.pending}
              color="text-status-inactive"
              caption={t("admin.nodes.kpi.shareOfTotal", { percent: kpiRatio(healthSummary.pending) })}
            />
          </ColumnLayout>
        </Container>

        <Tabs
          activeTabId={nodeView}
          onChange={({ detail }) => {
            const view = NODE_VIEWS.find((candidate) => candidate === detail.activeTabId);
            if (!view) return;
            setSearchParams((params) => {
              params.set("view", view);
              return params;
            }, { replace: true });
            actions.setCurrentPage(1);
          }}
          tabs={NODE_VIEWS.map((view) => ({
            id: view,
            label: t(`admin.routeManagement.nodeViews.${view}`),
            content: <Box variant="small" color="text-body-secondary">{t(`admin.routeManagement.nodeViews.${view}Hint`)}</Box>,
          }))}
        />

        <div className="nodes-table">
          <Table
            {...collectionProps}
            variant="container"
            contentDensity="compact"
            stickyHeader
            wrapLines={preferences.wrapLines}
            columnDisplay={preferences.contentDisplay}
            items={items}
            trackBy="id"
            filter={
              <SpaceBetween direction="horizontal" size="xs" alignItems="center">
                <TextFilter
                  {...filterProps}
                  filteringPlaceholder={t("admin.nodes.searchPlaceholder")}
                  filteringAriaLabel={t("admin.nodes.searchPlaceholder")}
                  countText={
                    filtersActive ? t("admin.nodes.matchCount", { count: filteredItemsCount ?? 0 }) : ""
                  }
                />
                <Select
                  selectedOption={
                    statusOptions.find((option) => "value" in option && option.value === statusFilter) ??
                    null
                  }
                  options={statusOptions}
                  onChange={({ detail }) => {
                    setStatusFilter(detail.selectedOption.value ?? "all");
                    actions.setCurrentPage(1);
                  }}
                  ariaLabel={t("admin.nodes.filterStatusAll")}
                />
                <Select
                  selectedOption={
                    regionOptions.find((option) => "value" in option && option.value === regionFilter) ??
                    null
                  }
                  options={regionOptions}
                  onChange={({ detail }) => {
                    setRegionFilter(detail.selectedOption.value ?? "all");
                    actions.setCurrentPage(1);
                  }}
                  ariaLabel={t("admin.nodes.filterRegionAll")}
                />
                <Select
                  selectedOption={{ value: publicationFilter, label: t(`admin.routeManagement.publication.${publicationFilter}`) }}
                  options={["all", "assigned", "unassigned"].map((value) => ({ value, label: t(`admin.routeManagement.publication.${value}`) }))}
                  onChange={({ detail }) => { setPublicationFilter(detail.selectedOption.value ?? "all"); actions.setCurrentPage(1); }}
                  ariaLabel={t("admin.routeManagement.publication.label")}
                />
                {filtersActive && (
                  <Button variant="link" onClick={clearFilters}>
                    {t("admin.nodes.clearFilters")}
                  </Button>
                )}
              </SpaceBetween>
            }
            pagination={
              <Pagination
                {...paginationProps}
                ariaLabels={{ paginationLabel: t("admin.nodes.paginationLabel") }}
              />
            }
            preferences={
              <CollectionPreferences
                title={t("admin.nodes.preferencesTitle")}
                confirmLabel={t("admin.nodes.save")}
                cancelLabel={t("admin.nodes.cancel")}
                preferences={preferences}
                onConfirm={({ detail }) => setPreferences(detail)}
                pageSizePreference={{
                  title: t("admin.nodes.pageSize"),
                  options: [10, 20, 50].map((size) => ({
                    value: size,
                    label: t("admin.nodes.pageSizeOption", { count: size }),
                  })),
                }}
                wrapLinesPreference={{ label: t("admin.nodes.wrapLines"), description: "" }}
                contentDisplayPreference={{
                  title: t("admin.nodes.visibleColumns"),
                  options: [
                    { id: "name", label: t("admin.nodes.col.name"), alwaysVisible: true },
                    { id: "role", label: t("admin.nodes.role.label") },
                    { id: "country", label: t("admin.nodes.col.countryRegion") },
                    { id: "ip", label: t("admin.nodes.col.ip") },
                    { id: "entryDns", label: t("admin.nodes.endpoints.dns") },
                    { id: "realitySni", label: t("admin.nodes.endpoints.sni") },
                    { id: "status", label: t("admin.nodes.col.status") },
                    { id: "routes", label: t("admin.routeManagement.routes") },
                    { id: "resources", label: t("admin.nodes.col.resources") },
                    { id: "traffic", label: t("admin.nodes.col.traffic") },
                    { id: "connections", label: t("admin.nodes.col.connections") },
                    { id: "multiplier", label: t("admin.nodes.col.multiplier") },
                    { id: "lastCheck", label: t("admin.nodes.col.lastCheck") },
                    { id: "health", label: t("admin.nodes.col.health") },
                    { id: "actions", label: t("admin.nodes.col.actions") },
                  ],
                }}
              />
            }
            columnDefinitions={[
              {
                id: "routes", header: t("admin.routeManagement.routes"),
                cell: (item) => <Button variant="inline-link" onClick={() => navigate(`/admin/node-chains?${item.role === "both" ? "node" : item.role === "exit" ? "exit" : "entry"}=${encodeURIComponent(item.id)}`)}>
                  {t("admin.routeManagement.routeCount", { count: chains.filter((chain) => chain.entry_node_id === item.id || chain.exit_node_id === item.id).length })}
                </Button>,
              },
              { id: "entryDns", header: t("admin.nodes.endpoints.dns"), hasDynamicContent: true, cell: (item) => <NodeEndpointState node={item} kind="dns" /> },
              {
                id: "realitySni",
                hasDynamicContent: true,
                header: (
                  <div className="nodes-table__header">
                    <span>{t("admin.nodes.endpoints.sni")}</span>
                    <Popover content={t("admin.nodes.endpoints.publicationHint")} triggerType="custom">
                      <Button variant="inline-icon" iconName="status-info" ariaLabel={t("admin.nodes.endpoints.sni")} />
                    </Popover>
                  </div>
                ),
                cell: (item) => <NodeEndpointState node={item} kind="sni" />,
              },
              {
                id: "name",
                header: t("admin.nodes.col.name"),
                sortingField: "name",
                // "pending" is GenerateToken's marker for a node provisioned without a name.
                cell: (item) =>
                  item.status === "pending" ? (
                    <SpaceBetween direction="horizontal" size="xs" alignItems="center">
                      <Badge color="grey">{t("admin.nodes.statusPending")}</Badge>
                      {item.name !== "" && item.name !== "pending" && (
                        <Box color="text-body-secondary">{item.name}</Box>
                      )}
                    </SpaceBetween>
                  ) : (
                    <span className="nodes-table__identity">
                      <Button variant="inline-link" onClick={() => navigate(`/admin/nodes/${item.id}`)}>
                        {item.name}
                      </Button>
                    </span>
                  ),
              },
              {
                id: "role",
                header: t("admin.nodes.role.label"),
                cell: (item) => <span className="nodes-table__identity"><Badge color={ROLE_BADGE_COLORS[item.role]}>{t(`admin.nodes.role.${item.role}`)}</Badge></span>,
              },
              {
                id: "country",
                header: t("admin.nodes.col.countryRegion"),
                sortingField: "country",
                cell: (item) =>
                  item.country === "" && item.region === "" ? (
                    <Box color="text-status-inactive">—</Box>
                  ) : (
                    `${countryFlag(item.country)} ${item.country || "—"}${
                      item.region ? ` / ${item.region}` : ""
                    }`.trim()
                  ),
              },
              {
                id: "ip",
                header: t("admin.nodes.col.ip"),
                sortingField: "ip",
                cell: (item) =>
                  item.status === "pending" ? <Box color="text-status-inactive">—</Box> : item.ip,
              },
              {
                id: "status",
                header: t("admin.nodes.col.status"),
                sortingField: "status",
                cell: (item) => (
                  <HoverTooltip
                    content={
                      <SpaceBetween size="xxxs">
                        <Box variant="small" color="inherit">
                          {item.status_changed_at
                            ? t("admin.nodes.statusChangedAt", {
                                relative: formatRelativeTime(t, item.status_changed_at),
                                absolute: formatAbsoluteTime(item.status_changed_at),
                              })
                            : t("admin.nodes.statusChangedUnknown")}
                        </Box>
                        <Box variant="small" color="inherit">
                          {t("admin.nodes.statusLastSeen", {
                            relative: formatRelativeTime(t, item.last_seen),
                          })}
                        </Box>
                      </SpaceBetween>
                    }
                  >
                    <StatusIndicator type={getStatusIndicatorType(item.status)}>
                      {statusLabel(item.status)}
                    </StatusIndicator>
                  </HoverTooltip>
                ),
              },
              {
                id: "resources",
                header: (
                  <div className="nodes-table__header">
                    <span>{t("admin.nodes.col.resources")}</span>
                    <Popover
                      dismissButton={false}
                      position="top"
                      size="small"
                      triggerType="custom"
                      content={t("admin.nodes.resourcesInfo")}
                    >
                      <Button variant="inline-icon" iconName="status-info" ariaLabel="info" />
                    </Popover>
                  </div>
                ),
                minWidth: 165,
                cell: (item) => {
                  // Only an online node is reporting; anything else is showing its
                  // last known figures.
                  const stale = item.status !== "online";
                  const ageLabel = stale
                    ? t("admin.nodes.statusLastSeen", {
                        relative: formatRelativeTime(t, item.last_seen),
                      })
                    : undefined;
                  return (
                    <SpaceBetween size="xxxs">
                      <UsageCell
                        label={t("admin.nodes.col.cpu")}
                        value={item.cpu_usage}
                        stale={stale}
                        ageLabel={ageLabel}
                      />
                      <UsageCell
                        label={t("admin.nodes.col.memory")}
                        value={item.memory_usage}
                        stale={stale}
                        ageLabel={ageLabel}
                      />
                    </SpaceBetween>
                  );
                },
              },
              {
                id: "traffic",
                header: t("admin.nodes.col.traffic"),
                cell: (item) =>
                  item.network_in != null && item.network_out != null ? (
                    <SpaceBetween size="xxxs">
                      <Box fontSize="body-s">↓ {formatBytes(item.network_in)}</Box>
                      <Box fontSize="body-s">↑ {formatBytes(item.network_out)}</Box>
                    </SpaceBetween>
                  ) : (
                    <Box color="text-status-inactive">—</Box>
                  ),
              },
              {
                id: "connections",
                header: t("admin.nodes.col.connections"),
                sortingField: "online_devices",
                cell: (item) =>
                  item.status === "pending" ? (
                    <Box color="text-status-inactive">—</Box>
                  ) : (
                    `${item.online_devices} / ${item.capacity}`
                  ),
              },
              {
                id: "multiplier",
                header: t("admin.nodes.col.multiplier"),
                sortingField: "traffic_multiplier",
                cell: (item) => {
                  const factor = item.traffic_multiplier ?? 1;
                  return factor === 1 ? (
                    <Box variant="small" color="text-body-secondary">
                      {t("admin.nodes.multiplierNormal")}
                    </Box>
                  ) : (
                    <Badge color={factor > 1 ? "severity-medium" : "green"}>
                      {t("admin.nodes.multiplierValue", { factor })}
                    </Badge>
                  );
                },
              },
              {
                id: "lastCheck",
                header: t("admin.nodes.col.lastCheck"),
                sortingField: "last_seen",
                cell: (item) => (
                  <span title={formatAbsoluteTime(item.last_seen)}>
                    {formatRelativeTime(t, item.last_seen)}
                  </span>
                ),
              },
              {
                id: "health",
                header: t("admin.nodes.col.health"),
                cell: (item) => {
                  if (item.status === "pending") return <Box color="text-status-inactive">—</Box>;
                  const problems = nodeHealthProblems(item);
                  if (problems.length === 0) {
                    return <StatusIndicator type="success">{t("admin.nodes.healthOk")}</StatusIndicator>;
                  }
                  return (
                    <SpaceBetween size="xxxs">
                      {problems.map((problem) => (
                        <StatusIndicator key={problem} type="warning">
                          {problem}
                        </StatusIndicator>
                      ))}
                    </SpaceBetween>
                  );
                },
              },
              {
                id: "actions",
                header: "",
                cell: (item) => (
                  <ButtonDropdown
                    variant="inline-icon"
                    ariaLabel={t("admin.nodes.col.actions")}
                    expandToViewport
                    items={[
                      { id: "details", text: t("admin.nodes.details"), disabled: item.status === "pending" },
                      { id: "edit", text: t("admin.nodes.edit"), disabled: item.status === "pending" },
                      ...((item.status === "online" || item.status === "offline") && (item.role === "exit" || item.role === "both") && (item.reality_sni_status === "valid" || item.reality_sni_status === "conflict")
                        ? [{ id: "sni", text: t("admin.nodes.endpoints.edit") }] : []),
                      ...(item.role !== "relay" ? [{
                        id: "inbounds",
                        text: t("admin.nodes.panel.inbounds"),
                        disabled: item.status === "pending",
                      }] : []),
                      { id: "delete", text: t("admin.nodes.delete") },
                    ]}
                    onItemClick={({ detail }) => {
                      if (detail.id === "sni") { setSniSaved(false); setSniNode(item); return; }
                      if (detail.id === "details") {
                        navigate(`/admin/nodes/${item.id}`);
                      } else if (detail.id === "edit") {
                        handleEditOpen(item);
                      } else if (detail.id === "inbounds") {
                        navigate(`/admin/nodes/${item.id}/inbounds`);
                      } else if (detail.id === "delete") {
                        setDeleteModal(item);
                      }
                    }}
                  />
                ),
              },
            ]}
            header={
              <Header
                counter={
                  filteredItemsCount !== undefined && filteredItemsCount !== visibleNodes.length
                    ? `(${filteredItemsCount}/${visibleNodes.length})`
                    : `(${visibleNodes.length})`
                }
              >
                {t("admin.nodes.listTitle")}
              </Header>
            }
          />
        </div>

        <NodeProvisionWizard
          visible={provisionWizard}
          onDismiss={() => setProvisionWizard(false)}
          onProvisioned={() => void fetchNodes()}
        />

        {sniNode && <NodeSNIEditor node={sniNode} onDismiss={() => setSniNode(null)} onSaved={() => { setSniNode(null); setSniSaved(true); void fetchNodes(); }} />}

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header={t("admin.nodes.deleteConfirmTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.nodes.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.nodes.confirmDelete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.nodes.deleteConfirmMessage", { name: deleteModal?.name })}
        </Modal>

        <Modal
          visible={editModal !== null}
          onDismiss={() => setEditModal(null)}
          size="large"
          header={t("admin.nodes.editModalTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setEditModal(null)}>{t("admin.nodes.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleEditSubmit()}>
                  {t("admin.nodes.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <SpaceBetween size="l">
            <Container
              header={<Header variant="h3">{t("admin.nodes.editSections.reportedTitle")}</Header>}
            >
              <ColumnLayout columns={3} variant="text-grid">
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.ipReadOnly")}</Box>
                  <Box>{editModal?.ip}</Box>
                </div>
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.portReadOnly")}</Box>
                  <Box>{editModal?.port}</Box>
                </div>
                <div>
                  <Box variant="awsui-key-label">{t("admin.nodes.editSections.firewallPortsCurrent")}</Box>
                  <Box>
                    {editModal?.firewall_ports ? (
                      <span style={{ fontFamily: "monospace" }}>{editModal.firewall_ports}</span>
                    ) : (
                      <Box color="text-status-inactive">—</Box>
                    )}
                  </Box>
                </div>
              </ColumnLayout>
            </Container>

            <Container header={<Header variant="h3">{t("admin.nodes.editSections.identityTitle")}</Header>}>
              <SpaceBetween size="m">
                <FormField label={t("admin.nodes.role.label")} description={t("admin.nodes.role.restartHint")}>
                  <Select
                    selectedOption={{ value: editForm.role, label: t(`admin.nodes.role.${editForm.role}`) }}
                    options={(["exit", "relay", "both"] as const).map((value) => ({
                      value,
                      label: t(`admin.nodes.role.${value}`),
                    }))}
                    onChange={({ detail }) => {
                      const selected = detail.selectedOption.value;
                      if (selected === "exit" || selected === "relay" || selected === "both") {
                        setEditForm((form) => ({ ...form, role: selected }));
                      }
                    }}
                  />
                </FormField>
                {editForm.role !== "relay" && (
                  <FormField label={t("admin.nodes.role.publishDirect")} description={t("admin.nodes.role.publishDirectHint")}>
                    <Select
                      selectedOption={{ value: String(editForm.publishDirect), label: t(`admin.nodes.role.${editForm.publishDirect ? "published" : "relayOnly"}`) }}
                      options={[
                        { value: "true", label: t("admin.nodes.role.published") },
                        { value: "false", label: t("admin.nodes.role.relayOnly") },
                      ]}
                      onChange={({ detail }) =>
                        setEditForm((form) => ({ ...form, publishDirect: detail.selectedOption.value === "true" }))
                      }
                    />
                  </FormField>
                )}
                <FormField label={t("admin.nodes.col.name")}>
                  <Input
                    value={editForm.name}
                    onChange={({ detail }) => setEditForm((f) => ({ ...f, name: detail.value }))}
                  />
                </FormField>
                <FormField label={t("admin.nodes.col.country")}>
                  <Input
                    value={editForm.country}
                    onChange={({ detail }) => setEditForm((f) => ({ ...f, country: detail.value }))}
                  />
                </FormField>
                <FormField label={t("admin.nodes.region")}>
                  <Input
                    value={editForm.region}
                    onChange={({ detail }) => setEditForm((f) => ({ ...f, region: detail.value }))}
                  />
                </FormField>
                <FormField
                  label={t("admin.nodes.wizard.osFamily")}
                  description={t("admin.nodes.editSections.osFamilyHint")}
                >
                  <Select
                    selectedOption={
                      editForm.osFamily === ""
                        ? { value: "", label: t("admin.nodes.editSections.osUnknown") }
                        : {
                            value: editForm.osFamily,
                            label: t(`admin.nodes.wizard.os.${editForm.osFamily}`),
                          }
                    }
                    options={[
                      { value: "", label: t("admin.nodes.editSections.osUnknown") },
                      ...["debian", "rhel", "alpine"].map((f) => ({
                        value: f,
                        label: t(`admin.nodes.wizard.os.${f}`),
                      })),
                    ]}
                    onChange={({ detail }) =>
                      setEditForm((f) => ({ ...f, osFamily: detail.selectedOption.value ?? "" }))
                    }
                  />
                </FormField>
              </SpaceBetween>
            </Container>

            <Container
              header={<Header variant="h3">{t("admin.nodes.editSections.labelsTitle")}</Header>}
            >
              <SpaceBetween size="m">
                <Box variant="p" color="text-body-secondary">
                  {t("admin.nodes.editSections.labelsHint")}
                </Box>
                {(["ko", "en", "zh"] as const).map((code) => (
                  <FormField key={code} label={t(`admin.nodes.editSections.labelLang.${code}`)}>
                    <Input
                      value={editForm.labels[code] ?? ""}
                      placeholder={editForm.name}
                      onChange={({ detail }) =>
                        setEditForm((f) => ({
                          ...f,
                          labels: { ...f.labels, [code]: detail.value },
                        }))
                      }
                    />
                  </FormField>
                ))}
              </SpaceBetween>
            </Container>

            <Container header={<Header variant="h3">{t("admin.nodes.editSections.limitsTitle")}</Header>}>
              <SpaceBetween size="m">
                <FormField
                  label={t("admin.nodes.col.multiplier")}
                  description={t("admin.nodes.multiplierHint")}
                >
                  <Input
                    value={editForm.trafficMultiplier}
                    type="number"
                    step={0.1}
                    inputMode="decimal"
                    onChange={({ detail }) =>
                      setEditForm((f) => ({ ...f, trafficMultiplier: detail.value }))
                    }
                  />
                </FormField>
                <FormField
                  label={t("admin.nodes.wizard.maxConns")}
                  description={t("admin.nodes.wizard.maxConnsHint")}
                >
                  <Input
                    value={editForm.maxConns}
                    type="number"
                    inputMode="numeric"
                    onChange={({ detail }) => setEditForm((f) => ({ ...f, maxConns: detail.value }))}
                  />
                </FormField>
              </SpaceBetween>
            </Container>

            <Container header={<Header variant="h3">{t("admin.nodes.editSections.firewallTitle")}</Header>}>
              <SpaceBetween size="m">
                <FormField
                  label={t("admin.nodes.wizard.preset")}
                  description={t("admin.nodes.editSections.firewallHint")}
                >
                  <Select
                    selectedOption={{
                      value: editForm.firewallPreset,
                      label: t(`admin.nodes.wizard.presets.${editForm.firewallPreset}`),
                    }}
                    options={["standard", "web_alt", "high_port", "custom"].map((p) => ({
                      value: p,
                      label: t(`admin.nodes.wizard.presets.${p}`),
                      description: t(`admin.nodes.wizard.presetHint.${p}`),
                    }))}
                    onChange={({ detail }) =>
                      setEditForm((f) => ({
                        ...f,
                        firewallPreset: detail.selectedOption.value ?? "standard",
                      }))
                    }
                  />
                </FormField>
                {editForm.firewallPreset === "custom" && (
                  <FormField
                    label={t("admin.nodes.wizard.customPorts")}
                    description={t("admin.nodes.wizard.customPortsHint")}
                  >
                    <Input
                      value={editForm.customPorts}
                      placeholder="8080, 9000-9100"
                      onChange={({ detail }) =>
                        setEditForm((f) => ({ ...f, customPorts: detail.value }))
                      }
                    />
                  </FormField>
                )}
                <FormField
                  label={t("admin.nodes.wizard.resolvedPorts")}
                  description={t("admin.nodes.editSections.resolvedPortsHint")}
                >
                  <Box variant="code">
                    {formatPorts(
                      resolvePorts(
                        editForm.firewallPreset as NodeFirewallPreset,
                        editModal?.port ?? 443,
                        parsePortInput(editForm.customPorts).specs,
                      ),
                    )}
                  </Box>
                </FormField>
              </SpaceBetween>
            </Container>
          </SpaceBetween>
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
