import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ButtonDropdown,
  Container,
  Grid,
  Alert,
  ContentLayout,
  Flashbar,
  Header,
  Modal,
  SpaceBetween,
  Spinner,
} from "@cloudscape-design/components";
import {
  listPlans,
  getPlan,
  createPlan,
  updatePlan,
  deletePlan,
  listNodeGroups,
} from "../../api/admin";
import type {
  Plan,
  CreatePlanRequest,
  UpdatePlanRequest,
  NodeGroup,
  PlanRoutesResponse,
} from "../../api/types";
import PlanFormFields from "./PlanFormFields";
import ConcurrencyModeHint from "./ConcurrencyModeHint";
import PlanPreview from "./PlanPreview";
import PlanRoutesPanel from "./PlanRoutesPanel";
import { adminError } from "../../utils/adminError";
import { focusFirstInvalid } from "../../utils/formValidation";
import "./planEditor.css";
import "./plansList.css";
import {
  emptyForm,
  hasPlanFormErrors,
  toPlanFeatures,
  toPlanPrices,
  toPriceRows,
  toFeatureRows,
  validatePlanForm,
  type PlanForm,
} from "./planFormModel";

function formatTraffic(bytes: number | undefined, unlimited: string): string {
  if (bytes == null || bytes === 0) return unlimited;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

function formatSpeed(bps: number | undefined, unlimited: string): string {
  if (bps == null || bps === 0) return unlimited;
  return `${bps} Mbps`;
}



export default function Plans() {
  const { t } = useTranslation();
  const tRef = useRef(t);
  tRef.current = t;
  const [plans, setPlans] = useState<Plan[]>([]);
  const [nodeGroups, setNodeGroups] = useState<NodeGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [editModal, setEditModal] = useState<Plan | null>(null);
  const [publishModal, setPublishModal] = useState<Plan | null>(null);
  const [deleteModal, setDeleteModal] = useState<Plan | null>(null);
  const [form, setForm] = useState<PlanForm>(emptyForm);
  const [actionLoading, setActionLoading] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [initialForm, setInitialForm] = useState(JSON.stringify(emptyForm));
  const [discardModal, setDiscardModal] = useState(false);
  const [routeBusy, setRouteBusy] = useState(false);
  const [routeDirty, setRouteDirty] = useState(false);
  const [routeGroupConflict, setRouteGroupConflict] = useState(false);
  const [focusRoutes, setFocusRoutes] = useState(false);
  // Field errors stay hidden until the first save attempt, then track edits.
  const [showErrors, setShowErrors] = useState(false);
  const formErrors = validatePlanForm(form, t);
  const routeConflict = routeGroupConflict && editModal !== null && form.node_group_id !== editModal.node_group_id;
  const closeEditor = () => {
    if (actionLoading || routeBusy) return;
    if (routeDirty || JSON.stringify(form) !== initialForm) { setDiscardModal(true); return; }
    setRouteDirty(false);
    setCreateModal(false);
    setEditModal(null);
    setError(null);
    setShowErrors(false);
  };
  const checkForm = () => {
    setShowErrors(true);
    if (hasPlanFormErrors(formErrors)) {
      focusFirstInvalid();
      return false;
    }
    return true;
  };

  const fetchData = useCallback(async () => {
    try {
      const [plansData, groupsData] = await Promise.all([listPlans(), listNodeGroups()]);
      setPlans(plansData);
      setNodeGroups(groupsData);
      setError(null);
    } catch {
      setError(tRef.current("admin.plans.fetchError"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchData();
  }, [fetchData]);

  const handleCreate = async () => {
    if (!checkForm()) return;
    setError(null);
    setActionLoading(true);
    try {
      const req: CreatePlanRequest = {
        id: form.id.trim() || undefined,
        advertise: form.advertise,
        name: form.name.trim(),
        traffic_limit: form.traffic_limit ? Number(form.traffic_limit) * 1024 * 1024 * 1024 : undefined,
        duration_days: Number(form.duration_days),
        max_devices: Number(form.max_devices),
        max_concurrent: form.max_concurrent ? Number(form.max_concurrent) : undefined,
        speed_limit: form.speed_limit ? Number(form.speed_limit) : undefined,
        node_group_id: form.node_group_id,
        is_active: form.is_active,
        prices: toPlanPrices(form.prices),
        features: toPlanFeatures(form.features),
      };
      await createPlan(req);
      setCreateModal(false);
      setForm(emptyForm);
      setShowErrors(false);
      await fetchData();
    } catch (err) {
      setError(adminError(err, t("admin.plans.createError")));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEdit = async () => {
    if (!editModal || routeBusy || routeDirty || routeConflict || !checkForm()) return;
    setError(null);
    setActionLoading(true);
    try {
      const req: UpdatePlanRequest = {
        advertise: form.advertise,
        name: form.name.trim(),
        traffic_limit: form.traffic_limit ? Number(form.traffic_limit) * 1024 * 1024 * 1024 : null,
        duration_days: Number(form.duration_days),
        max_devices: Number(form.max_devices),
        max_concurrent: form.max_concurrent ? Number(form.max_concurrent) : null,
        speed_limit: form.speed_limit ? Number(form.speed_limit) : null,
        node_group_id: form.node_group_id,
        is_active: form.is_active,
        prices: toPlanPrices(form.prices),
        features: toPlanFeatures(form.features),
      };
      await updatePlan(editModal.id, req);
      setEditModal(null);
      setForm(emptyForm);
      setShowErrors(false);
      await fetchData();
    } catch (err) {
      setError(adminError(err, t("admin.plans.updateError")));
    } finally {
      setActionLoading(false);
    }
  };

  const handlePublish = async () => {
    if (!publishModal) return;
    setActionLoading(true);
    try {
      await updatePlan(publishModal.id, { is_advertised: !publishModal.is_advertised });
      setPublishModal(null);
      await fetchData();
    } catch (err) {
      setError(adminError(err, t("admin.plans.updateError")));
      setPublishModal(null);
    } finally { setActionLoading(false); }
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

  const handleEditOpen = async (plan: Plan, manageRoutes = false) => {
    setError(null);
    setDetailLoading(true);
    try {
    const full = await getPlan(plan.id);
    const nextForm: PlanForm = {
      id: full.id,
      advertise: full.advertise ?? false,
      name: full.name,
      traffic_limit: full.traffic_limit ? String(full.traffic_limit / (1024 * 1024 * 1024)) : "",
      duration_days: String(full.duration_days),
      max_devices: String(full.max_devices),
      max_concurrent: full.max_concurrent == null ? "" : String(full.max_concurrent),
      speed_limit: full.speed_limit ? String(full.speed_limit) : "",
      node_group_id: full.node_group_id,
      is_active: full.is_active,
      prices: toPriceRows(full.prices),
      features: toFeatureRows(full.features),
    };
    setForm(nextForm);
    setInitialForm(JSON.stringify(nextForm));
    setShowErrors(false);
    setEditModal(full);
    setRouteDirty(false);
    setRouteGroupConflict(false);
    setFocusRoutes(manageRoutes);
    } catch {
      setError(t("admin.plans.editor.detailError"));
    } finally {
      setDetailLoading(false);
    }
  };

  useEffect(() => {
    if (editModal && focusRoutes) {
      document.getElementById("plan-routes")?.scrollIntoView({ block: "start" });
      setFocusRoutes(false);
    }
  }, [editModal, focusRoutes]);

  const handleSavedRoutes = (routes: PlanRoutesResponse) => {
    if (!editModal || routes.node_group_id === editModal.node_group_id) return;
    // Apply and refresh can both reveal server-side isolation. Rebase the
    // saved group without overwriting an independently edited group draft.
    const previousGroup = editModal.node_group_id;
    setRouteGroupConflict(form.node_group_id !== previousGroup && form.node_group_id !== routes.node_group_id);
    setEditModal(plan => plan ? { ...plan, node_group_id: routes.node_group_id } : null);
    setForm(current => current.node_group_id === previousGroup ? { ...current, node_group_id: routes.node_group_id } : current);
    setInitialForm(current => JSON.stringify({ ...JSON.parse(current), node_group_id: routes.node_group_id }));
    const isolatedGroup: NodeGroup = { id: routes.node_group_id, name: routes.node_group_id, created_at: "" };
    setNodeGroups(current => current.some(group => group.id === isolatedGroup.id) ? current : [...current, isolatedGroup]);
    void listNodeGroups().then(groups => {
      setNodeGroups(groups.some(group => group.id === isolatedGroup.id) ? groups : [...groups, isolatedGroup]);
    }).catch(() => { /* The assigned ID remains selectable if group names cannot refresh. */ });
  };

  const savedForm = JSON.parse(initialForm) as PlanForm;
  const routePolicyChanged = editModal !== null && (
    form.node_group_id !== editModal.node_group_id ||
    form.traffic_limit !== savedForm.traffic_limit ||
    form.duration_days !== savedForm.duration_days ||
    form.max_devices !== savedForm.max_devices ||
    form.max_concurrent !== savedForm.max_concurrent ||
    form.speed_limit !== savedForm.speed_limit
  );

  const discardConfirmation = <Modal visible={discardModal} onDismiss={() => setDiscardModal(false)} header={t("admin.plans.editor.discardTitle")} footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs"><Button onClick={() => setDiscardModal(false)}>{t("admin.plans.cancel")}</Button><Button variant="primary" onClick={() => { setDiscardModal(false); setRouteDirty(false); setRouteGroupConflict(false); setCreateModal(false); setEditModal(null); setError(null); setShowErrors(false); }}>{t("admin.plans.editor.discard")}</Button></SpaceBetween></Box>}><SpaceBetween size="s"><div>{t("admin.plans.editor.discardMessage")}</div>{routeDirty && <div>{t("admin.routeManagement.discardRouteChanges", { defaultValue: "Your unapplied route selection changes will also be discarded." })}</div>}</SpaceBetween></Modal>;

  if (createModal || editModal) {
    return <ContentLayout header={<Header variant="h1" description={t("admin.plans.editor.description")} actions={<SpaceBetween direction="horizontal" size="xs"><Button disabled={actionLoading || routeBusy} onClick={closeEditor}>{t("admin.plans.cancel")}</Button><Button variant="primary" loading={actionLoading} disabled={routeBusy || routeDirty || routeConflict} onClick={() => void (editModal ? handleEdit() : handleCreate())}>{t("admin.plans.save")}</Button></SpaceBetween>}>{t(editModal ? "admin.plans.editTitle" : "admin.plans.createTitle")}</Header>}>
      <SpaceBetween size="l">
        {error && <Alert type="error">{error}</Alert>}
        {routeConflict && <Alert type="warning">{t("admin.routeManagement.groupConflict", { defaultValue: "The saved route group changed while you were editing. Your group draft was retained. Select the current saved group shown in Plan routes, or cancel and reopen, before saving." })}</Alert>}
        <Grid gridDefinition={[{ colspan: { default: 12, m: 8 } }, { colspan: { default: 12, m: 4 } }]}>
          <fieldset disabled={actionLoading || routeBusy} style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}>
            <PlanFormFields editing={!!editModal} form={form} errors={showErrors ? formErrors : undefined} nodeGroups={nodeGroups} onChange={patch => { if (!actionLoading && !routeBusy) setForm(f => ({ ...f, ...patch })); }} onFormUpdate={update => { if (!actionLoading && !routeBusy) setForm(update); }} />
          </fieldset>
          <div className="plan-editor-preview"><PlanPreview form={form} /></div>
        </Grid>
        {editModal ? <PlanRoutesPanel
          key={editModal.id}
          plan={editModal}
          nodeGroups={nodeGroups}
          policyChanged={routePolicyChanged}
          disabled={actionLoading}
          onBusyChange={setRouteBusy}
          onDirtyChange={setRouteDirty}
          onSavedRoutes={handleSavedRoutes}
        /> : <Alert type="info">{t("admin.routeManagement.savedPlanOnly")}</Alert>}
      </SpaceBetween>
      {discardConfirmation}
    </ContentLayout>;
  }

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.plans.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  const openCreate = () => { setError(null); setShowErrors(false); setForm(emptyForm); setInitialForm(JSON.stringify(emptyForm)); setCreateModal(true); };

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          counter={`(${plans.length})`}
          description={t("admin.plans.list.description")}
          actions={<Button variant="primary" iconName="add-plus" disabled={detailLoading} onClick={openCreate}>{t("admin.plans.create")}</Button>}
        >
          {t("admin.plans.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        <div className="plans-list__policy"><ConcurrencyModeHint /></div>
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        {plans.length === 0 ? (
          <Container>
            <Box textAlign="center" padding="l">
              <SpaceBetween size="s">
                <Box variant="h3" padding="n">{t("admin.plans.empty")}</Box>
                <Box color="text-body-secondary">{t("admin.plans.list.emptyBody")}</Box>
                <Button iconName="add-plus" onClick={openCreate}>{t("admin.plans.create")}</Button>
              </SpaceBetween>
            </Box>
          </Container>
        ) : (
          <ul className="plans-list__items">
            {plans.map(item => {
              const publishable = item.advertise && item.is_active && item.purchasable;
              const pub = item.is_advertised ? "published" : item.advertise ? "ready" : "private";
              const guide = item.is_advertised
                ? t("admin.plans.list.publishedHint")
                : !item.is_active ? t("admin.plans.list.needActive")
                : !item.purchasable ? t("admin.plans.list.needPrice")
                : !item.advertise ? t("admin.plans.list.needAdvertise")
                : t("admin.plans.list.readyHint");
              const concurrent = item.max_concurrent === null ? item.max_devices : item.max_concurrent;
              return (
                <li key={item.id}>
                  <Container>
                    <div className={`plans-list__card${item.is_active ? "" : " plans-list__card--inactive"}`}>
                      <div className="plans-list__heading">
                        <div className="plans-list__identity">
                          <h2 className="plans-list__name">{item.name}</h2>
                          <div className="plans-list__badges">
                            <span className={`plans-pill plans-pill--${pub}`}>{t(`admin.plans.editor.${pub}`)}</span>
                            <span className={`plans-pill plans-pill--${item.is_active ? "on" : "off"}`}>{t(item.is_active ? "admin.plans.list.active" : "admin.plans.list.inactive")}</span>
                            <span className={`plans-pill plans-pill--${item.purchasable ? "on" : "warn"}`}>{t(item.purchasable ? "admin.plans.list.priced" : "admin.plans.purchasableNo")}</span>
                          </div>
                        </div>
                        <div className="plans-list__actions">
                          {!item.is_advertised && publishable && (
                            <Button variant="primary" disabled={actionLoading || detailLoading} onClick={() => setPublishModal(item)}>{t("admin.plans.editor.publish")}</Button>
                          )}
                          <Button iconName="edit" disabled={detailLoading} onClick={() => void handleEditOpen(item)}>{t("admin.plans.edit")}</Button>
                          <ButtonDropdown
                            variant="icon"
                            ariaLabel={`${t("admin.plans.col.actions")}: ${item.name}`}
                            expandToViewport
                            items={[
                              { id: "routes", text: t("admin.routeManagement.manage"), disabled: detailLoading },
                              { id: "publish", text: t(item.is_advertised ? "admin.plans.editor.unpublish" : "admin.plans.editor.publish"), disabled: actionLoading || detailLoading || (!item.is_advertised && !publishable) },
                              { id: "delete", text: t("admin.plans.delete") },
                            ]}
                            onItemClick={({ detail }) => {
                              if (detail.id === "routes") void handleEditOpen(item, true);
                              if (detail.id === "publish") setPublishModal(item);
                              if (detail.id === "delete") setDeleteModal(item);
                            }}
                          />
                        </div>
                      </div>

                      <dl className="plans-list__stats">
                        <div><dt>{t("admin.plans.editor.monthlyTraffic")}</dt><dd>{formatTraffic(item.traffic_limit, t("admin.plans.unlimited"))}</dd></div>
                        <div><dt>{t("admin.plans.editor.assignmentDuration")}</dt><dd>{t("common.duration.days", { count: item.duration_days })}</dd></div>
                        <div><dt>{t("admin.plans.list.devices")}</dt><dd>{t("admin.plans.list.devicesValue", { count: item.max_devices })}<small>{t("admin.plans.list.concurrentValue", { count: concurrent })}</small></dd></div>
                        <div><dt>{t("admin.plans.col.speedLimit")}</dt><dd>{formatSpeed(item.speed_limit, t("admin.plans.unlimited"))}<small>{t("admin.plans.list.perSlot")}</small></dd></div>
                      </dl>

                      <div className="plans-list__footer">
                        <span className="plans-list__group">{t("admin.plans.col.nodeGroup")} <strong>{item.node_group_name ?? "-"}</strong></span>
                        <span className={`plans-list__guide${publishable || item.is_advertised ? "" : " plans-list__guide--todo"}`}>{guide}</span>
                      </div>
                    </div>
                  </Container>
                </li>
              );
            })}
          </ul>
        )}

        <Modal visible={publishModal !== null} onDismiss={() => !actionLoading && setPublishModal(null)} header={t(publishModal?.is_advertised ? "admin.plans.editor.unpublish" : "admin.plans.editor.publish")} footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs"><Button disabled={actionLoading} onClick={() => setPublishModal(null)}>{t("admin.plans.cancel")}</Button><Button variant="primary" loading={actionLoading} onClick={() => void handlePublish()}>{t(publishModal?.is_advertised ? "admin.plans.editor.unpublish" : "admin.plans.editor.publish")}</Button></SpaceBetween></Box>}>
          {t(publishModal?.is_advertised ? "admin.plans.editor.unpublishConfirm" : "admin.plans.editor.publishConfirm", { name: publishModal?.name })}
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
