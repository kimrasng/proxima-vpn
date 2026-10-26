import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { Alert, Box, Button, Container, Header, Modal, SpaceBetween } from "@cloudscape-design/components";
import { getPromoLanding } from "../api/promotionKinds";
import { cancelTrial, confirmTrial, currentTrial, startTrial, type TrialSubscription } from "../api/trialSubscription";

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
  const [days, setDays] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState(false);
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
    if (code) void getPromoLanding(code).then(
      (promo) => { if (active) setDays(promo.type === "trial" && promo.is_valid ? promo.trial_days ?? null : null); },
      () => { if (active) setDays(null); },
    );
    return () => { active = false; };
  }, [code, returnedID, returnedSession, onConfirmed, onBillingChange]);
  const start = async () => {
    setBusy(true);
    setError(false);
    try {
      const result = await startTrial(subscription?.code ?? code);
      if ("mode" in result) window.location.assign(result.redirect_url);
      else window.location.assign("/portal/plan");
    } catch (failure) {
      if (!(failure instanceof Error)) throw failure;
      setError(true);
    } finally { setBusy(false); }
  };
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
  if (!subscription && days === null && !error) return null;
  const date = (value: string | null) => value ? new Date(value).toLocaleString(i18n.language) : t("trialSubscription.pending");
  const price = subscription ? new Intl.NumberFormat(i18n.language, {
    style: "currency", currency: subscription.currency,
  }).format(subscription.price_cents / 100) : "";
  return <Container header={<Header variant="h2">{t("trialSubscription.title")}</Header>}>
    <SpaceBetween size="m">
      {error && <Alert type="error">{t("trialSubscription.error")}</Alert>}
      {subscription ? <>
        <Box fontWeight="bold">{subscription.plan_name}</Box>
        {subscription.status === "checkout" &&
          <Button variant="primary" loading={busy} onClick={() => void start()}>{t("trialSubscription.start")}</Button>}
        <Box>{t("trialSubscription.billing", { price, days: subscription.duration_days })}</Box>
        <Box>{t("trialSubscription.ends", { date: date(subscription.current_period_end ?? subscription.trial_ends_at) })}</Box>
        <Alert type={subscription.cancel_at_period_end || subscription.status === "canceled" ? "info" : "warning"}>
          {subscription.cancel_at_period_end || subscription.status === "canceled"
            ? t("trialSubscription.canceled") : t("trialSubscription.automatic")}
        </Alert>
        {subscription.status !== "canceled" && !subscription.cancel_at_period_end && subscription.status !== "checkout" &&
          <Button onClick={() => setConfirm(true)}>{t("trialSubscription.cancel")}</Button>}
      </> : days !== null && <>
        <Box>{t("trialSubscription.offer", { days, code })}</Box>
        <Box color="text-body-secondary">{t("trialSubscription.notice")}</Box>
        <Button variant="primary" loading={busy} onClick={() => void start()}>{t("trialSubscription.start")}</Button>
      </>}
    </SpaceBetween>
    <Modal visible={confirm} onDismiss={() => { if (!busy) setConfirm(false); }} header={t("trialSubscription.cancel")}
      footer={<SpaceBetween direction="horizontal" size="s">
        <Button disabled={busy} onClick={() => setConfirm(false)}>{t("trialSubscription.keep")}</Button>
        <Button variant="primary" loading={busy} onClick={() => void cancel()}>{t("trialSubscription.confirm")}</Button>
      </SpaceBetween>}>
      {t("trialSubscription.cancelHint")}
    </Modal>
  </Container>;
}
