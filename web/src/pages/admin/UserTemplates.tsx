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
  Select,
  SpaceBetween,
  Spinner,
  Table,
} from "@cloudscape-design/components";
import {
  listUserTemplates,
  createUserTemplate,
  updateUserTemplate,
  deleteUserTemplate,
  listNodeGroups,
} from "../../api/admin";
import type { UserTemplate, CreateUserTemplateRequest, NodeGroup } from "../../api/types";

interface FormState {
  name: string;
  traffic_limit: string;
  duration_days: string;
  max_devices: string;
  speed_limit: string;
  node_group_id: string;
}

const emptyForm: FormState = {
  name: "",
  traffic_limit: "",
  duration_days: "",
  max_devices: "1",
  speed_limit: "",
  node_group_id: "",
};

export default function UserTemplates() {
  const { t } = useTranslation();
  const [templates, setTemplates] = useState<UserTemplate[]>([]);
  const [nodeGroups, setNodeGroups] = useState<NodeGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [editModal, setEditModal] = useState<UserTemplate | null>(null);
  const [deleteModal, setDeleteModal] = useState<UserTemplate | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [actionLoading, setActionLoading] = useState(false);

  const fetchTemplates = async () => {
    try {
      const data = await listUserTemplates();
      setTemplates(data);
      setError(null);
    } catch {
      setError(t("admin.userTemplates.fetchError", "Failed to load templates"));
    } finally {
      setLoading(false);
    }
  };

  const fetchNodeGroups = async () => {
    try {
      const data = await listNodeGroups();
      setNodeGroups(data);
    } catch {
      /* empty */
    }
  };

  useEffect(() => {
    void fetchTemplates();
    void fetchNodeGroups();
  }, []);

  const buildRequest = (): CreateUserTemplateRequest => ({
    name: form.name,
    traffic_limit: form.traffic_limit ? Number(form.traffic_limit) * 1073741824 : undefined,
    duration_days: Number(form.duration_days),
    max_devices: Number(form.max_devices),
    speed_limit: form.speed_limit ? Number(form.speed_limit) * 1000000 : undefined,
    node_group_id: form.node_group_id || undefined,
  });

  const handleCreate = async () => {
    setActionLoading(true);
    try {
      await createUserTemplate(buildRequest());
      setCreateModal(false);
      setForm(emptyForm);
      await fetchTemplates();
    } catch {
      setError(t("admin.userTemplates.createError", "Failed to create template"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEdit = async () => {
    if (!editModal) return;
    setActionLoading(true);
    try {
      await updateUserTemplate(editModal.id, buildRequest());
      setEditModal(null);
      setForm(emptyForm);
      await fetchTemplates();
    } catch {
      setError(t("admin.userTemplates.updateError", "Failed to update template"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deleteUserTemplate(deleteModal.id);
      setDeleteModal(null);
      await fetchTemplates();
    } catch {
      setError(t("admin.userTemplates.deleteError", "Failed to delete template"));
    } finally {
      setActionLoading(false);
    }
  };

  const openEditModal = (template: UserTemplate) => {
    setForm({
      name: template.name,
      traffic_limit: template.traffic_limit ? String(template.traffic_limit / 1073741824) : "",
      duration_days: String(template.duration_days),
      max_devices: String(template.max_devices),
      speed_limit: template.speed_limit ? String(template.speed_limit / 1000000) : "",
      node_group_id: template.node_group_id ?? "",
    });
    setEditModal(template);
  };

  const formatTraffic = (bytes?: number) => {
    if (!bytes) return "-";
    return `${(bytes / 1073741824).toFixed(1)} GB`;
  };

  const formatSpeed = (bps?: number) => {
    if (!bps) return "-";
    return `${(bps / 1000000).toFixed(0)} Mbps`;
  };

  const nodeGroupOptions = [
    { label: t("admin.userTemplates.noNodeGroup", "None"), value: "" },
    ...nodeGroups.map((g) => ({ label: g.name, value: g.id })),
  ];

  const renderForm = () => (
    <SpaceBetween size="m">
      <FormField label={t("admin.userTemplates.nameLabel", "Name")}>
        <Input value={form.name} onChange={({ detail }) => setForm({ ...form, name: detail.value })} />
      </FormField>
      <FormField label={t("admin.userTemplates.trafficLimitLabel", "Traffic Limit (GB)")} description={t("admin.userTemplates.trafficLimitDesc", "Leave empty for unlimited")}>
        <Input value={form.traffic_limit} type="number" onChange={({ detail }) => setForm({ ...form, traffic_limit: detail.value })} />
      </FormField>
      <FormField label={t("admin.userTemplates.durationLabel", "Duration (days)")}>
        <Input value={form.duration_days} type="number" onChange={({ detail }) => setForm({ ...form, duration_days: detail.value })} />
      </FormField>
      <FormField label={t("admin.userTemplates.maxDevicesLabel", "Max Devices")}>
        <Input value={form.max_devices} type="number" onChange={({ detail }) => setForm({ ...form, max_devices: detail.value })} />
      </FormField>
      <FormField label={t("admin.userTemplates.speedLimitLabel", "Speed Limit (Mbps)")} description={t("admin.userTemplates.speedLimitDesc", "Leave empty for unlimited")}>
        <Input value={form.speed_limit} type="number" onChange={({ detail }) => setForm({ ...form, speed_limit: detail.value })} />
      </FormField>
      <FormField label={t("admin.userTemplates.nodeGroupLabel", "Node Group")}>
        <Select
          selectedOption={nodeGroupOptions.find((o) => o.value === form.node_group_id) ?? null}
          options={nodeGroupOptions}
          onChange={({ detail }) => setForm({ ...form, node_group_id: detail.selectedOption.value ?? "" })}
        />
      </FormField>
    </SpaceBetween>
  );

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.userTemplates.title", "User Templates")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.userTemplates.title", "User Templates")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <Table
          header={
            <Header
              actions={
                <Button variant="primary" onClick={() => { setForm(emptyForm); setCreateModal(true); }}>
                  {t("admin.userTemplates.create", "Create Template")}
                </Button>
              }
              counter={`(${templates.length})`}
            >
              {t("admin.userTemplates.title", "User Templates")}
            </Header>
          }
          items={templates}
          columnDefinitions={[
            { id: "name", header: t("admin.userTemplates.col.name", "Name"), cell: (item) => item.name },
            { id: "trafficLimit", header: t("admin.userTemplates.col.trafficLimit", "Traffic Limit"), cell: (item) => formatTraffic(item.traffic_limit) },
            { id: "duration", header: t("admin.userTemplates.col.duration", "Duration"), cell: (item) => `${item.duration_days} days` },
            { id: "maxDevices", header: t("admin.userTemplates.col.maxDevices", "Max Devices"), cell: (item) => item.max_devices },
            { id: "speedLimit", header: t("admin.userTemplates.col.speedLimit", "Speed Limit"), cell: (item) => formatSpeed(item.speed_limit) },
            { id: "nodeGroup", header: t("admin.userTemplates.col.nodeGroup", "Node Group"), cell: (item) => item.node_group_name ?? "-" },
            {
              id: "actions",
              header: t("admin.userTemplates.col.actions", "Actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button variant="inline-link" onClick={() => openEditModal(item)}>
                    {t("admin.userTemplates.edit", "Edit")}
                  </Button>
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>
                    {t("admin.userTemplates.delete", "Delete")}
                  </Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.userTemplates.empty", "No templates")}</Box>}
        />

        <Modal
          visible={createModal}
          onDismiss={() => setCreateModal(false)}
          header={t("admin.userTemplates.createTitle", "Create Template")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setCreateModal(false)}>{t("admin.userTemplates.cancel", "Cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleCreate()}>
                  {t("admin.userTemplates.save", "Save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {renderForm()}
        </Modal>

        <Modal
          visible={editModal !== null}
          onDismiss={() => setEditModal(null)}
          header={t("admin.userTemplates.editTitle", "Edit Template")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setEditModal(null)}>{t("admin.userTemplates.cancel", "Cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleEdit()}>
                  {t("admin.userTemplates.save", "Save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {renderForm()}
        </Modal>

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header={t("admin.userTemplates.deleteTitle", "Delete Template")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.userTemplates.cancel", "Cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.userTemplates.confirmDelete", "Delete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.userTemplates.deleteConfirm", "Are you sure you want to delete template \"{{name}}\"?", { name: deleteModal?.name })}
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
