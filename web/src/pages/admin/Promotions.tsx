import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ContentLayout,
  Flashbar,
  Header,
  Modal,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
} from "@cloudscape-design/components";
import {
  listPromotions,
  createPromotion,
  updatePromotion,
  deletePromotion,
  listPlans,
} from "../../api/admin";
import type { Plan, PromotionCode } from "../../api/types";
import { formatPriceCents } from "../../utils/planPricing";
import { focusFirstInvalid, hasFieldErrors } from "../../utils/formValidation";
import PromotionFormFields from "./PromotionFormFields";
import {
  emptyPromotionForm,
  toPromotionForm,
  toPromotionRequest,
  validatePromotionForm,
  type PromotionForm,
} from "./promotionFormModel";

export default function Promotions() {
  const { t } = useTranslation();
  const tRef = useRef(t);
  tRef.current = t;
  const [promotions, setPromotions] = useState<PromotionCode[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [editModal, setEditModal] = useState<PromotionCode | null>(null);
  const [deleteModal, setDeleteModal] = useState<PromotionCode | null>(null);
  const [form, setForm] = useState<PromotionForm>(emptyPromotionForm);
  const [actionLoading, setActionLoading] = useState(false);
  // Field errors stay hidden until the first save attempt, then track edits.
  const [showErrors, setShowErrors] = useState(false);
  const formErrors = validatePromotionForm(form, t);

  const fetchData = useCallback(async () => {
    try {
      const [promotionData, planData] = await Promise.all([listPromotions(), listPlans()]);
      setPromotions(promotionData);
      setPlans(planData);
      setError(null);
    } catch {
      setError(tRef.current("admin.promotions.fetchError"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchData();
  }, [fetchData]);

  const formatDiscount = (item: PromotionCode) =>
    item.discount_type === "percent"
      ? t("admin.promotions.discountPercent", { value: item.discount_value })
      : t("admin.promotions.discountFixed", { value: formatPriceCents(item.discount_value) });

  const formatRedemptions = (item: PromotionCode) =>
    item.max_redemptions === null
      ? t("admin.promotions.redeemedUnlimited", { count: item.redeemed_count })
      : `${item.redeemed_count} / ${item.max_redemptions}`;

  const checkForm = () => {
    setShowErrors(true);
    if (hasFieldErrors(formErrors)) {
      focusFirstInvalid();
      return false;
    }
    return true;
  };

  const handleCreate = async () => {
    if (!checkForm()) return;
    setActionLoading(true);
    try {
      await createPromotion(toPromotionRequest(form, false));
      setCreateModal(false);
      setForm(emptyPromotionForm);
      await fetchData();
    } catch {
      setError(t("admin.promotions.createError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEdit = async () => {
    if (!editModal || !checkForm()) return;
    setActionLoading(true);
    try {
      await updatePromotion(editModal.id, toPromotionRequest(form, true));
      setEditModal(null);
      setForm(emptyPromotionForm);
      await fetchData();
    } catch {
      setError(t("admin.promotions.updateError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deletePromotion(deleteModal.id);
      setDeleteModal(null);
      await fetchData();
    } catch {
      setError(t("admin.promotions.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEditOpen = (promotion: PromotionCode) => {
    setForm(toPromotionForm(promotion));
    setShowErrors(false);
    setEditModal(promotion);
  };

  const renderForm = (showActive: boolean) => (
    <PromotionFormFields
      form={form}
      errors={showErrors ? formErrors : undefined}
      plans={plans}
      showActive={showActive}
      onChange={(patch) => setForm((f) => ({ ...f, ...patch }))}
    />
  );

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.promotions.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header variant="h1" description={t("admin.promotions.subtitle")}>
          {t("admin.promotions.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <Table
          items={promotions}
          trackBy="id"
          header={
            <Header
              actions={
                <Button
                  variant="primary"
                  onClick={() => {
                    setForm(emptyPromotionForm);
                    setShowErrors(false);
                    setCreateModal(true);
                  }}
                >
                  {t("admin.promotions.create")}
                </Button>
              }
              counter={`(${promotions.length})`}
            >
              {t("admin.promotions.title")}
            </Header>
          }
          columnDefinitions={[
            { id: "code", header: t("admin.promotions.col.code"), cell: (item) => item.code },
            {
              id: "discount",
              header: t("admin.promotions.col.discount"),
              cell: (item) => formatDiscount(item),
            },
            {
              id: "validity",
              header: t("admin.promotions.col.validity"),
              minWidth: 220,
              cell: (item) =>
                `${new Date(item.valid_from).toLocaleString()} — ${new Date(item.valid_until).toLocaleString()}`,
            },
            {
              id: "redemptions",
              header: t("admin.promotions.col.redemptions"),
              cell: (item) => formatRedemptions(item),
            },
            {
              id: "active",
              header: t("admin.promotions.col.active"),
              cell: (item) => (
                <StatusIndicator type={item.is_active ? "success" : "stopped"}>
                  {item.is_active
                    ? t("admin.promotions.activeYes")
                    : t("admin.promotions.activeNo")}
                </StatusIndicator>
              ),
            },
            {
              id: "actions",
              header: t("admin.promotions.col.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button variant="inline-link" onClick={() => handleEditOpen(item)}>
                    {t("admin.promotions.edit")}
                  </Button>
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>
                    {t("admin.promotions.delete")}
                  </Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.promotions.empty")}</Box>}
        />

        <Modal
          visible={createModal}
          size="large"
          onDismiss={() => setCreateModal(false)}
          header={t("admin.promotions.createTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setCreateModal(false)}>{t("admin.promotions.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleCreate()}>
                  {t("admin.promotions.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {renderForm(false)}
        </Modal>

        <Modal
          visible={editModal !== null}
          size="large"
          onDismiss={() => setEditModal(null)}
          header={t("admin.promotions.editTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setEditModal(null)}>{t("admin.promotions.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleEdit()}>
                  {t("admin.promotions.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {renderForm(true)}
        </Modal>

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header={t("admin.promotions.deleteTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.promotions.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.promotions.confirmDelete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.promotions.deleteMessage", { code: deleteModal?.code })}
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
