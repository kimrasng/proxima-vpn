import { useState, useMemo, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
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
  Textarea,
  TextFilter,
} from "@cloudscape-design/components";
import { useCollection } from "@cloudscape-design/collection-hooks";
import { listNodes, generateNodeToken, deleteNode, updateNode } from "../../api/admin";
import type { Node, GenerateTokenResponse, UpdateNodeRequest } from "../../api/types";
import { formatAbsoluteTime, formatRelativeTime } from "../../utils/relativeTime";
import { useManualRefresh } from "../../hooks/useManualRefresh";

const REFRESH_INTERVAL = 30000;
const DEFAULT_PAGE_SIZE = 20;

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

/** Compact inline usage bar: label on the left, thin bar with its percentage on the right. */
function UsageCell({ label, value }: { label: string; value: number | undefined }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
      <Box variant="small" color="text-body-secondary">
        <span style={{ display: "inline-block", minWidth: "44px" }}>{label}</span>
      </Box>
      {value != null ? (
        <div style={{ flex: 1, minWidth: "72px" }}>
          <ProgressBar
            value={value}
            status={getUsageStatus(value) === "error" ? "error" : "in-progress"}
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
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tokenModal, setTokenModal] = useState(false);
  const [tokenData, setTokenData] = useState<GenerateTokenResponse | null>(null);
  const [deleteModal, setDeleteModal] = useState<Node | null>(null);
  const [editModal, setEditModal] = useState<Node | null>(null);
  const [editForm, setEditForm] = useState({
    name: "",
    country: "",
    region: "",
    trafficMultiplier: "1",
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
      { id: "country", visible: true },
      { id: "ip", visible: true },
      { id: "status", visible: true },
      { id: "resources", visible: true },
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
      const data = await listNodes();
      setNodes(data);
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

  const regionOptions = useMemo<SelectProps.Options>(() => {
    const regions = Array.from(new Set(nodes.map((n) => n.region).filter(Boolean))).sort();
    return [
      { value: "all", label: t("admin.nodes.filterRegionAll") },
      ...regions.map((region) => ({ value: region, label: region })),
    ];
  }, [nodes, t]);

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
    useCollection(nodes, {
      filtering: {
        empty: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            <Box variant="strong" color="inherit">
              {t("admin.nodes.empty")}
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
    Boolean(filterProps.filteringText) || statusFilter !== "all" || regionFilter !== "all";

  const clearFilters = () => {
    actions.setFiltering("");
    setStatusFilter("all");
    setRegionFilter("all");
  };

  const handleGenerateToken = async () => {
    setActionLoading(true);
    try {
      const data = await generateNodeToken();
      setTokenData(data);
      setTokenModal(true);
      await fetchNodes();
    } catch {
      setError(t("admin.nodes.tokenError"));
    } finally {
      setActionLoading(false);
    }
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
      const req: UpdateNodeRequest = {
        name: editForm.name,
        country: editForm.country,
        region: editForm.region,
        traffic_multiplier: multiplier,
      };
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
                loading={actionLoading}
                onClick={() => void handleGenerateToken()}
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
                  { id: "country", label: t("admin.nodes.col.countryRegion") },
                  { id: "ip", label: t("admin.nodes.col.ip") },
                  { id: "status", label: t("admin.nodes.col.status") },
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
              id: "name",
              header: t("admin.nodes.col.name"),
              sortingField: "name",
              minWidth: 120,
              cell: (item) =>
                item.status === "pending" ? (
                  <Badge color="grey">{t("admin.nodes.statusPending")}</Badge>
                ) : (
                  <Button variant="inline-link" onClick={() => navigate(`/admin/nodes/${item.id}`)}>
                    {item.name}
                  </Button>
                ),
            },
            {
              id: "country",
              header: t("admin.nodes.col.countryRegion"),
              sortingField: "country",
              maxWidth: 150,
              cell: (item) =>
                item.status === "pending" ? (
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
                <StatusIndicator type={getStatusIndicatorType(item.status)}>
                  {statusLabel(item.status)}
                </StatusIndicator>
              ),
            },
            {
              id: "resources",
              header: (
                <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
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
                </SpaceBetween>
              ),
              minWidth: 165,
              cell: (item) => (
                <SpaceBetween size="xxxs">
                  <UsageCell label={t("admin.nodes.col.cpu")} value={item.cpu_usage} />
                  <UsageCell label={t("admin.nodes.col.memory")} value={item.memory_usage} />
                </SpaceBetween>
              ),
            },
            {
              id: "traffic",
              header: t("admin.nodes.col.traffic"),
              maxWidth: 110,
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
              maxWidth: 110,
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
              maxWidth: 120,
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
              maxWidth: 105,
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
                    {
                      id: "inbounds",
                      text: t("admin.nodes.panel.inbounds"),
                      disabled: item.status === "pending",
                    },
                    { id: "delete", text: t("admin.nodes.delete") },
                  ]}
                  onItemClick={({ detail }) => {
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
                filteredItemsCount !== undefined && filteredItemsCount !== nodes.length
                  ? `(${filteredItemsCount}/${nodes.length})`
                  : `(${nodes.length})`
              }
            >
              {t("admin.nodes.listTitle")}
            </Header>
          }
        />

        <Modal
          visible={tokenModal}
          onDismiss={() => setTokenModal(false)}
          header={t("admin.nodes.tokenModalTitle")}
          footer={
            <Box float="right">
              <Button variant="primary" onClick={() => setTokenModal(false)}>
                {t("admin.nodes.close")}
              </Button>
            </Box>
          }
        >
          <SpaceBetween size="m">
            <Box variant="awsui-key-label">{t("admin.nodes.token")}</Box>
            <Textarea value={tokenData?.token ?? ""} readOnly rows={2} />
            <Box variant="awsui-key-label">{t("admin.nodes.installCommand")}</Box>
            <Textarea value={tokenData?.install_command ?? ""} readOnly rows={3} />
          </SpaceBetween>
        </Modal>

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
          <SpaceBetween size="m">
            <FormField label={t("admin.nodes.ipReadOnly")}>
              <Box>{editModal?.ip}</Box>
            </FormField>
            <FormField label={t("admin.nodes.portReadOnly")}>
              <Box>{editModal?.port}</Box>
            </FormField>
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
          </SpaceBetween>
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
