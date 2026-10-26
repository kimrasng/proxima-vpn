import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert, Box, Button, ColumnLayout, Container, FormField, Header, Input, Modal, RadioGroup, Select,
  SpaceBetween, StatusIndicator, Table, Textarea, Tiles,
} from "@cloudscape-design/components";
import { ApiError } from "../../api/client";
import { getUserOrderReceipt, grantUserPlan, listUserOrders } from "../../api/admin";
import type { AdminUserOrder, OrderReceipt, Plan, UserDetail } from "../../api/types";
import OrderReceiptDetails from "../../components/OrderReceiptDetails";
import { formatPriceCents } from "../../utils/planPricing";
import { formatAbsoluteTime } from "../../utils/relativeTime";

type Props = {
  user: UserDetail;
  plans: Plan[];
  onGranted: () => void;
};

type PriceMode = "free" | "listed" | "custom";

const FALLBACK_DURATIONS = [30, 90, 180, 365];

function addDays(base: Date, days: number): Date {
  return new Date(base.getTime() + days * 86400000);
}

export default function UserPlanGrantPanel({ user, plans, onGranted }: Props) {
  const { t, i18n } = useTranslation();
  const activePlans = useMemo(() => plans.filter((plan) => plan.is_active), [plans]);
  const [planId, setPlanId] = useState(user.plan_id ?? activePlans[0]?.id ?? "");
  const plan = activePlans.find((item) => item.id === planId) ?? null;
  const durations = useMemo(() => {
    const listed = (plan?.prices ?? []).map((price) => price.duration_days);
    return listed.length > 0 ? [...new Set(listed)].sort((a, b) => a - b) : FALLBACK_DURATIONS;
  }, [plan]);
  const [durationChoice, setDurationChoice] = useState<string>("");
  const [customDays, setCustomDays] = useState("30");
  const [priceMode, setPriceMode] = useState<PriceMode>("free");
  const [customPrice, setCustomPrice] = useState("");
  const [note, setNote] = useState("");
  const [showErrors, setShowErrors] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [saving, setSaving] = useState(false);
  const [flash, setFlash] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const [orders, setOrders] = useState<AdminUserOrder[]>([]);
  const [ordersLoading, setOrdersLoading] = useState(true);
  const [ordersError, setOrdersError] = useState(false);
  const [receipt, setReceipt] = useState<OrderReceipt | null>(null);
  const [receiptLoading, setReceiptLoading] = useState<string | null>(null);

  const openReceipt = async (orderId: string) => {
    setReceiptLoading(orderId);
    try {
      setReceipt(await getUserOrderReceipt(user.id, orderId));
    } catch {
      setFlash({ type: "error", text: t("admin.userDetail.grant.receiptError") });
    } finally {
      setReceiptLoading(null);
    }
  };

  const loadOrders = useCallback(async () => {
    try {
      setOrders(await listUserOrders(user.id));
      setOrdersError(false);
    } catch {
      setOrdersError(true);
    } finally {
      setOrdersLoading(false);
    }
  }, [user.id]);

  useEffect(() => { void loadOrders(); }, [loadOrders]);

  const selectedDuration = durationChoice || String(durations[0] ?? 30);
  const days = selectedDuration === "custom" ? Number(customDays) : Number(selectedDuration);
  const listedPrice = plan?.prices?.find((price) => price.duration_days === days)?.price_cents;
  const priceCents = priceMode === "free" ? 0
    : priceMode === "listed" ? (listedPrice ?? 0)
    : Math.round(Number(customPrice) * 100);

  const isRenewal = !!user.plan_id && user.plan_id === planId && !!user.plan_expires_at
    && new Date(user.plan_expires_at).getTime() > Date.now();
  const hasRunningTerm = !!user.plan_expires_at && new Date(user.plan_expires_at).getTime() > Date.now();
  const base = hasRunningTerm ? new Date(user.plan_expires_at as string) : new Date();
  const newExpiry = Number.isFinite(days) && days > 0 ? addDays(base, days) : null;
  const date = (value: Date | string) => new Date(value).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language);

  const errors = {
    plan: !plan ? t("admin.userDetail.grant.errors.plan") : "",
    days: !Number.isInteger(days) || days < 1 || days > 3650 ? t("admin.userDetail.grant.errors.days") : "",
    price: priceMode === "custom" && (!/^\d+(\.\d{1,2})?$/.test(customPrice.trim()) || priceCents < 0)
      ? t("admin.userDetail.grant.errors.price") : "",
    note: note.trim().length > 200 ? t("admin.userDetail.grant.errors.note") : "",
  };
  const invalid = Object.values(errors).some(Boolean);

  const submit = async () => {
    setSaving(true);
    try {
      await grantUserPlan(user.id, { plan_id: planId, duration_days: days, price_cents: priceCents, note: note.trim() });
      setFlash({ type: "success", text: t("admin.userDetail.grant.success", { plan: plan?.name, days }) });
      setNote("");
      setShowErrors(false);
      onGranted();
      await loadOrders();
    } catch (err) {
      const code = err instanceof ApiError ? (err.body as { code?: string } | undefined)?.code : undefined;
      setFlash({ type: "error", text: code === "pending_order" ? t("admin.userDetail.grant.pendingOrder") : t("admin.userDetail.grant.error") });
    } finally {
      setSaving(false);
      setConfirm(false);
    }
  };

  const providerLabel = (order: AdminUserOrder) => order.provider === "grant" ? t("admin.userDetail.grant.source.grant")
    : order.provider === "stripe" ? t("admin.userDetail.grant.source.stripe")
    : order.provider === "promotion" ? t("admin.userDetail.grant.source.promotion")
    : t("admin.userDetail.grant.source.manual");

  return (
    <SpaceBetween size="l">
      {flash && <Alert type={flash.type} dismissible onDismiss={() => setFlash(null)}>{flash.text}</Alert>}

      <ColumnLayout columns={2} minColumnWidth={360}>
        <Container header={<Header variant="h2" description={t("admin.userDetail.grant.description")}>{t("admin.userDetail.grant.title")}</Header>}>
          <SpaceBetween size="l">
            <FormField label={t("admin.userDetail.grant.plan")} errorText={showErrors ? errors.plan : undefined}>
              <Select
                selectedOption={plan ? { value: plan.id, label: plan.name } : null}
                placeholder={t("admin.userDetail.grant.planPlaceholder")}
                options={activePlans.map((item) => ({ value: item.id, label: item.name }))}
                onChange={({ detail }) => { setPlanId(detail.selectedOption.value ?? ""); setDurationChoice(""); }}
              />
            </FormField>

            <FormField label={t("admin.userDetail.grant.duration")} errorText={showErrors ? errors.days : undefined}>
              <SpaceBetween size="s">
                <Tiles
                  value={selectedDuration}
                  onChange={({ detail }) => setDurationChoice(detail.value)}
                  columns={3}
                  items={[
                    ...durations.map((value) => ({
                      value: String(value),
                      label: t("admin.userDetail.grant.days", { days: value }),
                      description: plan?.prices?.find((price) => price.duration_days === value)
                        ? formatPriceCents(plan.prices.find((price) => price.duration_days === value)?.price_cents ?? 0)
                        : undefined,
                    })),
                    { value: "custom", label: t("admin.userDetail.grant.customDays") },
                  ]}
                />
                {selectedDuration === "custom" && (
                  <Input type="number" inputMode="numeric" value={customDays}
                    ariaLabel={t("admin.userDetail.grant.customDays")}
                    onChange={({ detail }) => setCustomDays(detail.value)} />
                )}
              </SpaceBetween>
            </FormField>

            <FormField label={t("admin.userDetail.grant.price")} description={t("admin.userDetail.grant.priceHint")}
              errorText={showErrors ? errors.price : undefined}>
              <SpaceBetween size="s">
                <RadioGroup
                  value={priceMode}
                  onChange={({ detail }) => setPriceMode(detail.value as PriceMode)}
                  items={[
                    { value: "free", label: t("admin.userDetail.grant.priceFree") },
                    ...(listedPrice !== undefined ? [{ value: "listed", label: t("admin.userDetail.grant.priceListed", { price: formatPriceCents(listedPrice) }) }] : []),
                    { value: "custom", label: t("admin.userDetail.grant.priceCustom") },
                  ]}
                />
                {priceMode === "custom" && (
                  <Input value={customPrice} inputMode="decimal" placeholder="0.00"
                    ariaLabel={t("admin.userDetail.grant.priceCustom")}
                    onChange={({ detail }) => setCustomPrice(detail.value)} />
                )}
              </SpaceBetween>
            </FormField>

            <FormField label={t("admin.userDetail.grant.note")} description={t("admin.userDetail.grant.noteHint")}
              errorText={showErrors ? errors.note : undefined}>
              <Textarea value={note} rows={2} placeholder={t("admin.userDetail.grant.notePlaceholder")}
                onChange={({ detail }) => setNote(detail.value)} />
            </FormField>

            <Box float="right">
              <Button variant="primary" onClick={() => { setShowErrors(true); if (!invalid) setConfirm(true); }}>
                {t("admin.userDetail.grant.submit")}
              </Button>
            </Box>
          </SpaceBetween>
        </Container>

        <Container header={<Header variant="h2">{t("admin.userDetail.grant.summary")}</Header>}>
          <SpaceBetween size="m">
            <div>
              <Box variant="awsui-key-label">{t("admin.userDetail.grant.currentPlan")}</Box>
              <Box>{user.plan_name ?? t("admin.users.noPlan")}{user.plan_expires_at ? ` · ${date(user.plan_expires_at)}` : ""}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.userDetail.grant.result")}</Box>
              <Box fontSize="heading-s" fontWeight="bold">
                {plan ? `${plan.name} · ${t("admin.userDetail.grant.days", { days: Number.isFinite(days) ? days : 0 })}` : "—"}
              </Box>
              <Box color="text-body-secondary">
                {!plan || !newExpiry ? "—"
                  : isRenewal ? t("admin.userDetail.grant.extend", { from: date(base), to: date(newExpiry) })
                  : hasRunningTerm ? t("admin.userDetail.grant.change", { to: date(newExpiry) })
                  : t("admin.userDetail.grant.fresh", { to: date(newExpiry) })}
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("admin.userDetail.grant.recorded")}</Box>
              <Box>{priceCents > 0 ? formatPriceCents(priceCents) : t("admin.userDetail.grant.priceFree")}</Box>
            </div>
            <Alert type="info">{t("admin.userDetail.grant.visibility")}</Alert>
          </SpaceBetween>
        </Container>
      </ColumnLayout>

      <Table
        loading={ordersLoading}
        loadingText={t("admin.userDetail.grant.ordersLoading")}
        items={orders}
        trackBy="id"
        wrapLines
        header={<Header counter={`(${orders.length})`} description={t("admin.userDetail.grant.ordersDescription")}>
          {t("admin.userDetail.grant.orders")}
        </Header>}
        columnDefinitions={[
          { id: "plan", header: t("admin.userDetail.grant.col.plan"), cell: (order) => `${order.plan_name} · ${t("admin.userDetail.grant.days", { days: order.duration_days })}` },
          { id: "source", header: t("admin.userDetail.grant.col.source"), cell: (order) => <SpaceBetween size="xxxs">
            <Box>{providerLabel(order)}</Box>
            {order.note && <Box variant="small" color="text-body-secondary">{order.note}</Box>}
          </SpaceBetween> },
          { id: "price", header: t("admin.userDetail.grant.col.price"), cell: (order) => order.price_cents > 0 ? formatPriceCents(order.price_cents) : t("admin.userDetail.grant.priceFree") },
          { id: "status", header: t("admin.userDetail.grant.col.status"), cell: (order) => <StatusIndicator
            type={order.status === "paid" ? "success" : order.status === "pending" ? "pending" : "stopped"}>
            {t(`admin.orders.status.${order.status}`)}
          </StatusIndicator> },
          { id: "term", header: t("admin.userDetail.grant.col.term"), cell: (order) => order.granted_plan_expires_after
            ? order.granted_plan_expires_before
              ? `${date(order.granted_plan_expires_before)} → ${date(order.granted_plan_expires_after)}`
              : t("admin.userDetail.grant.until", { to: date(order.granted_plan_expires_after) })
            : "—" },
          { id: "at", header: t("admin.userDetail.grant.col.at"), cell: (order) => <SpaceBetween size="xxxs">
            <Box>{formatAbsoluteTime(order.paid_at ?? order.created_at)}</Box>
            {order.paid_by && <Box variant="small" color="text-body-secondary">{order.paid_by}</Box>}
          </SpaceBetween> },
          { id: "receipt", header: t("admin.userDetail.grant.col.receipt"), cell: (order) => order.status === "paid" ? (
            <Button variant="inline-link" loading={receiptLoading === order.id} disabled={receiptLoading !== null}
              ariaLabel={`${t("admin.userDetail.grant.viewReceipt")} ${order.plan_name}`}
              onClick={() => void openReceipt(order.id)}>{t("admin.userDetail.grant.viewReceipt")}</Button>
          ) : "—" },
        ]}
        empty={ordersError
          ? <Box textAlign="center" color="text-status-error">{t("admin.userDetail.grant.ordersError")}</Box>
          : <Box textAlign="center" color="text-body-secondary">{t("admin.userDetail.grant.ordersEmpty")}</Box>}
      />

      <Modal visible={receipt !== null} onDismiss={() => setReceipt(null)} header={t("admin.userDetail.grant.receiptTitle")}
        closeAriaLabel={t("admin.userDetail.grant.cancel")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
          <Button onClick={() => setReceipt(null)}>{t("admin.userDetail.grant.close")}</Button>
          {receipt?.receipt_url && <Button variant="primary" href={receipt.receipt_url} target="_blank" iconName="external" iconAlign="right">
            {t(receipt.receipt_url.includes("invoice.stripe.com") ? "admin.userDetail.grant.openInvoice" : "admin.userDetail.grant.openStripe")}</Button>}
        </SpaceBetween></Box>}>
        {receipt && <OrderReceiptDetails receipt={receipt} />}
      </Modal>

      <Modal visible={confirm} onDismiss={() => !saving && setConfirm(false)} header={t("admin.userDetail.grant.confirmTitle")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
          <Button variant="link" disabled={saving} onClick={() => setConfirm(false)}>{t("admin.userDetail.grant.cancel")}</Button>
          <Button variant="primary" loading={saving} onClick={() => void submit()}>{t("admin.userDetail.grant.confirm")}</Button>
        </SpaceBetween></Box>}>
        <SpaceBetween size="s">
          <Box>{t("admin.userDetail.grant.confirmBody", { email: user.email, plan: plan?.name, days })}</Box>
          {newExpiry && <Box color="text-body-secondary">{isRenewal
            ? t("admin.userDetail.grant.extend", { from: date(base), to: date(newExpiry) })
            : hasRunningTerm ? t("admin.userDetail.grant.change", { to: date(newExpiry) })
            : t("admin.userDetail.grant.fresh", { to: date(newExpiry) })}</Box>}
        </SpaceBetween>
      </Modal>
    </SpaceBetween>
  );
}
