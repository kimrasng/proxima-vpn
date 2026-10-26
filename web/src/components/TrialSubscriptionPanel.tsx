import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import type { TFunction } from "i18next";
import { Alert, Box, Button, Container, Header, Modal, SpaceBetween, StatusIndicator } from "@cloudscape-design/components";
import "./trialSubscription.css";
import { cancelTrial, confirmTrial, currentTrial, openBillingPortal, type PaymentMethodSummary, type TrialSubscription } from "../api/trialSubscription";

const BRAND_LABELS: Record<string, string> = {
  visa: "Visa", mastercard: "Mastercard", amex: "American Express", discover: "Discover",
  jcb: "JCB", diners: "Diners Club", unionpay: "UnionPay", cartes_bancaires: "Cartes Bancaires",
};

function fundingLabel(funding: string, t: TFunction): string {
  if (funding === "credit") return t("trialSubscription.card.credit");
  if (funding === "debit") return t("trialSubscription.card.debit");
  if (funding === "prepaid") return t("trialSubscription.card.prepaid");
  return "";
}

function CardRow({ method, t }: { readonly method: PaymentMethodSummary; readonly t: TFunction }) {
  const brand = BRAND_LABELS[method.brand] ?? t("trialSubscription.card.unknownBrand");
  const expiry = `${String(method.exp_month).padStart(2, "0")}/${String(method.exp_year).slice(-2)}`;
  const funding = fundingLabel(method.funding, t);
  return (
    <div className="trial-card" role="group" aria-label={t("trialSubscription.card.label")}>
      <Box variant="awsui-key-label">{t("trialSubscription.card.label")}</Box>
      <Box fontWeight="bold">{brand} •••• {method.last4}</Box>
      <Box variant="small" color="text-body-secondary">
        {t("trialSubscription.card.expires", { date: expiry })}
        {funding ? ` · ${funding}` : ""}
        {method.wallet ? ` · ${t("trialSubscription.card.wallet", { wallet: method.wallet.replace("_", " ") })}` : ""}
      </Box>
    </div>
  );
}

export default function TrialSubscriptionPanel({ code, onConfirmed, onBillingChange }: {
  readonly code: string;
  readonly onConfirmed: () => void;
  readonly onBillingChange: (enabled: boolean) => void;
}) {
  const { t, i18n } = useTranslation();
  const [params] = useSearchParams();
  const returnedID = params.get("trial_id");
  const returnedSession = params.get("session_id");
  const [subscription, setSubscription] = useState<TrialSubscription | null>(null);
  const [visible, setVisible] = useState(Boolean(returnedID && returnedSession));
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState(false);
  const [portalBusy, setPortalBusy] = useState(false);
  const [portalError, setPortalError] = useState<"popupBlocked" | "portalError" | "refreshError" | null>(null);
  const portalPending = useRef(false);
  useEffect(() => {
    let active = true;
    const refreshCard = () => {
      if (!portalPending.current) return;
      portalPending.current = false;
      void currentTrial().then(value => {
        if (!active) return;
        setSubscription(value);
        onBillingChange(value !== null && value.status !== "canceled" && value.status !== "checkout" && !value.cancel_at_period_end);
      }, () => { if (active) setPortalError("refreshError"); });
    };
    window.addEventListener("focus", refreshCard);
    return () => { active = false; window.removeEventListener("focus", refreshCard); };
  }, [onBillingChange]);
  const editCard = async () => {
    setPortalError(null);
    const portal = window.open("about:blank", "_blank");
    if (!portal) {
      setPortalError("popupBlocked");
      return;
    }
    portal.opener = null;
    setPortalBusy(true);
    try {
      const result = await openBillingPortal();
      portalPending.current = true;
      portal.location.href = result.url;
    } catch (failure) {
      portal.close();
      if (!(failure instanceof Error)) throw failure;
      setPortalError("portalError");
    } finally {
      setPortalBusy(false);
    }
  };
  useEffect(() => {
    let active = true;
    const loading = returnedID && returnedSession
      ? confirmTrial(returnedID, returnedSession) : currentTrial();
    void loading.then(
      (value) => {
        if (active) {
          setSubscription(value);
          onBillingChange(value !== null && value.status !== "canceled" && value.status !== "checkout" && !value.cancel_at_period_end);
          if (returnedID && returnedSession) onConfirmed();
        }
      },
      () => { if (active) setError(true); },
    );
    return () => { active = false; };
  }, [code, returnedID, returnedSession, onConfirmed, onBillingChange]);
  const cancel = async () => {
    setBusy(true);
    setError(false);
    try {
      setSubscription(await cancelTrial());
      onBillingChange(false);
      setConfirm(false);
    } catch (failure) {
      if (!(failure instanceof Error)) throw failure;
      setError(true);
    } finally { setBusy(false); }
  };
  if (!subscription && !(returnedID && returnedSession)) return null;
  const date = (value: string | null) => value ? new Date(value).toLocaleString(i18n.language) : t("trialSubscription.pending");
  const price = subscription ? new Intl.NumberFormat(i18n.language, {
    style: "currency", currency: subscription.currency,
  }).format(subscription.price_cents / 100) : "";
  const billing = subscription && subscription.status !== "checkout" && subscription.status !== "canceled" && !subscription.cancel_at_period_end;
  return <>
    {subscription && subscription.status !== "checkout" && (
      <Container
        header={<Header variant="h2" description={t(billing ? "trialSubscription.card.description" : "trialSubscription.card.billingStopped")}
          actions={<SpaceBetween direction="horizontal" size="xs">
            <Button loading={portalBusy} onClick={() => void editCard()}>{t("trialSubscription.card.edit")}</Button>
            <Button variant="link" onClick={() => setVisible(true)}>{t("trialSubscription.manage")}</Button>
          </SpaceBetween>}>
          {t("trialSubscription.card.title")}
        </Header>}
      >
        {portalError && <Alert type="error">{t(`trialSubscription.card.${portalError}`)}</Alert>}
        <div className="trial-summary">
          <div className="trial-summary__item">
            {subscription.payment_method
              ? <CardRow method={subscription.payment_method} t={t} />
              : <>
                <Box variant="awsui-key-label">{t("trialSubscription.card.label")}</Box>
                <Box color="text-body-secondary">{t(subscription.payment_method_unavailable
                  ? "trialSubscription.card.unavailable" : "trialSubscription.card.none")}</Box>
              </>}
          </div>
          <div className="trial-summary__item">
            <Box variant="awsui-key-label">{t("trialSubscription.card.nextCharge")}</Box>
            {billing ? <>
              <Box fontWeight="bold">{price}</Box>
              <Box variant="small" color="text-body-secondary">
                {date(subscription.current_period_end ?? subscription.trial_ends_at)} · {t("trialSubscription.card.every", { days: subscription.duration_days })}
              </Box>
            </> : <Box color="text-body-secondary">{t("trialSubscription.card.noneScheduled")}</Box>}
          </div>
          <div className="trial-summary__item">
            <Box variant="awsui-key-label">{t("trialSubscription.card.state")}</Box>
            <StatusIndicator type={billing ? "warning" : "stopped"}>
              {billing ? t("trialSubscription.card.recurring") : t("trialSubscription.card.stopped")}
            </StatusIndicator>
          </div>
        </div>
      </Container>
    )}
    {subscription?.status === "checkout" &&
      <Button onClick={() => setVisible(true)}>{t("trialSubscription.start")}</Button>}
    <Modal visible={visible} onDismiss={() => setVisible(false)} header={t("trialSubscription.title")}>
    <SpaceBetween size="m">
      {error && <Alert type="error">{t("trialSubscription.error")}</Alert>}
      {subscription ? <>
        <Box fontWeight="bold">{subscription.plan_name}</Box>
        {subscription.status === "checkout" &&
          <Button variant="primary" href={`/portal/plan?promo=${encodeURIComponent(subscription.code)}&resume=1`}>{t("trialSubscription.start")}</Button>}
        {subscription.payment_method
          ? <CardRow method={subscription.payment_method} t={t} />
          : subscription.status !== "checkout" &&
            <Box variant="small" color="text-body-secondary">{t(subscription.payment_method_unavailable
              ? "trialSubscription.card.unavailable" : "trialSubscription.card.none")}</Box>}
        <Box>{t("trialSubscription.billing", { price, days: subscription.duration_days })}</Box>
        <Box>{t("trialSubscription.ends", { date: date(subscription.current_period_end ?? subscription.trial_ends_at) })}</Box>
        <Alert type={subscription.cancel_at_period_end || subscription.status === "canceled" ? "info" : "warning"}>
          {subscription.cancel_at_period_end || subscription.status === "canceled"
            ? t("trialSubscription.canceled") : t("trialSubscription.automatic")}
        </Alert>
        {subscription.status !== "canceled" && !subscription.cancel_at_period_end && subscription.status !== "checkout" &&
          <Button onClick={() => setConfirm(true)}>{t("trialSubscription.cancel")}</Button>}
      </> : null}
    </SpaceBetween>
    </Modal>
    <Modal visible={confirm} onDismiss={() => { if (!busy) setConfirm(false); }} header={t("trialSubscription.cancel")}
      footer={<SpaceBetween direction="horizontal" size="s">
        <Button disabled={busy} onClick={() => setConfirm(false)}>{t("trialSubscription.keep")}</Button>
        <Button variant="primary" loading={busy} onClick={() => void cancel()}>{t("trialSubscription.confirm")}</Button>
      </SpaceBetween>}>
      {t("trialSubscription.cancelHint")}
    </Modal>
  </>;
}
