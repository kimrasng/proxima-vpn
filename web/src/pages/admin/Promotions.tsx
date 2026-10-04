import { useEffect, useState } from "react";
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
import type { Plan, PromotionCode, PromotionRequest } from "../../api/types";
import { formatPriceCents } from "../../utils/planPricing";
import PromotionFormFields from "./PromotionFormFields";
import {
  emptyPromotionForm,
  localDateTimeToIso,
  splitList,
  toPromotionForm,
  type PromotionForm,
} from "./promotionFormModel";

export default function Promotions() {
  const { t } = useTranslation();
  const [promotions, setPromotions] = useState<PromotionCode[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [editModal, setEditModal] = useState<PromotionCode | null>(null);
  const [deleteModal, setDeleteModal] = useState<PromotionCode | null>(null);
  const [form, setForm] = useState<PromotionForm>(emptyPromotionForm);
  const [actionLoading, setActionLoading] = useState(false);

  const fetchData = async () => {
    try {
      const [promotionData, planData] = await Promise.all([listPromotions(), listPlans()]);
      setPromotions(promotionData);
      setPlans(planData);
      setError(null);
    } catch {
      setError(t("admin.promotions.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchData();
  }, []);

  const formatDiscount = (item: PromotionCode) =>
    item.discount_type === "percent"
      ? t("admin.promotions.discountPercent", { value: item.discount_value })
      : t("admin.promotions.discountFixed", { value: formatPriceCents(item.discount_value) });

  const formatRedemptions = (item: PromotionCode) =>
    item.max_redemptions === null
      ? t("admin.promotions.redeemedUnlimited", { count: item.redeemed_count })
      : `${item.redeemed_count} / ${item.max_redemptions}`;

  // Returns null and sets the inline error when a field is unusable, so the
  // caller can abort before touching the API.
  const collectRequest = (includeActive: boolean): PromotionRequest | null => {
    const code = form.code.trim();
    if (!code) {
      setError(t("admin.promotions.form.codeRequired"));
      return null;
    }

    const rawValue = Number(form.discountValue);
    if (!Number.isFinite(rawValue)) {
      setError(t("admin.promotions.form.discountValueInvalid"));
      return null;
    }
    if (form.discountType === "percent") {
      if (!Number.isInteger(rawValue) || rawValue < 1 || rawValue > 100) {
        setError(t("admin.promotions.form.percentRangeInvalid"));
        return null;
      }
    } else if (rawValue <= 0) {
      setError(t("admin.promotions.form.fixedRangeInvalid"));
      return null;
    }
    const discountValue =
      form.discountType === "percent" ? rawValue : Math.round(rawValue * 100);

    const validFrom = localDateTimeToIso(form.validFromDate, form.validFromTime);
    const validUntil = localDateTimeToIso(form.validUntilDate, form.validUntilTime);
    if (!validFrom || !validUntil) {
      setError(t("admin.promotions.form.windowRequired"));
      return null;
    }
    if (new Date(validUntil) <= new Date(validFrom)) {
      setError(t("admin.promotions.form.windowOrderInvalid"));
      return null;
    }

    const minOrderDollars = Number(form.minOrderDollars || "0");
    if (!Number.isFinite(minOrderDollars) || minOrderDollars < 0) {
      setError(t("admin.promotions.form.minOrderInvalid"));
      return null;
    }

    let maxRedemptions: number | null = null;
    if (!form.unlimitedRedemptions) {
      const parsed = Number(form.maxRedemptions);
      if (!Number.isInteger(parsed) || parsed <= 0) {
        setError(t("admin.promotions.form.maxRedemptionsInvalid"));
        return null;
      }
      maxRedemptions = parsed;
    }

    const perUser = Number(form.maxRedemptionsPerUser);
    if (!Number.isInteger(perUser) || perUser <= 0) {
      setError(t("admin.promotions.form.perUserInvalid"));
      return null;
    }

    const durationDays: number[] = [];
    for (const entry of splitList(form.durationDays)) {
      const days = Number(entry);
      if (!Number.isInteger(days) || days <= 0) {
        setError(t("admin.promotions.form.durationDaysInvalid"));
        return null;
      }
      durationDays.push(days);
    }

    return {
      code,
      discount_type: form.discountType,
      discount_value: discountValue,
      valid_from: validFrom,
      valid_until: validUntil,
      min_order_cents: Math.round(minOrderDollars * 100),
      max_redemptions: maxRedemptions,
      max_redemptions_per_user: perUser,
      first_purchase_only: form.firstPurchaseOnly,
      plan_ids: form.planIds,
      duration_days: durationDays,
      allowed_user_ids: splitList(form.allowedUserIds),
      ...(includeActive ? { is_active: form.isActive } : {}),
    };
  };

  const handleCreate = async () => {
    const req = collectRequest(false);
    if (!req) return;
    setActionLoading(true);
    try {
      await createPromotion(req);
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
    if (!editModal) return;
    const req = collectRequest(true);
    if (!req) return;
    setActionLoading(true);
    try {
      await updatePromotion(editModal.id, req);
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
    setEditModal(promotion);
  };

  const renderForm = (showActive: boolean) => (
    <PromotionFormFields
      form={form}
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
