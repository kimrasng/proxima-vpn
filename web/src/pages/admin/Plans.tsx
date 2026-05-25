import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  Checkbox,
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
import { listPlans, createPlan, updatePlan, deletePlan, listNodeGroups } from "../../api/admin";
import type { Plan, CreatePlanRequest, UpdatePlanRequest, NodeGroup } from "../../api/types";

function formatTraffic(bytes?: number): string {
  if (bytes == null || bytes === 0) return "Unlimited";
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

function formatSpeed(bps?: number): string {
  if (bps == null || bps === 0) return "Unlimited";
  return `${bps} Mbps`;
}

interface PlanForm {
  name: string;
  traffic_limit: string;
  duration_days: string;
  max_devices: string;
  speed_limit: string;
  node_group_id: string;
  is_active: boolean;
}

const emptyForm: PlanForm = {
  name: "",
  traffic_limit: "",
  duration_days: "30",
  max_devices: "3",
  speed_limit: "",
  node_group_id: "",
  is_active: true,
};

export default function Plans() {
  const { t } = useTranslation();
  const [plans, setPlans] = useState<Plan[]>([]);
  const [nodeGroups, setNodeGroups] = useState<NodeGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [editModal, setEditModal] = useState<Plan | null>(null);
  const [deleteModal, setDeleteModal] = useState<Plan | null>(null);
  const [form, setForm] = useState<PlanForm>(emptyForm);
  const [actionLoading, setActionLoading] = useState(false);

  const fetchData = async () => {
    try {
      const [plansData, groupsData] = await Promise.all([listPlans(), listNodeGroups()]);
      setPlans(plansData);
      setNodeGroups(groupsData);
      setError(null);
    } catch {
      setError(t("admin.plans.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchData();
  }, []);

  const handleCreate = async () => {
    setActionLoading(true);
    try {
      const req: CreatePlanRequest = {
        name: form.name,
        traffic_limit: form.traffic_limit ? Number(form.traffic_limit) * 1024 * 1024 * 1024 : undefined,
        duration_days: Number(form.duration_days),
        max_devices: Number(form.max_devices),
        speed_limit: form.speed_limit ? Number(form.speed_limit) : undefined,
        node_group_id: form.node_group_id,
        is_active: form.is_active,
      };
      await createPlan(req);
      setCreateModal(false);
      setForm(emptyForm);
      await fetchData();
    } catch {
      setError(t("admin.plans.createError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEdit = async () => {
    if (!editModal) return;
    setActionLoading(true);
    try {
      const req: UpdatePlanRequest = {
        name: form.name,
        traffic_limit: form.traffic_limit ? Number(form.traffic_limit) * 1024 * 1024 * 1024 : undefined,
        duration_days: Number(form.duration_days),
        max_devices: Number(form.max_devices),
        speed_limit: form.speed_limit ? Number(form.speed_limit) : undefined,
        node_group_id: form.node_group_id,
        is_active: form.is_active,
      };
      await updatePlan(editModal.id, req);
      setEditModal(null);
      setForm(emptyForm);
      await fetchData();
    } catch {
      setError(t("admin.plans.updateError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deletePlan(deleteModal.id);
      setDeleteModal(null);
      await fetchData();
    } catch {
      setError(t("admin.plans.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  const openEdit = (plan: Plan) => {
    setForm({
      name: plan.name,
      traffic_limit: plan.traffic_limit ? String(plan.traffic_limit / (1024 * 1024 * 1024)) : "",
      duration_days: String(plan.duration_days),
      max_devices: String(plan.max_devices),
      speed_limit: plan.speed_limit ? String(plan.speed_limit) : "",
      node_group_id: plan.node_group_id,
      is_active: plan.is_active,
    });
    setEditModal(plan);
  };

  const groupOptions = nodeGroups.map((g) => ({ label: g.name, value: g.id }));

  const renderForm = () => (
    <SpaceBetween size="m">
      <FormField label={t("admin.plans.form.name")}>
        <Input value={form.name} onChange={({ detail }) => setForm({ ...form, name: detail.value })} />
      </FormField>
      <FormField label={t("admin.plans.form.trafficLimit")} description={t("admin.plans.form.trafficHint")}>
        <Input value={form.traffic_limit} type="number" onChange={({ detail }) => setForm({ ...form, traffic_limit: detail.value })} />
      </FormField>
      <FormField label={t("admin.plans.form.duration")}>
        <Input value={form.duration_days} type="number" onChange={({ detail }) => setForm({ ...form, duration_days: detail.value })} />
      </FormField>
      <FormField label={t("admin.plans.form.maxDevices")}>
        <Input value={form.max_devices} type="number" onChange={({ detail }) => setForm({ ...form, max_devices: detail.value })} />
      </FormField>
      <FormField label={t("admin.plans.form.speedLimit")} description={t("admin.plans.form.speedHint")}>
        <Input value={form.speed_limit} type="number" onChange={({ detail }) => setForm({ ...form, speed_limit: detail.value })} />
      </FormField>
      <FormField label={t("admin.plans.form.nodeGroup")}>
        <Select
          selectedOption={groupOptions.find((o) => o.value === form.node_group_id) ?? null}
          options={groupOptions}
          onChange={({ detail }) => setForm({ ...form, node_group_id: detail.selectedOption.value ?? "" })}
        />
      </FormField>
      <Checkbox checked={form.is_active} onChange={({ detail }) => setForm({ ...form, is_active: detail.checked })}>
        {t("admin.plans.form.active")}
      </Checkbox>
    </SpaceBetween>
  );

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.plans.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.plans.title")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <Table
          header={
            <Header
              actions={
                <Button variant="primary" onClick={() => { setForm(emptyForm); setCreateModal(true); }}>
                  {t("admin.plans.create")}
                </Button>
              }
              counter={`(${plans.length})`}
            >
              {t("admin.plans.title")}
            </Header>
          }
          items={plans}
          columnDefinitions={[
            { id: "name", header: t("admin.plans.col.name"), cell: (item) => item.name },
            { id: "traffic", header: t("admin.plans.col.trafficLimit"), cell: (item) => formatTraffic(item.traffic_limit) },
            { id: "duration", header: t("admin.plans.col.duration"), cell: (item) => `${item.duration_days}d` },
            { id: "devices", header: t("admin.plans.col.maxDevices"), cell: (item) => item.max_devices },
            { id: "speed", header: t("admin.plans.col.speedLimit"), cell: (item) => formatSpeed(item.speed_limit) },
            { id: "nodeGroup", header: t("admin.plans.col.nodeGroup"), cell: (item) => item.node_group_name ?? "-" },
            {
              id: "active",
              header: t("admin.plans.col.active"),
              cell: (item) => item.is_active ? t("admin.plans.yes") : t("admin.plans.no"),
            },
            {
              id: "actions",
              header: t("admin.plans.col.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button variant="inline-link" onClick={() => openEdit(item)}>{t("admin.plans.edit")}</Button>
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>{t("admin.plans.delete")}</Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.plans.empty")}</Box>}
        />

        <Modal
          visible={createModal}
          onDismiss={() => setCreateModal(false)}
          header={t("admin.plans.createTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setCreateModal(false)}>{t("admin.plans.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleCreate()}>
                  {t("admin.plans.save")}
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
          header={t("admin.plans.editTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setEditModal(null)}>{t("admin.plans.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleEdit()}>
                  {t("admin.plans.save")}
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
          header={t("admin.plans.deleteTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.plans.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.plans.confirmDelete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.plans.deleteMessage", { name: deleteModal?.name })}
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
