import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { Spinner } from "@cloudscape-design/components";
import { isAuthenticated } from "../api/auth";
import { ApiError } from "../api/client";
import { getPromoLanding, type PromoLandingInfo } from "../api/promotionKinds";
import { PromoOffer } from "../components/PromoOffer";
import type { PromoOfferFailure } from "../components/PromoOfferError";
import { rememberPromo, clearPromo, pendingPromoDuration, promoDashboardTarget } from "../utils/pendingPromo";
import Landing from "./Landing";
import "./promoLanding.css";

type LoadState = { readonly code: string; readonly promo?: PromoLandingInfo; readonly failure?: PromoOfferFailure };

export default function PromoLanding() {
  const { t, i18n } = useTranslation();
  const { code = "" } = useParams();
  const navigate = useNavigate();
  const [state, setState] = useState<LoadState | null>(null);
  const [duration, setDuration] = useState<number>();
  const lang = i18n.resolvedLanguage ?? i18n.language;
  const signedIn = isAuthenticated("user");

  useEffect(() => {
    if (signedIn) return;
    if (!code.trim()) {
      clearPromo();
      setState({ code, failure: "invalid" });
      return;
    }
    let active = true;
    void getPromoLanding(code, lang).then(
      (promo) => {
        if (!active) return;
        if (promo.is_valid && promo.plan) {
          const days = pendingPromoDuration(promo.code) ?? promo.plan.duration_days;
          rememberPromo(promo.code, days);
          setDuration(days);
        } else clearPromo();
        const failure = !promo.is_valid
          ? "ineligible"
          : !promo.plan ? "unavailable" : undefined;
        setState({ code, promo, failure });
      },
      (error: unknown) => {
        if (active) {
          clearPromo();
          setState({ code, failure: error instanceof ApiError && error.status === 404 ? "invalid" : "unavailable" });
        }
      },
    );
    return () => { active = false; };
  }, [code, lang, signedIn]);

  if (signedIn) return <Navigate to={code.trim()
    ? promoDashboardTarget(code, pendingPromoDuration(code))
    : "/portal/dashboard?promo_error=invalid"} replace />;
  const current = state?.code === code ? state : null;
  const promo = current?.promo;
  if (current?.failure) return <Navigate
    to={isAuthenticated("user") ? `/portal/dashboard?promo_error=${current.failure}` : "/"} replace />;
  const authParams = new URLSearchParams({ promo: promo?.code ?? code });
  if (duration) authParams.set("duration_days", String(duration));
  const authQuery = `?${authParams}`;

  return <Landing authQuery={authQuery} offer={
    <div className="promo-landing__offer">
      {!promo ? <div className="promo-landing__loading" role="status" aria-busy="true">
        <Spinner /> <span>{t("user.promo.loading")}</span>
      </div> : <PromoOffer promo={promo} durationDays={duration}
        onDurationChange={(days) => { setDuration(days); rememberPromo(promo.code, days); }}
        onAccept={(days) => {
          rememberPromo(promo.code, days);
          navigate(`/register?${new URLSearchParams({ promo: promo.code, duration_days: String(days) })}`);
        }}
        onDismiss={() => { clearPromo(); navigate("/"); }} />}
    </div>
  } />;
}
