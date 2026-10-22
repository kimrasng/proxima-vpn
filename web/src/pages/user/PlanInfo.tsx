import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Container,
  SpaceBetween,
  Box,
  Button,
  Spinner,
  Flashbar,
  type FlashbarProps,
  StatusIndicator,
  type StatusIndicatorProps,
  ColumnLayout,
  Badge,
  Icon,
  Input,
  FormField,
  Table,
  Modal,
  Alert,
  Tiles,
} from "@cloudscape-design/components";
import {
  borderRadiusInput, borderRadiusItem, colorBackgroundCellShaded, colorBackgroundInputDefault,
  colorBackgroundItemSelected, colorBorderDividerDefault, colorBorderInputDefault, colorBorderItemSelected,
} from "@cloudscape-design/design-tokens";
import "./planInfo.css";
import type {
  UserProfile,
  UserPlanItem,
  OrderResponse,
  OrderStatus,
  PaymentProviderItem,
  PromotionRejectReason,
  OrderReceipt,
} from "../../api/types";
import * as userApi from "../../api/user";
import { ApiError } from "../../api/client";
import { planSavings, formatPriceCents } from "../../utils/planPricing";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "∞";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

// The PATCH endpoint reports a refused code as a 400 with this envelope rather
// than a transport failure, so it has to be read off the error body.
function isPromotionRejectReason(value: unknown): value is PromotionRejectReason {
	return (
		value === "not_found" ||
		value === "inactive" ||
		value === "outside_window" ||
		value === "plan_not_eligible" ||
		value === "below_minimum" ||
		value === "not_first_purchase" ||
		value === "user_not_allowed" ||
		value === "overall_cap_reached" ||
		value === "user_cap_reached"
	);
}

function rejectionFromError(err: unknown): PromotionRejectReason | null {
	if (!(err instanceof ApiError) || err.status !== 400 || typeof err.body !== "object" || err.body === null) {
		return null;
	}
	const error = Object.entries(err.body).find(([key]) => key === "error")?.[1];
	const reason = Object.entries(err.body).find(([key]) => key === "reason")?.[1];
	return error === "promotion_rejected" && isPromotionRejectReason(reason) ? reason : null;
}

// "expired" borrows the warning severity the user pages already use for a lapsed
// plan: it needs attention but nothing failed.
function orderStatusIndicatorType(status: OrderStatus): StatusIndicatorProps.Type {
  switch (status) {
    case "paid":
      return "success";
    case "cancelled":
      return "stopped";
    case "expired":
      return "warning";
    default:
      return "pending";
  }
}

// What the checkout modal is paying for: a fresh plan/duration pick, or an
// order that already exists (pending, or just created with a refused code).
type CheckoutTarget = {
  planId: string;
  planName: string;
  durationDays: number;
  priceCents: number;
  discountCents: number;
  order: OrderResponse | null;
};

const STATUS_LABEL_KEYS: Record<string, string> = {
  active: "user.dashboard.statusActive",
  suspended: "user.dashboard.statusSuspended",
  expired: "user.dashboard.statusExpired",
};

export default function PlanInfo() {
  const { t, i18n } = useTranslation();
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [plans, setPlans] = useState<UserPlanItem[]>([]);
  const [orders, setOrders] = useState<OrderResponse[]>([]);
  const [providers, setProviders] = useState<PaymentProviderItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [receipt, setReceipt] = useState<OrderReceipt | null>(null);
  const [receiptLoading, setReceiptLoading] = useState<string | null>(null);
  const [selectedDurations, setSelectedDurations] = useState<Record<string, number>>({});
  const [checkout, setCheckout] = useState<CheckoutTarget | null>(null);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);
  const [providerName, setProviderName] = useState<string | undefined>();
  const [cancelModal, setCancelModal] = useState<OrderResponse | null>(null);
  const [promotionCode, setPromotionCode] = useState("");

  const loadData = async () => {
    try {
      setLoading(true);
      const [profileData, planList, orderList, providerList] = await Promise.all([
        userApi.getProfile(),
        userApi.listPlans(i18n.language),
        userApi.listMyOrders(),
        userApi.listPaymentProviders(),
      ]);
      setProfile(profileData);
      setPlans(planList);
      setOrders(orderList);
      setProviders(providerList);
    } catch {
      setFlash([{ type: "error", content: t("user.plan.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [i18n.language]);

  // Stripe sends the browser back here, but the plan is granted by the webhook, so
  // "success" only means the payment went through — the order may still read pending.
  useEffect(() => {
    const checkout = new URLSearchParams(window.location.search).get("checkout");
    if (checkout !== "success" && checkout !== "cancelled") return;

    setFlash([
      {
        type: "info",
        content:
          checkout === "success"
            ? t("user.plan.order.checkout.successPending")
            : t("user.plan.order.checkout.cancelled"),
        dismissible: true,
        onDismiss: () => setFlash([]),
      },
    ]);
    window.history.replaceState({}, "", window.location.pathname);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openCheckout = (target: CheckoutTarget) => {
    setCheckout(target);
    setCheckoutError(null);
    setPromotionCode("");
    setProviderName((current) =>
      current && providers.some((provider) => provider.name === current)
        ? current
        : (providers.find((provider) => provider.mode === "redirect") ?? providers[0])?.name,
    );
  };

  const openOrderCheckout = (order: OrderResponse) =>
    openCheckout({
      planId: order.plan_id,
      planName: order.plan_name,
      durationDays: order.duration_days,
      priceCents: order.price_cents,
      discountCents: order.discount_cents ?? 0,
      order,
    });

  const closeCheckout = () => {
    setCheckout(null);
    setCheckoutError(null);
    setPromotionCode("");
  };

  // Hands an existing pending order to the chosen provider. A redirect provider
  // (Stripe) leaves the SPA; a manual one only records the choice, so the order
  // waits for an admin to confirm the transfer.
  const payWithProvider = async (orderId: string) => {
    const provider = providers.find((item) => item.name === providerName);
    if (!provider) throw new Error("no payment provider selected");
    const session = await userApi.startCheckout(orderId, provider.name);
    if (session.mode === "redirect") {
      if (!session.redirect_url) throw new Error("checkout returned no redirect URL");
      window.location.href = session.redirect_url;
      return;
    }
    closeCheckout();
    setFlash([{ type: "success", content: t("user.plan.order.placed"), dismissible: true, onDismiss: () => setFlash([]) }]);
    await loadData();
  };

  const handleConfirmCheckout = async () => {
    if (!checkout || submitting) return;
    const code = promotionCode.trim();
    setSubmitting(true);
    setCheckoutError(null);
    try {
      let order = checkout.order;
      if (order) {
        if (code) {
          try {
            await userApi.applyPromotionToOrder(order.id, code);
          } catch (err) {
            const reason = rejectionFromError(err);
            if (!reason) throw err;
            setCheckoutError(t(`user.plan.promotion.rejected.${reason}`));
            return;
          }
        }
      } else {
        const result = await userApi.createOrder({
          plan_id: checkout.planId,
          duration_days: checkout.durationDays,
          ...(code ? { promotion_code: code } : {}),
        });
        order = result.order;
        if (result.promotion_rejected) {
          // The order now exists without the discount. Keep the modal on it so the
          // user can fix the code or pay the full price; reloading the whole page
          // here would unmount the modal mid-decision.
          setCheckout({ ...checkout, order, priceCents: order.price_cents, discountCents: order.discount_cents ?? 0 });
          setCheckoutError(t(`user.plan.promotion.rejected.${result.promotion_rejected}`));
          setOrders(await userApi.listMyOrders());
          return;
        }
        if (order.status === "paid") {
          closeCheckout();
          setFlash([{ type: "success", content: t("user.plan.checkoutModal.paid"), dismissible: true, onDismiss: () => setFlash([]) }]);
          await loadData();
          return;
        }
      }
      await payWithProvider(order.id);
    } catch (err) {
      setCheckoutError(
        err instanceof ApiError && err.status === 409 ? t("user.plan.order.conflict") : t("user.plan.order.error"),
      );
    } finally {
      setSubmitting(false);
    }
  };

  const providerLabel = (name: string) =>
    name === "stripe" ? t("user.plan.checkoutModal.providerStripe")
      : name === "admin" ? t("user.plan.checkoutModal.providerAdmin") : name;
  const providerHint = (name: string) =>
    name === "stripe" ? t("user.plan.checkoutModal.providerStripeHint")
      : name === "admin" ? t("user.plan.checkoutModal.providerAdminHint") : undefined;

  const handleCancelOrder = async () => {
    if (!cancelModal) return;
    try {
      setSubmitting(true);
      await userApi.cancelOrder(cancelModal.id);
      setFlash([{ type: "success", content: t("user.plan.order.cancelSuccess"), dismissible: true, onDismiss: () => setFlash([]) }]);
      setCancelModal(null);
      await loadData();
    } catch {
      setFlash([{ type: "error", content: t("user.plan.order.cancelError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setSubmitting(false);
    }
  };

  const handleViewReceipt = async (orderId: string) => {
    try {
      setReceiptLoading(orderId);
      setReceipt(await userApi.getOrderReceipt(orderId));
    } catch {
      setFlash([{ type: "error", content: t("user.plan.order.receipt.error"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setReceiptLoading(null);
    }
  };

  const pendingOrder = orders.find((o) => o.status === "pending") ?? null;

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1" description={t("user.plan.subtitle")}>{t("user.plan.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  const isExpired = profile?.plan_expires_at
    ? new Date(profile.plan_expires_at) < new Date()
    : false;

  const hasPlan = !!profile?.plan_name && !isExpired;

  return (
    <ContentLayout header={<Header variant="h1" description={t("user.plan.subtitle")}>{t("user.plan.title")}</Header>}>
      <div className="plan-info__page">
      <SpaceBetween size="l">
        <Flashbar items={flash} />

        {hasPlan && profile && (
          <Container header={<Header variant="h2">{t("user.plan.currentPlan")}</Header>}>
            <ColumnLayout columns={4} variant="text-grid">
              <SpaceBetween size="xs">
                <Box variant="awsui-key-label">{t("user.plan.planName")}</Box>
                <Box>{profile.plan_name}</Box>
              </SpaceBetween>
              <SpaceBetween size="xs">
                <Box variant="awsui-key-label">{t("user.plan.trafficLimit")}</Box>
                <Box>{profile.traffic_limit ? formatBytes(profile.traffic_limit) : "∞"}</Box>
              </SpaceBetween>
              <SpaceBetween size="xs">
                <Box variant="awsui-key-label">{t("user.plan.expiresAt")}</Box>
                <Box>
                  {profile.plan_expires_at
                    ? new Date(profile.plan_expires_at).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language)
                    : "-"}
                </Box>
              </SpaceBetween>
              <SpaceBetween size="xs">
                <Box variant="awsui-key-label">{t("user.plan.status")}</Box>
                <StatusIndicator type={profile.status === "active" ? "success" : "warning"}>
                  {t(STATUS_LABEL_KEYS[profile.status] ?? "user.dashboard.statusUnknown")}
                </StatusIndicator>
              </SpaceBetween>
            </ColumnLayout>
          </Container>
        )}

        {!hasPlan && (
          <Alert type={isExpired ? "warning" : "info"} header={isExpired ? t("user.plan.expired") : t("user.plan.noPlan")}>
            {t("user.plan.noPlanHint")}
          </Alert>
        )}

        {pendingOrder && (
          <Container>
            <SpaceBetween size="s">
              <StatusIndicator type="pending">
                {t("user.plan.order.pendingBanner", {
                  plan: pendingOrder.plan_name,
                  days: pendingOrder.duration_days,
                  price: formatPriceCents(pendingOrder.price_cents),
                })}
              </StatusIndicator>
              {(pendingOrder.discount_cents ?? 0) > 0 && (
                <SpaceBetween direction="horizontal" size="xs" alignItems="center">
                  <Box variant="awsui-key-label">{t("user.plan.promotion.discountApplied")}</Box>
                  <Box color="text-status-inactive">
                    <s>{formatPriceCents(pendingOrder.price_cents + (pendingOrder.discount_cents ?? 0))}</s>
                  </Box>
                  <Badge color="green">{formatPriceCents(pendingOrder.price_cents)}</Badge>
                </SpaceBetween>
              )}
              <SpaceBetween direction="horizontal" size="xs">
                {providers.length > 0 && (
                  <Button variant="primary" onClick={() => openOrderCheckout(pendingOrder)}>
                    {t("user.plan.order.checkout.payNow")}
                  </Button>
                )}
                <Button onClick={() => setCancelModal(pendingOrder)}>{t("user.plan.order.cancel")}</Button>
              </SpaceBetween>
            </SpaceBetween>
          </Container>
        )}

        <SpaceBetween size="m">
          <Header variant="h2" counter={plans.length > 0 ? `(${plans.length})` : undefined}>{t("user.plan.availablePlans")}</Header>
          {plans.length === 0 ? (
            <Container>
              <Box textAlign="center" color="text-body-secondary" padding="l">{t("user.plan.noPlansAvailable")}</Box>
            </Container>
          ) : (
            <div className="plan-info__grid">
              {plans.map((item) => {
                const selectedPrice =
                  item.prices.find((price) => price.duration_days === selectedDurations[item.id]) ?? item.prices[0];
                const savings = selectedPrice ? planSavings(item.prices, selectedPrice.duration_days) : null;
                const stats = [
                  { label: t("user.plan.trafficLimit"), value: item.traffic_limit ? formatBytes(item.traffic_limit) : t("user.plan.unlimited") },
                  { label: t("user.plan.speedLimit"), value: item.speed_limit ? `${item.speed_limit} Mbps` : t("user.plan.unlimited") },
                  { label: t("user.plan.maxDevices"), value: String(item.max_devices) },
                ];
                return (
                  <Container key={item.id} fitHeight header={<Header variant="h2">{item.name}</Header>}>
                    <div className="plan-info__card">
                      {selectedPrice ? (
                        <Box textAlign="center">
                          <Box tagOverride="span" fontSize="display-l" fontWeight="bold">{formatPriceCents(selectedPrice.price_cents)}</Box>
                          <Box tagOverride="span" color="text-body-secondary">
                            {" "}{t("user.plan.perDays", { days: selectedPrice.duration_days })}
                          </Box>
                          {savings && (
                            <Box margin={{ top: "xxs" }}><Badge color="green">{t("user.plan.order.savings", { percent: savings.percent })}</Badge></Box>
                          )}
                        </Box>
                      ) : null}

                      {item.purchasable && selectedPrice && item.prices.length > 1 && (
                        <FormField label={t("user.plan.order.chooseDuration")} stretch>
                          <div
                            className="plan-info__durations"
                            role="radiogroup"
                            aria-label={t("user.plan.order.chooseDuration")}
                            style={{ gridTemplateColumns: `repeat(${item.prices.length <= 4 ? item.prices.length : 3}, minmax(0, 1fr))` }}
                          >
                            {item.prices.map((price) => {
                              const selected = price.duration_days === selectedPrice.duration_days;
                              const priceSavings = planSavings(item.prices, price.duration_days);
                              return (
                                <button
                                  key={price.duration_days}
                                  type="button"
                                  role="radio"
                                  aria-checked={selected}
                                  className="plan-info__duration"
                                  disabled={submitting || pendingOrder !== null}
                                  style={{
                                    borderColor: selected ? colorBorderItemSelected : colorBorderInputDefault,
                                    background: selected ? colorBackgroundItemSelected : colorBackgroundInputDefault,
                                    boxShadow: selected ? `inset 0 0 0 1px ${colorBorderItemSelected}` : undefined,
                                    borderRadius: borderRadiusInput,
                                  }}
                                  onClick={() =>
                                    setSelectedDurations((current) => ({ ...current, [item.id]: price.duration_days }))
                                  }
                                >
                                  <Box fontWeight="bold" fontSize="body-s">{t("user.plan.daysLabel", { days: price.duration_days })}</Box>
                                  <Box fontSize="body-s">{formatPriceCents(price.price_cents)}</Box>
                                  {priceSavings && (
                                    <Box fontSize="body-s" color="text-status-success">
                                      {t("user.plan.savePercent", { percent: priceSavings.percent })}
                                    </Box>
                                  )}
                                </button>
                              );
                            })}
                          </div>
                        </FormField>
                      )}

                      {item.features.length > 0 && (
                        <div className="plan-info__divided" style={{ borderBlockStart: `1px solid ${colorBorderDividerDefault}` }}>
                          <SpaceBetween size="xs">
                            {item.features.map((feature, index) => (
                              <div key={index} className="plan-info__feature">
                                <Icon
                                  name={feature.included ? "status-positive" : "status-negative"}
                                  variant={feature.included ? "success" : "disabled"}
                                />
                                <Box color={feature.included ? undefined : "text-status-inactive"}>{feature.text}</Box>
                              </div>
                            ))}
                          </SpaceBetween>
                        </div>
                      )}

                      <div className="plan-info__stats">
                        {stats.map((stat) => (
                          <div key={stat.label} className="plan-info__stat"
                            style={{ background: colorBackgroundCellShaded, borderRadius: borderRadiusItem }}>
                            <Box fontWeight="bold" fontSize="heading-s">{stat.value}</Box>
                            <Box variant="small" color="text-body-secondary">{stat.label}</Box>
                          </div>
                        ))}
                      </div>

                      <div className="plan-info__action">
                        {!item.purchasable || !selectedPrice ? (
                          <StatusIndicator type="info">{t("user.plan.order.notPurchasable")}</StatusIndicator>
                        ) : (
                          <SpaceBetween size="xs">
                            <Button
                              variant="primary"
                              fullWidth
                              iconName="add-plus"
                              ariaLabel={`${t("user.plan.order.placeOrder")} ${item.name}`}
                              disabled={submitting || pendingOrder !== null}
                              onClick={() =>
                                openCheckout({
                                  planId: item.id,
                                  planName: item.name,
                                  durationDays: selectedPrice.duration_days,
                                  priceCents: selectedPrice.price_cents,
                                  discountCents: 0,
                                  order: null,
                                })
                              }
                            >
                              {t("user.plan.order.placeOrder")}
                            </Button>
                            {pendingOrder && (
                              <Box variant="small" color="text-status-inactive" textAlign="center">
                                {t("user.plan.order.blockedByPending")}
                              </Box>
                            )}
                          </SpaceBetween>
                        )}
                      </div>
                    </div>
                  </Container>
                );
              })}
            </div>
          )}
        </SpaceBetween>

        {orders.length > 0 && (
          <Table
            items={orders}
            trackBy="id"
            header={
              <Header variant="h2" counter={`(${orders.length})`}>
                {t("user.plan.order.myOrders")}
              </Header>
            }
            columnDefinitions={[
              {
                id: "plan",
                header: t("user.plan.order.col.plan"),
                cell: (item) => item.plan_name,
              },
              {
                id: "duration",
                header: t("user.plan.order.col.duration"),
                cell: (item) => `${item.duration_days} ${t("user.plan.durationDays")}`,
              },
              {
                id: "price",
                header: t("user.plan.order.col.price"),
                cell: (item) =>
                  (item.discount_cents ?? 0) > 0 ? (
                    <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
                      <Box color="text-status-inactive">
                        <s>{formatPriceCents(item.price_cents + (item.discount_cents ?? 0))}</s>
                      </Box>
                      <Box>{formatPriceCents(item.price_cents)}</Box>
                    </SpaceBetween>
                  ) : (
                    formatPriceCents(item.price_cents)
                  ),
              },
              {
                id: "status",
                header: t("user.plan.order.col.status"),
                cell: (item) => (
                  <StatusIndicator type={orderStatusIndicatorType(item.status)}>
                    {t(`user.plan.order.status.${item.status}`)}
                  </StatusIndicator>
                ),
              },
              {
                id: "createdAt",
                header: t("user.plan.order.col.createdAt"),
                cell: (item) => new Date(item.created_at).toLocaleString(),
              },
              {
                id: "actions",
                header: t("user.plan.order.col.actions"),
                cell: (item) =>
                  item.status === "pending" ? (
                    <SpaceBetween direction="horizontal" size="xs">
                      {providers.length > 0 && (
                        <Button variant="inline-link" onClick={() => openOrderCheckout(item)}>
                          {t("user.plan.order.checkout.payNow")}
                        </Button>
                      )}
                      <Button variant="inline-link" onClick={() => setCancelModal(item)}>
                        {t("user.plan.order.cancel")}
                      </Button>
                    </SpaceBetween>
                  ) : item.status === "paid" ? (
                    <Button
                      variant="inline-link"
                      loading={receiptLoading === item.id}
                      disabled={receiptLoading !== null}
                      onClick={() => void handleViewReceipt(item.id)}
                    >
                      {t("user.plan.order.receipt.view")}
                    </Button>
                  ) : (
                    <Box color="text-status-inactive">—</Box>
                  ),
              },
            ]}
            empty={<Box textAlign="center">{t("user.plan.order.noOrders")}</Box>}
          />
        )}

        <Modal
          size="medium"
          closeAriaLabel={t("user.announcements.close")}
          visible={checkout !== null}
          onDismiss={() => {
            if (!submitting) closeCheckout();
          }}
          header={t("user.plan.checkoutModal.title")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button disabled={submitting} onClick={closeCheckout}>
                  {t("user.plan.order.dismiss")}
                </Button>
                <Button
                  variant="primary"
                  loading={submitting}
                  disabled={!providerName || (checkout?.order === null && pendingOrder !== null)}
                  onClick={() => void handleConfirmCheckout()}
                >
                  {t("user.plan.checkoutModal.confirm")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {checkout && (
            <SpaceBetween size="l">
              <div className="plan-info__summary">
                <SpaceBetween size="xxs">
                  <Box variant="awsui-key-label">{t("user.plan.planName")}</Box>
                  <Box>{checkout.planName}</Box>
                </SpaceBetween>
                <SpaceBetween size="xxs">
                  <Box variant="awsui-key-label">{t("user.plan.duration")}</Box>
                  <Box>{t("user.plan.daysLabel", { days: checkout.durationDays })}</Box>
                </SpaceBetween>
                <SpaceBetween size="xxs">
                  <Box variant="awsui-key-label">{t("user.plan.order.col.price")}</Box>
                  <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
                    {checkout.discountCents > 0 && (
                      <Box color="text-status-inactive"><s>{formatPriceCents(checkout.priceCents + checkout.discountCents)}</s></Box>
                    )}
                    <Box fontWeight="bold">{formatPriceCents(checkout.priceCents)}</Box>
                  </SpaceBetween>
                </SpaceBetween>
              </div>

              <FormField label={t("user.plan.checkoutModal.provider")} stretch>
                {providers.length === 0 ? (
                  <Alert type="warning">{t("user.plan.checkoutModal.noProviders")}</Alert>
                ) : (
                  <Tiles
                    columns={Math.min(2, providers.length)}
                    value={providerName ?? null}
                    onChange={({ detail }) => setProviderName(detail.value)}
                    items={[...providers].sort((a, b) => Number(b.mode === "redirect") - Number(a.mode === "redirect")).map((provider) => ({
                      value: provider.name,
                      label: providerLabel(provider.name),
                      description: providerHint(provider.name),
                      disabled: submitting,
                    }))}
                  />
                )}
              </FormField>

              <FormField
                label={t("user.plan.promotion.label")}
                constraintText={t("user.plan.checkoutModal.promoOptional")}
                description={checkout.order ? t("user.plan.promotion.pendingHint") : undefined}
                stretch
              >
                <Input
                  value={promotionCode}
                  disabled={submitting}
                  placeholder={t("user.plan.promotion.placeholder")}
                  onChange={({ detail }) => setPromotionCode(detail.value)}
                />
              </FormField>

              {checkout.order === null && pendingOrder && <Alert type="warning">{t("user.plan.order.blockedByPending")}</Alert>}
              {checkoutError && <Alert type="error">{checkoutError}</Alert>}
            </SpaceBetween>
          )}
        </Modal>

        <Modal
          visible={receipt !== null}
          onDismiss={() => setReceipt(null)}
          closeAriaLabel={t("user.announcements.close")}
          header={t("user.plan.order.receipt.title")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setReceipt(null)}>{t("user.plan.order.dismiss")}</Button>
                {receipt?.receipt_url && (
                  <Button
                    variant="primary"
                    href={receipt.receipt_url}
                    target="_blank"
                    iconName="external"
                    iconAlign="right"
                  >
                    {t("user.plan.order.receipt.openStripe")}
                  </Button>
                )}
              </SpaceBetween>
            </Box>
          }
        >
          {receipt && (
            <ColumnLayout columns={2} variant="text-grid">
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.order.receipt.orderId")}</Box>
                <Box variant="code">{receipt.order_id}</Box>
              </SpaceBetween>
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.order.receipt.paidAt")}</Box>
                <Box>{receipt.paid_at ? new Date(receipt.paid_at).toLocaleString(i18n.resolvedLanguage ?? i18n.language) : "-"}</Box>
              </SpaceBetween>
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.planName")}</Box>
                <Box>{receipt.plan_name}</Box>
              </SpaceBetween>
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.duration")}</Box>
                <Box>{receipt.duration_days} {t("user.plan.durationDays")}</Box>
              </SpaceBetween>
              {receipt.discount_cents > 0 && (
                <SpaceBetween size="xxs">
                  <Box variant="awsui-key-label">{t("user.plan.promotion.discountApplied")}</Box>
                  <Box>-{formatPriceCents(receipt.discount_cents)}</Box>
                </SpaceBetween>
              )}
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.order.receipt.total")}</Box>
                <Box fontWeight="bold">{formatPriceCents(receipt.price_cents)}</Box>
              </SpaceBetween>
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.order.receipt.email")}</Box>
                <Box>{receipt.email}</Box>
              </SpaceBetween>
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.order.receipt.method")}</Box>
                <Box>{receipt.provider === "stripe" ? "Stripe" : t("user.plan.order.receipt.methodPanel")}</Box>
              </SpaceBetween>
            </ColumnLayout>
          )}
        </Modal>

        <Modal
          visible={cancelModal !== null}
          onDismiss={() => setCancelModal(null)}
          header={t("user.plan.order.cancelTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setCancelModal(null)}>{t("user.plan.order.dismiss")}</Button>
                <Button variant="primary" loading={submitting} onClick={() => void handleCancelOrder()}>
                  {t("user.plan.order.confirmCancel")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("user.plan.order.cancelMessage", {
            plan: cancelModal?.plan_name,
            days: cancelModal?.duration_days,
          })}
        </Modal>
      </SpaceBetween>
      </div>
    </ContentLayout>
  );
}
