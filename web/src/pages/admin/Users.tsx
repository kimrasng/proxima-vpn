import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ButtonDropdown,
  ContentLayout,
  Flashbar,
  FormField,
  Header,
  Input,
  Link,
  Modal,
  Pagination,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  type StatusIndicatorProps,
  Table,
  TextFilter,
} from "@cloudscape-design/components";
import { listUsers, updateUser, createUser, resetUserTraffic } from "../../api/admin";
import type { User, CreateUserRequest } from "../../api/types";
import { formatBytes } from "../../utils/format";
import { formatDate } from "../../utils/relativeTime";

interface CreateUserForm {
  email: string;
  password: string;
  name: string;
}

const emptyCreateForm: CreateUserForm = {
  email: "",
  password: "",
  name: "",
};

const knownStatuses = ["active", "suspended", "expired", "pending"] as const;

type KnownStatus = (typeof knownStatuses)[number];

function isKnownStatus(value: string): value is KnownStatus {
  return (knownStatuses as readonly string[]).includes(value);
}

// A disabled account denies service whatever its status column says, so
// is_active decides both the label and the severity. Reporting such a row as
// "active" because status still reads active is what made a suspended account
// look enabled. Otherwise the status carries the severity: pending is a neutral
// waiting state, not a failure.
function statusIndicatorType(user: User): StatusIndicatorProps.Type {
  if (!user.is_active) return "error";
  switch (user.status) {
    case "active":
      return "success";
    case "pending":
      return "pending";
    case "expired":
      return "warning";
    case "suspended":
      return "error";
    default:
      return "info";
  }
}

export default function Users() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<{ type: "success" | "error"; content: string }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [createForm, setCreateForm] = useState<CreateUserForm>(emptyCreateForm);
  const [createLoading, setCreateLoading] = useState(false);

  const limit = 20;

  const fetchUsers = async () => {
    setLoading(true);
    try {
      const data = await listUsers({
        page,
        limit,
        search: search || undefined,
        status: statusFilter || undefined,
      });
      setUsers(data.users);
      setTotal(data.total);
      setError(null);
    } catch {
      setError(t("admin.users.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchUsers();
    const interval = setInterval(() => void fetchUsers(), 60000);
    return () => clearInterval(interval);
  }, [page, statusFilter]);

  const handleSearch = () => {
    setPage(1);
    void fetchUsers();
  };

  const handleToggleActive = async (user: User) => {
    setActionLoading(user.id);
    try {
      await updateUser(user.id, { is_active: !user.is_active });
      await fetchUsers();
    } catch {
      setError(t("admin.users.updateError"));
    } finally {
      setActionLoading(null);
    }
  };

  const handleCreateUser = async () => {
    setCreateLoading(true);
    try {
      const req: CreateUserRequest = {
        email: createForm.email,
        password: createForm.password,
        name: createForm.name,
      };
      await createUser(req);
      setCreateModal(false);
      setCreateForm(emptyCreateForm);
      await fetchUsers();
      setFlash([{ type: "success", content: t("admin.users.createSuccess") }]);
    } catch {
      setFlash([{ type: "error", content: t("admin.users.createError") }]);
    } finally {
      setCreateLoading(false);
    }
  };

  const handleResetTraffic = async (userId: string) => {
    setActionLoading(userId);
    try {
      await resetUserTraffic(userId);
      await fetchUsers();
      setFlash([{ type: "success", content: t("admin.users.resetTrafficSuccess") }]);
    } catch {
      setFlash([{ type: "error", content: t("admin.users.resetTrafficError") }]);
    } finally {
      setActionLoading(null);
    }
  };

  const statusOptions = [
    { label: t("admin.users.filter.all"), value: "" },
    { label: t("admin.users.filter.active"), value: "active" },
    { label: t("admin.users.filter.suspended"), value: "suspended" },
    { label: t("admin.users.filter.expired"), value: "expired" },
  ];

  const statusLabel = (user: User): string => {
    if (!user.is_active && user.status === "active") return t("admin.users.status.disabled");
    return isKnownStatus(user.status) ? t(`admin.users.status.${user.status}`) : user.status;
  };

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.users.title")}</Header>}>
      <SpaceBetween size="l">
        {flash.length > 0 && (
          <Flashbar
            items={flash.map((f) => ({
              type: f.type,
              content: f.content,
              dismissible: true,
              onDismiss: () => setFlash([]),
            }))}
          />
        )}
        {error && (
          <Flashbar
            items={[
              { type: "error", content: error, dismissible: true, onDismiss: () => setError(null) },
            ]}
          />
        )}

        <Table
          loading={loading}
          loadingText={t("admin.users.loading")}
          header={
            <Header
              counter={`(${total})`}
              actions={
                <Button variant="primary" onClick={() => setCreateModal(true)}>
                  {t("admin.users.createUser")}
                </Button>
              }
            >
              {t("admin.users.title")}
            </Header>
          }
          filter={
            <SpaceBetween direction="horizontal" size="m">
              <TextFilter
                filteringText={search}
                onChange={({ detail }) => setSearch(detail.filteringText)}
                onDelayedChange={handleSearch}
                filteringPlaceholder={t("admin.users.searchPlaceholder")}
              />
              <Select
                selectedOption={statusOptions.find((o) => o.value === (statusFilter ?? "")) ?? null}
                options={statusOptions}
                onChange={({ detail }) => {
                  setStatusFilter(detail.selectedOption.value || null);
                  setPage(1);
                }}
              />
            </SpaceBetween>
          }
          pagination={
            <Pagination
              currentPageIndex={page}
              pagesCount={Math.ceil(total / limit) || 1}
              onChange={({ detail }) => setPage(detail.currentPageIndex)}
            />
          }
          items={users}
          columnDefinitions={[
            {
              id: "email",
              header: t("admin.users.col.email"),
              cell: (item) => (
                <Link
                  href={`/admin/users/${item.id}`}
                  onFollow={(event) => {
                    event.preventDefault();
                    void navigate(`/admin/users/${item.id}`);
                  }}
                >
                  {item.email}
                </Link>
              ),
            },
            { id: "name", header: t("admin.users.col.name"), cell: (item) => item.name || "—" },
            {
              id: "status",
              header: t("admin.users.col.status"),
              cell: (item) => (
                <StatusIndicator type={statusIndicatorType(item)}>
                  {statusLabel(item)}
                </StatusIndicator>
              ),
            },
            {
              id: "plan",
              header: t("admin.users.col.plan"),
              cell: (item) => item.plan_name ?? t("admin.users.noPlan"),
            },
            {
              id: "traffic",
              header: t("admin.users.col.trafficUsed"),
              cell: (item) => formatBytes(item.traffic_used),
            },
            {
              id: "expires",
              header: t("admin.users.col.expiresAt"),
              cell: (item) =>
                item.plan_expires_at ? formatDate(item.plan_expires_at) : "—",
            },
            {
              id: "actions",
              header: t("admin.users.col.actions"),
              cell: (item) => (
                <ButtonDropdown
                  expandToViewport
                  ariaLabel={t("admin.users.actionsLabel")}
                  loading={actionLoading === item.id}
                  items={[
                    { id: "detail", text: t("admin.users.viewDetail") },
                    {
                      id: "toggle",
                      text: item.is_active ? t("admin.users.suspend") : t("admin.users.activate"),
                    },
                    { id: "reset", text: t("admin.users.resetTraffic") },
                  ]}
                  onItemClick={({ detail }) => {
                    if (detail.id === "detail") void navigate(`/admin/users/${item.id}`);
                    if (detail.id === "toggle") void handleToggleActive(item);
                    if (detail.id === "reset") void handleResetTraffic(item.id);
                  }}
                >
                  {t("admin.users.actionsLabel")}
                </ButtonDropdown>
              ),
            },
          ]}
          empty={<Box textAlign="center">{loading ? <Spinner /> : t("admin.users.empty")}</Box>}
        />
      </SpaceBetween>

      <Modal
        visible={createModal}
        onDismiss={() => {
          setCreateModal(false);
          setCreateForm(emptyCreateForm);
        }}
        header={t("admin.users.createUserModalTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                variant="link"
                onClick={() => {
                  setCreateModal(false);
                  setCreateForm(emptyCreateForm);
                }}
              >
                {t("common.cancel")}
              </Button>
              <Button variant="primary" loading={createLoading} onClick={() => void handleCreateUser()}>
                {t("common.create")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        <SpaceBetween size="m">
          <FormField label={t("admin.users.email")}>
            <Input
              value={createForm.email}
              onChange={({ detail }) => setCreateForm((f) => ({ ...f, email: detail.value }))}
              type="email"
            />
          </FormField>
          <FormField label={t("admin.users.password")}>
            <Input
              value={createForm.password}
              onChange={({ detail }) => setCreateForm((f) => ({ ...f, password: detail.value }))}
              type="password"
            />
          </FormField>
          <FormField label={t("admin.users.col.name")}>
            <Input
              value={createForm.name}
              onChange={({ detail }) => setCreateForm((f) => ({ ...f, name: detail.value }))}
            />
          </FormField>
        </SpaceBetween>
      </Modal>
    </ContentLayout>
  );
}
