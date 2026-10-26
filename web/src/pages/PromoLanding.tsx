import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useParams } from "react-router-dom";
import { Alert, Box, Button, Container, Header, Icon, SpaceBetween, Spinner } from "@cloudscape-design/components";
import { colorBackgroundLayoutMain, colorBackgroundContainerContent, colorBackgroundButtonPrimaryDefault, colorTextBodyDefault, colorTextButtonPrimaryDefault, spaceScaledL, spaceScaledXxl, borderRadiusContainer, fontSizeDisplayL, fontSizeHeadingXl } from "@cloudscape-design/design-tokens";
import { isAuthenticated } from "../api/auth";
import { ApiError } from "../api/client";
import { getPromoLanding, type PromoLandingInfo } from "../api/promotionKinds";
import "./promoLanding.css";

export default function PromoLanding() {
  const { t } = useTranslation();
  const { code = "" } = useParams();
  const [state, setState] = useState<{ code: string; promo?: PromoLandingInfo; error?: "invalid" | "network" } | null>(null);
  const [retry, setRetry] = useState(0);
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
  return <main className="promo-landing" style={{
    "--promo-background": colorBackgroundLayoutMain, "--promo-surface": colorBackgroundContainerContent,
    "--promo-accent": colorBackgroundButtonPrimaryDefault, "--promo-text": colorTextBodyDefault,
    "--promo-on-accent": colorTextButtonPrimaryDefault, "--promo-gap": spaceScaledL,
    "--promo-padding": spaceScaledXxl, "--promo-radius": borderRadiusContainer,
    "--promo-display": fontSizeDisplayL, "--promo-heading": fontSizeHeadingXl,
  } as React.CSSProperties}>
    <div className="promo-landing__content">
      <div className="promo-landing__brand"><Icon name="lock-private" size="medium" /> Proxima VPN</div>
      {!current ? <Box textAlign="center" padding="xxl"><Spinner size="large" /><Box>{t("user.promo.loading")}</Box></Box>
        : current.error === "network" ? <Alert type="error" header={t("user.promo.errorTitle")} action={<Button onClick={() => { setState(null); setRetry((n) => n + 1); }}>{t("user.promo.retry")}</Button>}>{t("user.promo.error")}</Alert>
        : !promo?.is_valid ? <Container header={<Header variant="h1">{t("user.promo.invalidTitle")}</Header>}><SpaceBetween size="l"><Box>{t("user.promo.invalid")}</Box><Button href="/login">{t("user.promo.login")}</Button></SpaceBetween></Container>
        : <section className="promo-landing__hero">
          <div className="promo-landing__intro">
            <span className="promo-landing__code">{promo.code}</span>
            <h1>{t("user.promo.title")}</h1>
            <p>{t("user.promo.subtitle")}</p>
          </div>
          <Container>
            <SpaceBetween size="l">
              <div className="promo-landing__benefit">{promo.type === "trial" ? t("user.promo.trialBenefit", { days: promo.trial_days }) : promo.discount_description}</div>
              {promo.plan_name && <Box fontWeight="bold">{promo.plan_name}</Box>}
              <Box>{t("user.promo.benefitHint")}</Box>
              <Button variant="primary" fullWidth href={`/login?promo=${encodeURIComponent(promo.code)}`}>{t("user.promo.claim")}</Button>
              <div className="promo-landing__trust">
                <span><Icon name="lock-private" /> {t("user.promo.secure")}</span>
                <span><Icon name="settings" /> {t("user.promo.devices")}</span>
                <span><Icon name="status-info" /> {t("user.promo.support")}</span>
              </div>
            </SpaceBetween>
          </Container>
        </section>}
      <Box textAlign="center" color="text-body-secondary">{t("user.promo.footer")}</Box>
    </div>
  </main>;
}
