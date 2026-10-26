import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
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
  Badge,
  Icon,
  Input,
  FormField,
  Modal,
  Alert,
  Tiles,
  ProgressBar,
} from "@cloudscape-design/components";
import {
  borderRadiusInput, borderRadiusItem, colorBackgroundCellShaded, colorBackgroundInputDefault,
  colorBackgroundItemSelected, colorBorderDividerDefault, colorBorderInputDefault, colorBorderItemSelected,
} from "@cloudscape-design/design-tokens";
import "./planInfo.css";
import ResetTickets from "../../components/ResetTickets";
import type {
  UserProfile,
  UserPlanItem,
  OrderResponse,
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
  const value = bytes / Math.pow(1024, i);
  return `${Number.isInteger(value) ? value : value.toFixed(1)} ${units[i]}`;
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
  const navigate = useNavigate();
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [plans, setPlans] = useState<UserPlanItem[]>([]);
  const [orders, setOrders] = useState<OrderResponse[]>([]);
  const [providers, setProviders] = useState<PaymentProviderItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [selectedDurations, setSelectedDurations] = useState<Record<string, number>>({});
  const [checkout, setCheckout] = useState<CheckoutTarget | null>(null);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);
  const [providerName, setProviderName] = useState<string | undefined>();
  const [cancelModal, setCancelModal] = useState<OrderResponse | null>(null);
  const [promotionCode, setPromotionCode] = useState("");
  const [appliedCode, setAppliedCode] = useState<string | null>(null);
  const [applying, setApplying] = useState(false);
  // After a manual (bank transfer) order the modal stays open on a receipt
  // instead of collapsing into a page banner, so the user sees what was ordered.
  const [placedOrder, setPlacedOrder] = useState<OrderResponse | null>(null);
  const [stripeTabBlocked, setStripeTabBlocked] = useState<string | null>(null);
  // After Stripe opens in its own tab the payment is out-of-band, so the user
  // watches a persistent "payment in progress" popup here instead of a
  // transient toast; it can reopen the tab and closes into a fresh reload.
  const [paying, setPaying] = useState<{ url: string; planName: string; priceCents: number } | null>(null);
  // Coming back from Stripe deserves its own popup (a receipt-style result),
  // not a transient toast the user can miss before it fades.
  type CheckoutReturn =
    | { kind: "confirming" }
    | { kind: "paid" | "extension"; planName: string; days: number; until: string }
    | { kind: "pending" | "cancelled" | "failed" };
  const [checkoutReturn, setCheckoutReturn] = useState<CheckoutReturn | null>(null);
  const returnGeneration = useRef(0);

  const loadData = async (): Promise<OrderResponse[] | null> => {
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
      // Manual bank transfer is hidden from customers for now; only hosted
      // checkout (Stripe) is offered. Admins still settle orders manually.
      setProviders(providerList.filter((provider) => provider.mode === "redirect"));
      return orderList;
    } catch {
      setFlash([{ type: "error", content: t("user.plan.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
      return null;
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [i18n.language]);

  // Stripe sends the browser back here. The webhook may never reach this
  // server (a local or firewalled panel), so the return itself asks the API to
  // read the session back from Stripe and settle the order if it is paid.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const checkoutResult = params.get("checkout");
    if (checkoutResult !== "success" && checkoutResult !== "cancelled") return;
    const orderId = params.get("order_id");
    const sessionId = params.get("session_id");
    window.history.replaceState({}, "", window.location.pathname);
    if (checkoutResult === "cancelled") {
      setCheckoutReturn({ kind: "cancelled" });
      return;
    }
    if (!orderId || !sessionId) {
      setCheckoutReturn({ kind: "pending" });
      return;
    }
    const generation = ++returnGeneration.current;
    setCheckoutReturn({ kind: "confirming" });
    void (async () => {
      try {
        const result = await userApi.confirmCheckout(orderId, sessionId);
        if (returnGeneration.current !== generation) return;
        if (result.status === "paid") {
          // An extension keeps plan_started_at; a new term starts at the grant.
          // Comparing it to the order's own time stays right even when the
          // webhook settled the order before this page loaded.
          const [after, orderList] = await Promise.all([userApi.getProfile(), userApi.listMyOrders()]);
          const order = orderList.find((item) => item.id === orderId);
          const wasActive = !!after.plan_started_at && !!order && new Date(after.plan_started_at) < new Date(order.created_at);
          setCheckoutReturn({
            kind: wasActive ? "extension" : "paid",
            planName: order?.plan_name ?? "",
            days: order?.duration_days ?? 0,
            until: after.plan_expires_at ?? "",
          });
        } else {
          setCheckoutReturn({ kind: "pending" });
        }
      } catch {
        if (returnGeneration.current === generation) setCheckoutReturn({ kind: "failed" });
      } finally {
        await loadData();
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Stripe runs in another tab, so this tab learns about the payment only when
  // the user comes back to it.
  useEffect(() => {
    const onFocus = () => {
      if (document.visibilityState === "visible" && orders.some((order) => order.status === "pending")) {
        void (async () => {
          const list = await loadData();
          // Payment finished in the Stripe tab: the waiting popup has served its
          // purpose, so it closes itself instead of asking to be dismissed.
          if (list && !list.some((order) => order.status === "pending")) setPaying(null);
        })();
      }
    };
    document.addEventListener("visibilitychange", onFocus);
    return () => document.removeEventListener("visibilitychange", onFocus);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orders]);

  const openCheckout = (target: CheckoutTarget) => {
    setCheckout(target);
    setCheckoutError(null);
    setPromotionCode("");
    setAppliedCode(target.discountCents > 0 ? "" : null);
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
    if (placedOrder || stripeTabBlocked) void loadData();
    setPlacedOrder(null);
    setStripeTabBlocked(null);
    setCheckout(null);
    setCheckoutError(null);
    setPromotionCode("");
    setAppliedCode(null);
  };

  // Applying a code needs a real order: the server alone knows eligibility and
  // the discount, so the first Apply places the pending order and later ones
  // replace the code on it. The totals shown are always the server's.
  const handleApplyCode = async () => {
    if (!checkout || applying || submitting) return;
    const code = promotionCode.trim();
    if (!code) return;
    setApplying(true);
    setCheckoutError(null);
    try {
      let order: OrderResponse;
      if (checkout.order) {
        try {
          order = await userApi.applyPromotionToOrder(checkout.order.id, code);
        } catch (err) {
          const reason = rejectionFromError(err);
          if (!reason) throw err;
          setCheckoutError(t(`user.plan.promotion.rejected.${reason}`));
          return;
        }
      } else {
        const result = await userApi.createOrder({
          plan_id: checkout.planId,
          duration_days: checkout.durationDays,
          promotion_code: code,
        });
        order = result.order;
        if (result.promotion_rejected) {
          setCheckout({ ...checkout, order, priceCents: order.price_cents, discountCents: order.discount_cents ?? 0 });
          setCheckoutError(t(`user.plan.promotion.rejected.${result.promotion_rejected}`));
          setOrders(await userApi.listMyOrders());
          return;
        }
        setOrders(await userApi.listMyOrders());
      }
      setCheckout({ ...checkout, order, priceCents: order.price_cents, discountCents: order.discount_cents ?? 0 });
      setAppliedCode(code);
      setPromotionCode("");
    } catch (err) {
      setCheckoutError(
        err instanceof ApiError && err.status === 409 ? t("user.plan.order.conflict") : t("user.plan.promotion.error"),
      );
    } finally {
      setApplying(false);
    }
  };

  // Hands an existing pending order to the chosen provider. A redirect provider
  // (Stripe) leaves the SPA; a manual one only records the choice, so the order
  // waits for an admin to confirm the transfer.
  // Hands an existing pending order to the chosen provider. Stripe opens in a
  // new tab that was reserved synchronously on click (a tab opened after an
  // await is treated as a popup and blocked); a manual provider only records
  // the choice, so the order waits for an admin to confirm the transfer.
  const payWithProvider = async (orderId: string, stripeTab: Window | null) => {
    const provider = providers.find((item) => item.name === providerName);
    if (!provider) throw new Error("no payment provider selected");
    const session = await userApi.startCheckout(orderId, provider.name);
    if (session.mode === "redirect") {
      if (!session.redirect_url) throw new Error("checkout returned no redirect URL");
      if (stripeTab && !stripeTab.closed) {
        stripeTab.location.href = session.redirect_url;
        setPaying({ url: session.redirect_url, planName: checkout?.planName ?? "", priceCents: checkout?.priceCents ?? 0 });
        closeCheckout();
      } else {
        setStripeTabBlocked(session.redirect_url);
      }
      return;
    }
    stripeTab?.close();
    const orderList = await userApi.listMyOrders();
    setOrders(orderList);
    setPlacedOrder(orderList.find((order) => order.id === orderId) ?? null);
  };

  const handleConfirmCheckout = async () => {
    if (!checkout || submitting) return;
    const code = promotionCode.trim();
    const provider = providers.find((item) => item.name === providerName);
    const stripeTab = provider?.mode === "redirect" ? window.open("about:blank", "_blank") : null;
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
            stripeTab?.close();
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
          stripeTab?.close();
          return;
        }
        if (order.status === "paid") {
          closeCheckout();
          stripeTab?.close();
          setFlash([{ type: "success", content: t("user.plan.checkoutModal.paid"), dismissible: true, onDismiss: () => setFlash([]) }]);
          await loadData();
          return;
        }
      }
      await payWithProvider(order.id, stripeTab);
    } catch (err) {
      stripeTab?.close();
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
  // Buying the plan the user already holds extends it: the server adds the new
  // term on top of the current expiry and keeps usage and the reset date.
  const isExtension = (planId: string) => hasPlan && profile?.plan_id === planId;
  const formatDate = (value: Date) => value.toLocaleDateString(i18n.resolvedLanguage ?? i18n.language);
  const extendedUntil = (days: number) => {
    const base = profile?.plan_expires_at ? new Date(profile.plan_expires_at) : new Date();
    return new Date(Math.max(base.getTime(), Date.now()) + days * 86400000);
  };

  const daysLeft = profile?.plan_expires_at
    ? Math.max(0, Math.ceil((new Date(profile.plan_expires_at).getTime() - Date.now()) / 86400000))
    : 0;

  return (
    <ContentLayout header={<Header variant="h1" description={t("user.plan.subtitle")}>{t("user.plan.title")}</Header>}>
      <div className="plan-info">
      <SpaceBetween size="l">
        <Flashbar items={flash} />

        {hasPlan && profile && (
          <Container
            header={
              <Header variant="h2" description={t("user.plan.current.description")}>
                {t("user.plan.currentPlan")}
              </Header>
            }
          >
            <div className="plan-current">
              <div className="plan-current__name">
                <Box variant="awsui-key-label">{t("user.plan.planName")}</Box>
                <Box variant="h2" padding="n">{profile.plan_name}</Box>
                <StatusIndicator type={profile.status === "active" ? "success" : "warning"}>
                  {t(STATUS_LABEL_KEYS[profile.status] ?? "user.dashboard.statusUnknown")}
                </StatusIndicator>
              </div>
              <div className="plan-current__metric">
                <Box variant="awsui-key-label">{t(profile.traffic_limit ? "user.plan.current.dataLeft" : "user.plan.current.dataUsed")}</Box>
                <Box variant="h2" padding="n">
                  {profile.traffic_limit
                    ? formatBytes(Math.max(0, profile.traffic_limit - profile.traffic_used))
                    : formatBytes(profile.traffic_used) }
                </Box>
                {profile.traffic_limit ? (
                  <ProgressBar
                    value={Math.max(0, 100 - Math.min(100, (profile.traffic_used / profile.traffic_limit) * 100))}
                    ariaLabel={t("user.plan.current.dataLeft")}
                    additionalInfo={t("user.plan.current.dataOf", { limit: formatBytes(profile.traffic_limit) })}
                  />
                ) : (
                  <Box variant="small" color="text-body-secondary">{t("user.plan.current.unlimited")}</Box>
                )}
              </div>
              <div className="plan-current__metric">
                <Box variant="awsui-key-label">{t("user.plan.expiresAt")}</Box>
                <Box variant="h2" padding="n">
                  {profile.plan_expires_at ? t("user.plan.current.daysLeft", { days: daysLeft }) : "-"}
                </Box>
                <Box variant="small" color="text-body-secondary">
                  {profile.plan_expires_at ? t("user.plan.current.until", { date: formatDate(new Date(profile.plan_expires_at)) }) : ""}
                </Box>
              </div>
            </div>
            <Box variant="small" color="text-body-secondary" margin={{ top: "m" }}>
              {t("user.plan.current.note")}
            </Box>
          </Container>
        )}

        {hasPlan && profile && (
          <ResetTickets canReset={!!profile.traffic_limit} onRedeemed={() => void loadData()} />
        )}

        {!hasPlan && !pendingOrder && (
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
          <Header
            variant="h2"
            counter={plans.length > 0 ? `(${plans.length})` : undefined}
            description={t("user.plan.how.description")}
          >
            {t("user.plan.availablePlans")}
          </Header>
          <div className="plan-how">
            {[
              { title: t("user.plan.how.step1Title"), body: t("user.plan.how.step1Body") },
              { title: t("user.plan.how.step2Title"), body: t("user.plan.how.step2Body") },
              { title: t("user.plan.how.step3Title"), body: t("user.plan.how.step3Body") },
            ].map((step, index) => (
              <div key={index} className="plan-how__step">
                <span className="plan-how__num" aria-hidden>{index + 1}</span>
                <div>
                  <Box fontWeight="bold">{step.title}</Box>
                  <Box variant="small" color="text-body-secondary">{step.body}</Box>
                </div>
              </div>
            ))}
          </div>
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
                  <Container
                    key={item.id}
                    fitHeight
                    header={
                      <Header
                        variant="h2"
                        actions={isExtension(item.id) ? <Badge color="blue">{t("user.plan.current.badge")}</Badge> : undefined}
                      >
                        {item.name}
                      </Header>
                    }
                  >
                    <div className="plan-info__card">
                      {selectedPrice ? (
                        <div className="plan-info__price">
                          <Box tagOverride="span" fontSize="heading-xl" fontWeight="bold">{formatPriceCents(selectedPrice.price_cents)}</Box>
                          <Box tagOverride="span" color="text-body-secondary">
                            {" "}{t("user.plan.perDays", { days: selectedPrice.duration_days })}
                          </Box>
                          {savings && <Badge color="green">{t("user.plan.order.savings", { percent: savings.percent })}</Badge>}
                        </div>
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
                              iconName={isExtension(item.id) ? "redo" : "add-plus"}
                              ariaLabel={`${isExtension(item.id) ? t("user.plan.extend.button") : t("user.plan.order.placeOrder")} ${item.name}`}
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
                              {isExtension(item.id) ? t("user.plan.extend.button") : t("user.plan.order.placeOrder")}
                            </Button>
                            {isExtension(item.id) && !pendingOrder && (
                              <Box variant="small" color="text-body-secondary" textAlign="center">
                                {t("user.plan.extend.cardHint", { date: formatDate(extendedUntil(selectedPrice.duration_days)) })}
                              </Box>
                            )}
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

        <Container header={<Header variant="h2">{t("user.plan.faq.title")}</Header>}>
          <div className="plan-faq">
            {[
              { q: t("user.plan.faq.q1"), a: t("user.plan.faq.a1") },
              { q: t("user.plan.faq.q2"), a: t("user.plan.faq.a2") },
              { q: t("user.plan.faq.q3"), a: t("user.plan.faq.a3") },
              { q: t("user.plan.faq.q4"), a: t("user.plan.faq.a4") },
              { q: t("user.plan.faq.q5"), a: t("user.plan.faq.a5") },
            ].map((item, index) => (
              <details key={index} className="plan-faq__item">
                <summary>{item.q}</summary>
                <Box variant="p" color="text-body-secondary">{item.a}</Box>
              </details>
            ))}
          </div>
        </Container>

        <Modal
          visible={checkoutReturn !== null}
          onDismiss={checkoutReturn?.kind === "confirming" ? undefined : () => { ++returnGeneration.current; setCheckoutReturn(null); void loadData(); }}
          closeAriaLabel={t("user.announcements.close")}
          header={checkoutReturn?.kind === "paid" ? t("user.plan.return.paidTitle")
            : checkoutReturn?.kind === "extension" ? t("user.plan.return.extensionTitle")
            : checkoutReturn?.kind === "cancelled" ? t("user.plan.return.cancelledTitle")
            : checkoutReturn?.kind === "failed" ? t("user.plan.return.failedTitle")
            : t("user.plan.return.pendingTitle")}
          footer={checkoutReturn && checkoutReturn.kind !== "confirming" ? (
            <Box float="right">
              <Button variant="primary" onClick={() => { ++returnGeneration.current; setCheckoutReturn(null); void loadData(); }}>{t("user.plan.return.ok")}</Button>
            </Box>
          ) : undefined}
        >
          {checkoutReturn?.kind === "confirming" || checkoutReturn?.kind === undefined ? (
            <Box textAlign="center" padding="l">
              <SpaceBetween size="s" alignItems="center">
                <Spinner size="large" />
                <Box>{t("user.plan.return.confirming")}</Box>
              </SpaceBetween>
            </Box>
          ) : checkoutReturn?.kind === "paid" || checkoutReturn?.kind === "extension" ? (
            <SpaceBetween size="m">
              <StatusIndicator type="success">
                {checkoutReturn.kind === "extension"
                  ? t("user.plan.return.extensionBody", { days: checkoutReturn.days, until: checkoutReturn.until ? new Date(checkoutReturn.until).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language) : "-" })
                  : t("user.plan.return.paidBody", { plan: checkoutReturn.planName, until: checkoutReturn.until ? new Date(checkoutReturn.until).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language) : "-" })}
              </StatusIndicator>
              <Box variant="small" color="text-body-secondary">{t("user.plan.return.autoHint")}</Box>
            </SpaceBetween>
          ) : (
            <SpaceBetween size="m">
              <StatusIndicator type={checkoutReturn.kind === "pending" ? "in-progress" : "info"}>
                {checkoutReturn.kind === "pending" ? t("user.plan.return.pendingBody")
                  : checkoutReturn.kind === "cancelled" ? t("user.plan.return.cancelledBody")
                  : t("user.plan.return.failedBody")}
              </StatusIndicator>
              {checkoutReturn.kind === "cancelled" && pendingOrder && (
                <Button variant="primary" onClick={() => { ++returnGeneration.current; setCheckoutReturn(null); openOrderCheckout(pendingOrder); }}>
                  {t("user.plan.order.checkout.payNow")}
                </Button>
              )}
            </SpaceBetween>
          )}
        </Modal>

        <Modal
          visible={paying !== null}
          onDismiss={() => { setPaying(null); void loadData(); }}
          closeAriaLabel={t("user.announcements.close")}
          header={t("user.plan.paying.title")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setPaying(null); void loadData(); }}>{t("user.plan.paying.dismiss")}</Button>
                {paying && (
                  <Button variant="primary" iconName="external" iconAlign="right" onClick={() => window.open(paying.url, "_blank")}>
                    {t("user.plan.paying.reopen")}
                  </Button>
                )}
              </SpaceBetween>
            </Box>
          }
        >
          {paying && (
            <SpaceBetween size="m">
              <StatusIndicator type="in-progress">{t("user.plan.paying.standby")}</StatusIndicator>
              <Box>{t("user.plan.paying.body", { plan: paying.planName, amount: formatPriceCents(paying.priceCents) })}</Box>
              <Box variant="small" color="text-body-secondary">{t("user.plan.paying.reopenHint")}</Box>
            </SpaceBetween>
          )}
        </Modal>

        <Modal
          size="medium"
          closeAriaLabel={t("user.announcements.close")}
          visible={checkout !== null}
          onDismiss={() => {
            if (!submitting) closeCheckout();
          }}
          header={placedOrder ? t("user.plan.checkoutModal.placedTitle") : checkout && isExtension(checkout.planId) ? t("user.plan.extend.title") : t("user.plan.checkoutModal.title")}
          footer={placedOrder || stripeTabBlocked ? (
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                {placedOrder && <Button onClick={() => { closeCheckout(); navigate("/portal/orders"); }}>{t("user.plan.checkoutModal.goOrders")}</Button>}
                {stripeTabBlocked && (
                  <Button variant="primary" href={stripeTabBlocked} target="_blank" iconName="external" iconAlign="right" onClick={() => closeCheckout()}>
                    {t("user.plan.checkoutModal.openStripe")}
                  </Button>
                )}
                {placedOrder && <Button variant="primary" onClick={closeCheckout}>{t("user.plan.checkoutModal.done")}</Button>}
              </SpaceBetween>
            </Box>
          ) : (
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button disabled={submitting} onClick={closeCheckout}>
                  {t("user.plan.order.dismiss")}
                </Button>
                <Button
                  variant="primary"
                  loading={submitting}
                  disabled={!providerName || applying || (checkout?.order === null && pendingOrder !== null)}
                  onClick={() => void handleConfirmCheckout()}
                >
                  {checkout
                    ? t(isExtension(checkout.planId) ? "user.plan.extend.confirmAmount" : "user.plan.checkoutModal.confirmAmount", { amount: formatPriceCents(checkout.priceCents) })
                    : t("user.plan.checkoutModal.confirm")}
                </Button>
              </SpaceBetween>
            </Box>
          )}
        >
          {placedOrder ? (
            <SpaceBetween size="l">
              <Box textAlign="center" padding={{ top: "s" }}>
                <SpaceBetween size="xs" alignItems="center">
                  <Icon name="status-positive" variant="success" size="big" />
                  <Box variant="h2" padding="n">{t("user.plan.checkoutModal.placedHeading")}</Box>
                  <Box color="text-body-secondary">{t("user.plan.checkoutModal.placedHint")}</Box>
                </SpaceBetween>
              </Box>
              <div className="plan-info__receipt" style={{ background: colorBackgroundCellShaded, borderRadius: borderRadiusItem }}>
                <div className="plan-info__receipt-lines">
                  <div className="plan-info__receipt-line">
                    <Box color="text-body-secondary">{t("user.plan.checkoutModal.item")}</Box>
                    <Box>{placedOrder.plan_name} · {t("user.plan.daysLabel", { days: placedOrder.duration_days })}</Box>
                  </div>
                  <div className="plan-info__receipt-line">
                    <Box color="text-body-secondary">{t("user.plan.checkoutModal.provider")}</Box>
                    <Box>{providerLabel(placedOrder.provider ?? "admin")}</Box>
                  </div>
                  <div className="plan-info__receipt-line">
                    <Box color="text-body-secondary">{t("user.plan.order.col.status")}</Box>
                    <StatusIndicator type="pending">{t("user.plan.order.status.pending")}</StatusIndicator>
                  </div>
                </div>
                <div className="plan-info__receipt-total" style={{ borderBlockStart: `1px solid ${colorBorderDividerDefault}` }}>
                  <Box variant="h3" padding="n">{t("user.plan.checkoutModal.amountDue")}</Box>
                  <Box fontSize="heading-xl" fontWeight="bold">{formatPriceCents(placedOrder.price_cents)}</Box>
                </div>
              </div>
              <Alert type="info">{t("user.plan.checkoutModal.manualNext")}</Alert>
            </SpaceBetween>
          ) : stripeTabBlocked ? (
            <Alert type="warning" header={t("user.plan.checkoutModal.popupBlockedTitle")}>
              {t("user.plan.checkoutModal.popupBlocked")}
            </Alert>
          ) : checkout && (
            <SpaceBetween size="l">
              <div className="plan-info__receipt" style={{ background: colorBackgroundCellShaded, borderRadius: borderRadiusItem }}>
                <div className="plan-info__receipt-item">
                  <div>
                    <Box variant="awsui-key-label">
                      {isExtension(checkout.planId) ? t("user.plan.extend.itemLabel") : t("user.plan.checkoutModal.item")}
                    </Box>
                    <Box variant="h3" padding="n">{checkout.planName}</Box>
                  </div>
                  <Badge>{t("user.plan.daysLabel", { days: checkout.durationDays })}</Badge>
                </div>
                <div className="plan-info__receipt-lines" style={{ borderBlockStart: `1px solid ${colorBorderDividerDefault}` }}>
                  <div className="plan-info__receipt-line">
                    <Box color="text-body-secondary">{t("user.plan.checkoutModal.subtotal")}</Box>
                    <Box>{formatPriceCents(checkout.priceCents + checkout.discountCents)}</Box>
                  </div>
                  {checkout.discountCents > 0 && (
                  <div className="plan-info__receipt-line">
                    <Box color="text-body-secondary">
                      {t("user.plan.checkoutModal.discount")}
                      {appliedCode ? ` (${appliedCode})` : ""}
                    </Box>
                    <Box color={checkout.discountCents > 0 ? "text-status-success" : undefined}>
                      -{formatPriceCents(checkout.discountCents)}
                    </Box>
                  </div>
                  )}
                <div className="plan-info__receipt-promo">
                  <div className="plan-info__promo">
                    <Input
                      value={promotionCode}
                      disabled={submitting || applying}
                      placeholder={appliedCode ? t("user.plan.checkoutModal.replaceCode") : t("user.plan.checkoutModal.promoPlaceholder")}
                      ariaLabel={t("user.plan.promotion.label")}
                      onChange={({ detail }) => setPromotionCode(detail.value)}
                      onKeyDown={({ detail }) => {
                        if (detail.key === "Enter") void handleApplyCode();
                      }}
                    />
                    <Button
                      loading={applying}
                      disabled={!promotionCode.trim() || submitting || (checkout.order === null && pendingOrder !== null)}
                      onClick={() => void handleApplyCode()}
                    >
                      {t("user.plan.promotion.apply")}
                    </Button>
                  </div>
                  {appliedCode && (
                    <Box variant="small" color="text-status-success" margin={{ top: "xxs" }}>
                      {t("user.plan.checkoutModal.codeApplied", { code: appliedCode })}
                    </Box>
                  )}
                </div>
                </div>
                <div className="plan-info__receipt-total" style={{ borderBlockStart: `1px solid ${colorBorderDividerDefault}` }}>
                  <Box variant="h3" padding="n">{t("user.plan.checkoutModal.total")}</Box>
                  <Box fontSize="heading-xl" fontWeight="bold">{formatPriceCents(checkout.priceCents)}</Box>
                </div>
              </div>

              {isExtension(checkout.planId) && (
                <Alert type="info" header={t("user.plan.extend.title")}>
                  {t("user.plan.extend.detail", {
                    from: profile?.plan_expires_at ? formatDate(new Date(profile.plan_expires_at)) : "-",
                    to: formatDate(extendedUntil(checkout.durationDays)),
                  })}
                </Alert>
              )}

              <FormField label={t("user.plan.checkoutModal.provider")} stretch>
                {providers.length === 0 ? (
                  <Alert type="warning">{t("user.plan.checkoutModal.noProviders")}</Alert>
                ) : providers.length === 1 && providers[0] ? (
                  <Box>
                    <Box fontWeight="bold">{providerLabel(providers[0].name)}</Box>
                    <Box variant="small" color="text-body-secondary">{providerHint(providers[0].name)}</Box>
                  </Box>
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

              {checkout.order === null && pendingOrder && <Alert type="warning">{t("user.plan.order.blockedByPending")}</Alert>}
              {checkoutError && <Alert type="error">{checkoutError}</Alert>}
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
      </div>
    </ContentLayout>
  );
}
