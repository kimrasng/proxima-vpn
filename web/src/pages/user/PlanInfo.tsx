import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Container,
  SpaceBetween,
  Box,
  Cards,
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
  Select,
  Alert,
} from "@cloudscape-design/components";
import type {
  UserProfile,
  UserPlanItem,
  UserPlanPrice,
  OrderResponse,
  OrderStatus,
  PaymentProviderItem,
  PromotionRejectReason,
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
  const [checkoutLoading, setCheckoutLoading] = useState<string | null>(null);
  const [selectedDurations, setSelectedDurations] = useState<Record<string, number>>({});
  const [orderReview, setOrderReview] = useState<{ plan: UserPlanItem; price: UserPlanPrice } | null>(null);
  const [orderReviewError, setOrderReviewError] = useState<string | null>(null);
  const [orderingPlanId, setOrderingPlanId] = useState<string | null>(null);
  const [cancelModal, setCancelModal] = useState<OrderResponse | null>(null);
  const [promotionCode, setPromotionCode] = useState("");
  const [promotionLoading, setPromotionLoading] = useState(false);

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

  const showRejection = (reason: PromotionRejectReason) => {
    setFlash([
      {
        type: "error",
        content: t(`user.plan.promotion.rejected.${reason}`),
        dismissible: true,
        onDismiss: () => setFlash([]),
      },
    ]);
  };

  const handleOrder = async (planId: string, durationDays: number) => {
    if (submitting || pendingOrder) return;
    const code = promotionCode.trim();
    try {
      setSubmitting(true);
      setOrderingPlanId(planId);
      setOrderReviewError(null);
      const result = await userApi.createOrder({
        plan_id: planId,
        duration_days: durationDays,
        ...(code ? { promotion_code: code } : {}),
      });
      if (result.promotion_rejected) {
        showRejection(result.promotion_rejected);
      } else {
        setFlash([{ type: "success", content: t("user.plan.order.placed"), dismissible: true, onDismiss: () => setFlash([]) }]);
        setPromotionCode("");
      }
      setOrderReview(null);
      await loadData();
    } catch (err) {
      // 409 is the one-pending-order rule, not a failure worth a generic message.
      const content =
        err instanceof ApiError && err.status === 409
          ? t("user.plan.order.conflict")
          : t("user.plan.order.error");
      setFlash([{ type: "error", content, dismissible: true, onDismiss: () => setFlash([]) }]);
      setOrderReviewError(content);
    } finally {
      setSubmitting(false);
      setOrderingPlanId(null);
    }
  };

  const handleApplyPromotion = async (orderId: string) => {
    const code = promotionCode.trim();
    if (!code) return;
    try {
      setPromotionLoading(true);
      await userApi.applyPromotionToOrder(orderId, code);
      setFlash([{ type: "success", content: t("user.plan.promotion.applied"), dismissible: true, onDismiss: () => setFlash([]) }]);
      setPromotionCode("");
      await loadData();
    } catch (err) {
      const reason = rejectionFromError(err);
      if (reason) {
        showRejection(reason);
      } else {
        setFlash([{ type: "error", content: t("user.plan.promotion.error"), dismissible: true, onDismiss: () => setFlash([]) }]);
      }
    } finally {
      setPromotionLoading(false);
    }
  };

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

  // At most one redirect provider exists today (stripe), so the button needs no picker.
  const redirectProvider = providers.find((p) => p.mode === "redirect") ?? null;

  const handleStartCheckout = async (orderId: string) => {
    if (!redirectProvider) return;
    try {
      setCheckoutLoading(orderId);
      const session = await userApi.startCheckout(orderId, redirectProvider.name);
      if (!session.redirect_url) {
        setFlash([{ type: "error", content: t("user.plan.order.checkout.error"), dismissible: true, onDismiss: () => setFlash([]) }]);
        return;
      }
      // Checkout lives on Stripe's domain, so this must leave the SPA entirely.
      window.location.href = session.redirect_url;
    } catch {
      setFlash([{ type: "error", content: t("user.plan.order.checkout.error"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setCheckoutLoading(null);
    }
  };

  const pendingOrder = orders.find((o) => o.status === "pending") ?? null;

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.plan.title")}</Header>}>
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
    <ContentLayout header={<Header variant="h1">{t("user.plan.title")}</Header>}>
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
          <Container>
            <Box textAlign="center" padding="l">
              <StatusIndicator type="warning">
                {isExpired ? t("user.plan.expired") : t("user.plan.noPlan")}
              </StatusIndicator>
            </Box>
          </Container>
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
                    <s>
                      {formatPriceCents(
                        pendingOrder.price_cents + (pendingOrder.discount_cents ?? 0),
                      )}
                    </s>
                  </Box>
                  <Badge color="green">{formatPriceCents(pendingOrder.price_cents)}</Badge>
                  <Box variant="small" color="text-body-secondary">
                    {t("user.plan.promotion.savedAmount", {
                      amount: formatPriceCents(pendingOrder.discount_cents ?? 0),
                    })}
                  </Box>
                </SpaceBetween>
              )}
              <FormField
                label={t("user.plan.promotion.label")}
                description={t("user.plan.promotion.pendingHint")}
              >
                <SpaceBetween direction="horizontal" size="xs">
                  <Input
                    value={promotionCode}
                    placeholder={t("user.plan.promotion.placeholder")}
                    onChange={({ detail }) => setPromotionCode(detail.value)}
                  />
                  <Button
                    loading={promotionLoading}
                    disabled={promotionCode.trim().length === 0}
                    onClick={() => void handleApplyPromotion(pendingOrder.id)}
                  >
                    {t("user.plan.promotion.apply")}
                  </Button>
                </SpaceBetween>
              </FormField>
              {redirectProvider && (
                <SpaceBetween size="xxs">
                  <Button
                    variant="primary"
                    loading={checkoutLoading === pendingOrder.id}
                    disabled={checkoutLoading !== null}
                    onClick={() => void handleStartCheckout(pendingOrder.id)}
                  >
                    {t("user.plan.order.checkout.payNow")}
                  </Button>
                  <Box variant="small" color="text-body-secondary">
                    {t("user.plan.order.checkout.hint")}
                  </Box>
                </SpaceBetween>
              )}
            </SpaceBetween>
          </Container>
        )}

        {plans.length > 0 && !pendingOrder && (
          <Container header={<Header variant="h3">{t("user.plan.promotion.label")}</Header>}>
            <FormField description={t("user.plan.promotion.beforeOrderHint")}>
              <Input
                value={promotionCode}
                placeholder={t("user.plan.promotion.placeholder")}
                onChange={({ detail }) => setPromotionCode(detail.value)}
              />
            </FormField>
          </Container>
        )}

        {plans.length > 0 && (
          <Cards
            cardsPerRow={[{ cards: 1 }, { cards: 2, minWidth: 700 }]}
            cardDefinition={{
              header: (item) => item.name,
              sections: [
                {
                  id: "traffic",
                  header: t("user.plan.trafficLimit"),
                  content: (item) => item.traffic_limit ? formatBytes(item.traffic_limit) : "∞",
                },
                {
                  id: "devices",
                  header: t("user.plan.maxDevices"),
                  content: (item) => String(item.max_devices),
                },
                {
                  id: "speed",
                  header: t("user.plan.speedLimit"),
                  content: (item) => item.speed_limit ? `${item.speed_limit} Mbps` : "∞",
                },
                {
                  id: "features",
                  header: t("user.plan.features.title"),
                  content: (item) =>
                    item.features.length === 0 ? (
                      <Box color="text-status-inactive">—</Box>
                    ) : (
                      <SpaceBetween size="xxs">
                        {item.features.map((feature, index) => (
                          <SpaceBetween key={index} direction="horizontal" size="xs" alignItems="center">
                            <Icon
                              name={feature.included ? "status-positive" : "status-negative"}
                              variant={feature.included ? "success" : "disabled"}
                            />
                            <Box color={feature.included ? undefined : "text-status-inactive"}>
                              {feature.text}
                            </Box>
                          </SpaceBetween>
                        ))}
                      </SpaceBetween>
                    ),
                },
                {
                  id: "action",
                  content: (item) => {
                    const selectedPrice =
                      item.prices.find((price) => price.duration_days === selectedDurations[item.id]) ??
                      item.prices[0];
                    if (!item.purchasable || !selectedPrice) {
                      return (
                        <StatusIndicator type="info">
                          {t("user.plan.order.notPurchasable")}
                        </StatusIndicator>
                      );
                    }
                    const durationOptions = item.prices.map((price) => ({
                      value: String(price.duration_days),
                      label: t("user.plan.order.durationPrice", {
                        days: price.duration_days,
                        price: formatPriceCents(price.price_cents),
                      }),
                    }));
                    const savings = planSavings(item.prices, selectedPrice.duration_days);
                    return (
                      <SpaceBetween size="s">
                        <FormField label={t("user.plan.order.chooseDuration")}>
                          <Select
                            selectedOption={durationOptions.find(
                              (option) => option.value === String(selectedPrice.duration_days),
                            ) ?? null}
                            options={durationOptions}
                            disabled={submitting || pendingOrder !== null}
                            onChange={({ detail }) => {
                              if (detail.selectedOption.value) {
                                const durationDays = Number(detail.selectedOption.value);
                                setSelectedDurations((current) => ({
                                  ...current,
                                  [item.id]: durationDays,
                                }));
                              }
                            }}
                          />
                        </FormField>
                        <SpaceBetween size="xxs">
                          <Box variant="awsui-key-label">{t("user.plan.order.col.price")}</Box>
                          <Box>
                            {t("user.plan.order.durationPrice", {
                              days: selectedPrice.duration_days,
                              price: formatPriceCents(selectedPrice.price_cents),
                            })}
                          </Box>
                          {savings && (
                            <Badge color="green">
                              {t("user.plan.order.savings", { percent: savings.percent })}
                            </Badge>
                          )}
                        </SpaceBetween>
                        <Button
                          variant="primary"
                          ariaLabel={`${t("user.plan.order.placeOrder")} ${item.name}`}
                          loading={orderingPlanId === item.id}
                          disabled={submitting || pendingOrder !== null}
                          onClick={() => {
                            setOrderReviewError(null);
                            setOrderReview({ plan: item, price: selectedPrice });
                          }}
                        >
                          {t("user.plan.order.placeOrder")}
                        </Button>
                        {pendingOrder && (
                          <Box variant="small" color="text-status-inactive">
                            {t("user.plan.order.blockedByPending")}
                          </Box>
                        )}
                      </SpaceBetween>
                    );
                  },
                },
              ],
            }}
            items={plans}
            header={<Header variant="h2">{t("user.plan.availablePlans")}</Header>}
          />
        )}

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
                      {redirectProvider && (
                        <Button
                          variant="inline-link"
                          loading={checkoutLoading === item.id}
                          disabled={checkoutLoading !== null}
                          onClick={() => void handleStartCheckout(item.id)}
                        >
                          {t("user.plan.order.checkout.payNow")}
                        </Button>
                      )}
                      <Button variant="inline-link" onClick={() => setCancelModal(item)}>
                        {t("user.plan.order.cancel")}
                      </Button>
                    </SpaceBetween>
                  ) : (
                    <Box color="text-status-inactive">—</Box>
                  ),
              },
            ]}
            empty={<Box textAlign="center">{t("user.plan.order.noOrders")}</Box>}
          />
        )}

        <Modal
          size="small"
          closeAriaLabel={t("user.announcements.close")}
          visible={orderReview !== null}
          onDismiss={() => {
            if (!submitting) setOrderReview(null);
          }}
          header={t("user.plan.order.reviewTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button disabled={submitting} onClick={() => setOrderReview(null)}>
                  {t("user.plan.order.dismiss")}
                </Button>
                <Button
                  variant="primary"
                  loading={orderingPlanId !== null}
                  disabled={submitting || pendingOrder !== null}
                  onClick={() => {
                    if (orderReview) {
                      void handleOrder(orderReview.plan.id, orderReview.price.duration_days);
                    }
                  }}
                >
                  {t("user.plan.order.confirmOrder")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {orderReview && (
            <SpaceBetween size="s">
              <Box>{t("user.plan.order.reviewHint")}</Box>
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.planName")}</Box>
                <Box>{orderReview.plan.name}</Box>
              </SpaceBetween>
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.duration")}</Box>
                <Box>{orderReview.price.duration_days} {t("user.plan.durationDays")}</Box>
              </SpaceBetween>
              <SpaceBetween size="xxs">
                <Box variant="awsui-key-label">{t("user.plan.order.col.price")}</Box>
                <Box>{formatPriceCents(orderReview.price.price_cents)}</Box>
              </SpaceBetween>
              {promotionCode.trim() && (
                <SpaceBetween size="xxs">
                  <Box variant="awsui-key-label">{t("user.plan.promotion.label")}</Box>
                  <Box>{promotionCode.trim()}</Box>
                </SpaceBetween>
              )}
              {pendingOrder && <Alert type="warning">{t("user.plan.order.blockedByPending")}</Alert>}
              {orderReviewError && <Alert type="error">{orderReviewError}</Alert>}
            </SpaceBetween>
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
    </ContentLayout>
  );
}
