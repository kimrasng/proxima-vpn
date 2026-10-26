import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useParams } from "react-router-dom";
import { Button, Icon, Spinner } from "@cloudscape-design/components";
import { isAuthenticated } from "../api/auth";
import { ApiError } from "../api/client";
import { getPromoLanding, type PromoLandingInfo } from "../api/promotionKinds";
import { formatPriceCents } from "../utils/planPricing";
import { formatKst } from "./admin/promotionFormModel";
import "./promoLanding.css";

type LoadState = { code: string; promo?: PromoLandingInfo; error?: "invalid" | "network" };

export default function PromoLanding() {
  const { t } = useTranslation();
  const { code = "" } = useParams();
  const [state, setState] = useState<LoadState | null>(null);
  const [retry, setRetry] = useState(0);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    let active = true;
    void getPromoLanding(code).then(
      (promo) => { if (active) setState({ code, promo }); },
      (error: unknown) => { if (active) setState({ code, error: error instanceof ApiError && error.status === 404 ? "invalid" : "network" }); },
    );
    return () => { active = false; };
  }, [code, retry]);
  const current = state?.code === code ? state : null;
  const promo = current?.promo;
  if (promo?.is_valid && isAuthenticated("user")) {
    return <Navigate to={`/portal/plan?promo=${encodeURIComponent(promo.code)}`} replace />;
  }

  const benefit = (p: PromoLandingInfo) => {
    if (p.type === "trial") return t("user.promo.trialBenefit", { days: p.trial_days });
    if (p.discount_type === "percent" && p.discount_value) return t("user.promo.percentBenefit", { value: p.discount_value });
    if (p.discount_type === "fixed" && p.discount_value) return t("user.promo.fixedBenefit", { value: formatPriceCents(p.discount_value) });
    return p.discount_description;
  };
  const copy = async (value: string) => {
    try { await navigator.clipboard.writeText(value); setCopied(true); window.setTimeout(() => setCopied(false), 1800); }
    catch { setCopied(false); }
  };
  const claimHref = (value: string) => `/login?promo=${encodeURIComponent(value)}`;
  const copyByType = {
    coupon: { kind: t("user.promo.kind.coupon"), headline: t("user.promo.headline.coupon"), lead: t("user.promo.lead.coupon"), claim: t("user.promo.claim.coupon") },
    trial: { kind: t("user.promo.kind.trial"), headline: t("user.promo.headline.trial"), lead: t("user.promo.lead.trial"), claim: t("user.promo.claim.trial") },
    affiliate: { kind: t("user.promo.kind.affiliate"), headline: t("user.promo.headline.affiliate"), lead: t("user.promo.lead.affiliate"), claim: t("user.promo.claim.affiliate") },
  };
  const text = promo ? copyByType[promo.type] : null;

  return (
    <main className="promo-landing">
      <header className="promo-landing__top">
        <a className="promo-landing__brand" href="/"><Icon name="lock-private" /> Proxima VPN</a>
      </header>

      {!current ? (
        <div className="promo-landing__center" role="status"><Spinner size="large" /><p>{t("user.promo.loading")}</p></div>
      ) : current.error === "network" ? (
        <section className="promo-landing__notice">
          <h1>{t("user.promo.errorTitle")}</h1>
          <p>{t("user.promo.error")}</p>
          <Button onClick={() => { setState(null); setRetry((n) => n + 1); }}>{t("user.promo.retry")}</Button>
        </section>
      ) : !promo?.is_valid || !text ? (
        <section className="promo-landing__notice">
          <span className="promo-landing__muted-code">{code}</span>
          <h1>{t("user.promo.invalidTitle")}</h1>
          <p>{t("user.promo.invalid")}</p>
          <div className="promo-landing__actions">
            <Button variant="primary" href="/portal/plan">{t("user.promo.seePlans")}</Button>
            <Button href="/login">{t("user.promo.login")}</Button>
          </div>
        </section>
      ) : (
        <section className={`promo-landing__hero promo-landing__hero--${promo.type}`}>
          <div className="promo-landing__intro">
            <span className="promo-landing__eyebrow">{text.kind}</span>
            <h1>{text.headline}</h1>
            <p>{text.lead}</p>
            <ol className="promo-landing__steps">
              <li><b>1</b>{t("user.promo.step1")}</li>
              <li><b>2</b>{t(promo.type === "trial" ? "user.promo.step2Trial" : "user.promo.step2")}</li>
              <li><b>3</b>{t("user.promo.step3")}</li>
            </ol>
          </div>

          <div className="promo-landing__ticket">
            <div className="promo-landing__ticket-main">
              <span className="promo-landing__label">{t("user.promo.benefitLabel")}</span>
              <div className="promo-landing__benefit">{benefit(promo)}</div>
              {promo.plan_name && <div className="promo-landing__plan">{t("user.promo.planOnly", { plan: promo.plan_name })}</div>}
              {promo.valid_until && <div className="promo-landing__until">{t("user.promo.until", { date: formatKst(promo.valid_until) })}</div>}
            </div>
            <div className="promo-landing__ticket-cut" aria-hidden="true" />
            <div className="promo-landing__ticket-foot">
              <span className="promo-landing__label">{t("user.promo.codeLabel")}</span>
              <button type="button" className="promo-landing__code" onClick={() => void copy(promo.code)} aria-label={t("user.promo.copyCode")}>
                <span>{promo.code}</span>
                <Icon name={copied ? "check" : "copy"} />
              </button>
              <span className="promo-landing__copied" aria-live="polite">{copied ? t("user.promo.copied") : ""}</span>
              <Button variant="primary" fullWidth href={claimHref(promo.code)}>{text.claim}</Button>
              <p className="promo-landing__hint">{promo.type === "trial" ? t("user.promo.benefitHintTrial") : t("user.promo.benefitHint")}</p>
            </div>
          </div>
        </section>
      )}

      <footer className="promo-landing__footer">
        <span><Icon name="lock-private" /> {t("user.promo.secure")}</span>
        <span><Icon name="settings" /> {t("user.promo.devices")}</span>
        <span><Icon name="status-info" /> {t("user.promo.support")}</span>
      </footer>
    </main>
  );
}
