import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ContentLayout,
  Flashbar,
  FormField,
  Header,
  Input,
  Modal,
  Pagination,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  TextFilter,
  Toggle,
} from "@cloudscape-design/components";
import { listUsers, updateUser, createUser, resetUserTraffic, getUser, listPlans } from "../../api/admin";
import type { User, CreateUserRequest, Plan } from "../../api/types";

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

interface EditUserForm {
  name: string;
  status: string;
  plan_id: string;
  plan_expires_at: string;
  is_active: boolean;
}

const emptyEditForm: EditUserForm = {
  name: "",
  status: "active",
  plan_id: "",
  plan_expires_at: "",
  is_active: true,
};

export default function Users() {
  const { t } = useTranslation();
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

  const [editModal, setEditModal] = useState<User | null>(null);
  const [editForm, setEditForm] = useState<EditUserForm>(emptyEditForm);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [editLoading, setEditLoading] = useState(false);

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
      setFlash([{
        type: "success",
        content: t("admin.users.createSuccess"),
      }]);
    } catch {
      setFlash([{
        type: "error",
        content: t("admin.users.createError"),
      }]);
    } finally {
      setCreateLoading(false);
    }
  };

  const handleResetTraffic = async (userId: string) => {
    setActionLoading(userId + "-reset");
    try {
      await resetUserTraffic(userId);
      await fetchUsers();
      setFlash([{
        type: "success",
        content: t("admin.users.resetTrafficSuccess"),
      }]);
    } catch {
      setFlash([{
        type: "error",
        content: t("admin.users.resetTrafficError"),
      }]);
    } finally {
      setActionLoading(null);
    }
  };

  const handleOpenEdit = async (user: User) => {
    setEditLoading(true);
    try {
      const [detail, plansData] = await Promise.all([getUser(user.id), listPlans()]);
      setPlans(plansData);
      setEditForm({
        name: detail.name,
        status: detail.status,
        plan_id: detail.plan_id ?? "",
        plan_expires_at: detail.plan_expires_at ? (detail.plan_expires_at.split("T")[0] ?? "") : "",
        is_active: detail.is_active,
      });
      setEditModal(user);
    } catch {
      setFlash([{ type: "error", content: t("admin.users.fetchError") }]);
    } finally {
      setEditLoading(false);
    }
  };

  const handleEditUser = async () => {
    if (!editModal) return;
    setEditLoading(true);
    try {
      await updateUser(editModal.id, {
        name: editForm.name,
        status: editForm.status,
        plan_id: editForm.plan_id || undefined,
        plan_expires_at: editForm.plan_expires_at
          ? new Date(editForm.plan_expires_at).toISOString()
          : undefined,
        is_active: editForm.is_active,
      });
      setEditModal(null);
      setEditForm(emptyEditForm);
      await fetchUsers();
      setFlash([{ type: "success", content: t("admin.users.editUserSuccess") }]);
    } catch {
      setFlash([{ type: "error", content: t("admin.users.editUserError") }]);
    } finally {
      setEditLoading(false);
    }
  };

  const statusOptions = [
    { label: t("admin.users.filter.all"), value: "" },
    { label: t("admin.users.filter.active"), value: "active" },
    { label: t("admin.users.filter.suspended"), value: "suspended" },
    { label: t("admin.users.filter.expired"), value: "expired" },
  ];

  const editStatusOptions = [
    { label: "Active", value: "active" },
    { label: "Suspended", value: "suspended" },
    { label: "Expired", value: "expired" },
    { label: "Pending", value: "pending" },
  ];

  const planOptions = [
    { label: "No Plan", value: "" },
    ...plans.map((p) => ({ label: p.name, value: p.id })),
  ];

  const formatBytes = (bytes: number): string => {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
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
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
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
            { id: "email", header: t("admin.users.col.email"), cell: (item) => item.email },
            { id: "name", header: t("admin.users.col.name"), cell: (item) => item.name },
            {
              id: "status",
              header: t("admin.users.col.status"),
              cell: (item) => (
                <StatusIndicator type={item.is_active ? "success" : "error"}>
                  {item.status}
                </StatusIndicator>
              ),
            },
            { id: "plan", header: t("admin.users.col.plan"), cell: (item) => item.plan_name ?? "-" },
            { id: "traffic", header: t("admin.users.col.trafficUsed"), cell: (item) => formatBytes(item.traffic_used) },
            {
              id: "expires",
              header: t("admin.users.col.expiresAt"),
              cell: (item) => item.plan_expires_at ? new Date(item.plan_expires_at).toLocaleDateString() : "-",
            },
            {
              id: "actions",
              header: t("admin.users.col.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button
                    variant="inline-link"
                    loading={actionLoading === item.id + "-edit"}
                    onClick={() => void handleOpenEdit(item)}
                  >
                    {t("admin.users.editUser")}
                  </Button>
                  <Button
                    variant="inline-link"
                    loading={actionLoading === item.id}
                    onClick={() => void handleToggleActive(item)}
                  >
                    {item.is_active ? t("admin.users.suspend") : t("admin.users.activate")}
                  </Button>
                  <Button
                    variant="inline-link"
                    loading={actionLoading === item.id + "-reset"}
                    onClick={() => void handleResetTraffic(item.id)}
                  >
                    {t("admin.users.resetTraffic")}
                  </Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={
            <Box textAlign="center">
              {loading ? <Spinner /> : t("admin.users.empty")}
            </Box>
          }
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
              <Button
                variant="primary"
                loading={createLoading}
                onClick={() => void handleCreateUser()}
              >
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

      <Modal
        visible={editModal !== null}
        onDismiss={() => {
          setEditModal(null);
          setEditForm(emptyEditForm);
        }}
        header={t("admin.users.editUserModalTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                variant="link"
                onClick={() => {
                  setEditModal(null);
                  setEditForm(emptyEditForm);
                }}
              >
                {t("common.cancel")}
              </Button>
              <Button
                variant="primary"
                loading={editLoading}
                onClick={() => void handleEditUser()}
              >
                {t("common.save")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        <SpaceBetween size="m">
          <FormField label={t("admin.users.col.name")}>
            <Input
              value={editForm.name}
              onChange={({ detail }) => setEditForm((f) => ({ ...f, name: detail.value }))}
            />
          </FormField>
          <FormField label={t("admin.users.col.status")}>
            <Select
              selectedOption={editStatusOptions.find((o) => o.value === editForm.status) ?? null}
              options={editStatusOptions}
              onChange={({ detail }) => setEditForm((f) => ({ ...f, status: detail.selectedOption.value ?? "active" }))}
            />
          </FormField>
          <FormField label={t("admin.users.planLabel")}>
            <Select
              selectedOption={planOptions.find((o) => o.value === editForm.plan_id) ?? null}
              options={planOptions}
              onChange={({ detail }) => setEditForm((f) => ({ ...f, plan_id: detail.selectedOption.value ?? "" }))}
            />
          </FormField>
          <FormField label={t("admin.users.expiresAt")}>
            <input
              type="date"
              value={editForm.plan_expires_at}
              onChange={(e) => setEditForm((f) => ({ ...f, plan_expires_at: e.target.value }))}
              style={{ width: "100%" }}
            />
          </FormField>
          <FormField label={t("admin.users.col.status")}>
            <Toggle
              checked={editForm.is_active}
              onChange={({ detail }) => setEditForm((f) => ({ ...f, is_active: detail.checked }))}
            >
              {editForm.is_active ? t("admin.users.activate") : t("admin.users.suspend")}
            </Toggle>
          </FormField>
        </SpaceBetween>
      </Modal>
    </ContentLayout>
  );
}
