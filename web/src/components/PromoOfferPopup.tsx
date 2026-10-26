import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Modal } from "@cloudscape-design/components";
import { ApiError } from "../api/client";
import { getPromoLanding, type PromoLandingInfo } from "../api/promotionKinds";
import { getProfile } from "../api/user";
import type { UserProfile } from "../api/types";
import {
  clearPromo, markPromoOfferSeen, pendingPromo, pendingPromoDuration, promoOfferSeen,
} from "../utils/pendingPromo";
import { PromoOffer } from "./PromoOffer";
import { PromoOfferError, type PromoOfferFailure } from "./PromoOfferError";

type Offer = {
  readonly promo: PromoLandingInfo;
  readonly durationDays: number;
};

export function PromoOfferPopup() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const requestedCode = params.get("promo")?.trim() || "";
  const code = requestedCode || pendingPromo();
  const requestedDays = Number(params.get("duration_days")) || (code ? pendingPromoDuration(code) : undefined);
  const language = i18n.resolvedLanguage ?? i18n.language;
  const [offer, setOffer] = useState<Offer | null>(null);
  const [failure, setFailure] = useState<PromoOfferFailure | null>(null);
  const queryError = params.get("promo_error");
  const lookup = useRef<{ code: string; language: string; request: Promise<[UserProfile, PromoLandingInfo]> } | null>(null);

  useEffect(() => {
    if (queryError !== "invalid" && queryError !== "ineligible" && queryError !== "unavailable") return;
    setFailure(queryError);
    navigate("/portal/dashboard", { replace: true });
  }, [queryError, navigate]);

  useEffect(() => {
    if (!code || queryError) return;
    let active = true;
    if (lookup.current?.code !== code || lookup.current.language !== language) {
      lookup.current = { code, language, request: Promise.all([getProfile(), getPromoLanding(code, language, true)]) };
    }
    void lookup.current.request.then(
      ([profile, promo]) => {
        if (!active) return;
        clearPromo();
        if (!promo.plan) {
          setFailure(!promo.is_valid ? "ineligible" : "unavailable");
          navigate("/portal/dashboard", { replace: true });
          return;
        }
        if (requestedCode || !promoOfferSeen(profile.email, promo.code) || !promo.is_valid) {
          markPromoOfferSeen(profile.email, promo.code);
          setOffer({ promo, durationDays: requestedDays ?? promo.plan.duration_days });
        }
        navigate("/portal/dashboard", { replace: true });
      },
      (error: unknown) => {
        if (active) {
          clearPromo();
          setFailure(error instanceof ApiError && error.status === 404 ? "invalid" : "unavailable");
          navigate("/portal/dashboard", { replace: true });
        }
      },
    );
    return () => { active = false; };
  }, [code, language, navigate, requestedCode, requestedDays, queryError]);

  return <>
    <PromoOfferError failure={failure} onDismiss={() => setFailure(null)} />
    {offer && <Modal visible width={480}
    header={offer.promo.headline || t("user.promo.offer.title")}
    closeAriaLabel={t("user.announcements.close")}
    onDismiss={() => setOffer(null)}>
    <div className="promo-offer-popup">
    <PromoOffer promo={offer.promo} durationDays={offer.durationDays} showCampaign={false}
      onDurationChange={(durationDays) => setOffer({ ...offer, durationDays })}
      onDismiss={() => setOffer(null)}
      onAccept={(days) => {
        if (!offer.promo.is_valid) {
          setOffer(null);
          setFailure("ineligible");
          return;
        }
        const query = new URLSearchParams({
          promo: offer.promo.code, resume: "1", duration_days: String(days),
        });
        navigate(`/portal/plan?${query}`);
      }} />
    </div>
  </Modal>}
  </>;
}
