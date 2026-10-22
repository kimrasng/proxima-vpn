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
import { borderRadiusItem, colorBackgroundCellShaded, colorBorderDividerDefault } from "@cloudscape-design/design-tokens";
import "./planInfo.css";
import { focusFirstInvalid } from "../../utils/formValidation";
import type {
  UserProfile,
  UserPlanItem,
  UserPlanPrice,
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
  const [receipt, setReceipt] = useState<OrderReceipt | null>(null);
  const [receiptLoading, setReceiptLoading] = useState<string | null>(null);
  const [selectedDurations, setSelectedDurations] = useState<Record<string, number>>({});
  const [orderReview, setOrderReview] = useState<{ plan: UserPlanItem; price: UserPlanPrice } | null>(null);
  const [orderReviewError, setOrderReviewError] = useState<string | null>(null);
  const [orderingPlanId, setOrderingPlanId] = useState<string | null>(null);
  const [cancelModal, setCancelModal] = useState<OrderResponse | null>(null);
  const [promotionCode, setPromotionCode] = useState("");
  const [promotionLoading, setPromotionLoading] = useState(false);
  const [showPromotionError, setShowPromotionError] = useState(false);

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
      } else if (result.order.status === "pending" && redirectProvider) {
        // Place order goes straight to hosted checkout; the order already
        // exists, so a failed handoff leaves it payable from My Orders.
        setOrderReview(null);
        setPromotionCode("");
        const redirected = await redirectToCheckout(result.order.id);
        if (redirected) return;
        await loadData();
        return;
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
    setShowPromotionError(true);
    if (!code) { focusFirstInvalid(); return; }
    try {
      setPromotionLoading(true);
      await userApi.applyPromotionToOrder(orderId, code);
      setFlash([{ type: "success", content: t("user.plan.promotion.applied"), dismissible: true, onDismiss: () => setFlash([]) }]);
      setPromotionCode("");
      setShowPromotionError(false);
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

  // Returns true once the browser is leaving for the hosted checkout page.
  const redirectToCheckout = async (orderId: string): Promise<boolean> => {
    if (!redirectProvider) return false;
    try {
      setCheckoutLoading(orderId);
      const session = await userApi.startCheckout(orderId, redirectProvider.name);
      if (!session.redirect_url) {
        setFlash([{ type: "error", content: t("user.plan.order.checkout.error"), dismissible: true, onDismiss: () => setFlash([]) }]);
        return false;
      }
      // Checkout lives on Stripe's domain, so this must leave the SPA entirely.
      window.location.href = session.redirect_url;
      return true;
    } catch {
      setFlash([{ type: "error", content: t("user.plan.order.checkout.error"), dismissible: true, onDismiss: () => setFlash([]) }]);
      return false;
    } finally {
      setCheckoutLoading(null);
    }
  };

  const handleStartCheckout = async (orderId: string) => {
    await redirectToCheckout(orderId);
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
          <div className="plan-info__top">
            <Container fitHeight>
              <SpaceBetween size="xs">
                <StatusIndicator type={isExpired ? "warning" : "info"}>
                  <Box variant="strong" tagOverride="span">{isExpired ? t("user.plan.expired") : t("user.plan.noPlan")}</Box>
                </StatusIndicator>
                <Box color="text-body-secondary">{t("user.plan.noPlanHint")}</Box>
              </SpaceBetween>
            </Container>
            {plans.length > 0 && !pendingOrder && (
              <Container fitHeight>
                <FormField label={t("user.plan.promotion.label")} description={t("user.plan.promotion.beforeOrderHint")} stretch>
                  <Input
                    value={promotionCode}
                    placeholder={t("user.plan.promotion.placeholder")}
                    onChange={({ detail }) => setPromotionCode(detail.value)}
                  />
                </FormField>
              </Container>
            )}
          </div>
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
                errorText={showPromotionError && !promotionCode.trim() ? t("user.plan.promotion.codeRequired") : undefined}
              >
                <SpaceBetween direction="horizontal" size="xs">
                  <Input
                    value={promotionCode}
                    placeholder={t("user.plan.promotion.placeholder")}
                    onChange={({ detail }) => setPromotionCode(detail.value)}
                  />
                  <Button
                    loading={promotionLoading}
                    disabled={promotionLoading}
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

        {hasPlan && plans.length > 0 && !pendingOrder && (
          <Container>
            <FormField label={t("user.plan.promotion.label")} description={t("user.plan.promotion.beforeOrderHint")}>
              <Input
                value={promotionCode}
                placeholder={t("user.plan.promotion.placeholder")}
                onChange={({ detail }) => setPromotionCode(detail.value)}
              />
            </FormField>
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

                      {item.purchasable && item.prices.length > 0 && (
                        <FormField label={t("user.plan.order.chooseDuration")} stretch>
                          <Tiles
                            columns={Math.min(3, item.prices.length)}
                            value={selectedPrice ? String(selectedPrice.duration_days) : null}
                            onChange={({ detail }) =>
                              setSelectedDurations((current) => ({ ...current, [item.id]: Number(detail.value) }))
                            }
                            items={item.prices.map((price) => {
                              const priceSavings = planSavings(item.prices, price.duration_days);
                              return {
                                value: String(price.duration_days),
                                label: t("user.plan.daysLabel", { days: price.duration_days }),
                                description: priceSavings
                                  ? `${formatPriceCents(price.price_cents)} · ${t("user.plan.order.savings", { percent: priceSavings.percent })}`
                                  : formatPriceCents(price.price_cents),
                                disabled: submitting || pendingOrder !== null,
                              };
                            })}
                          />
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
    </ContentLayout>
  );
}
