import { useTranslation } from "react-i18next";
import { Alert, Badge, Box, Button, ColumnLayout, Container, FormField, Select, SpaceBetween, StatusIndicator } from "@cloudscape-design/components";
import type { PromoLandingInfo } from "../api/promotionKinds";
import { formatPriceCents } from "../utils/planPricing";
import "./PromoOffer.css";

// Prices are filtered for this campaign by the public landing endpoint.
type OfferInfo = PromoLandingInfo & {
  readonly max_discount_cents?: number;
};

function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(units.length - 1, Math.max(0, Math.floor(Math.log(bytes) / Math.log(1024))));
  const value = bytes / 1024 ** index;
  return `${Number.isInteger(value) ? value : value.toFixed(1)} ${units[index]}`;
}

export function PromoOffer({ promo, durationDays, showCampaign = true, onDurationChange, onAccept, onDismiss }: {
  readonly promo: OfferInfo;
  readonly durationDays?: number;
  readonly showCampaign?: boolean;
  readonly onDurationChange: (days: number) => void;
  readonly onAccept: (days: number) => void;
  readonly onDismiss: () => void;
}) {
  const { t, i18n } = useTranslation();
  const plan = promo.plan;
  if (!plan) return null;
  const trial = promo.type === "trial";
  const prices = plan.prices?.length ? plan.prices : [{
    duration_days: plan.duration_days, price_cents: plan.price_cents,
  }];
  const selected = prices.find((price) => price.duration_days === durationDays)
    ?? prices.find((price) => price.duration_days === plan.duration_days)
    ?? prices[0];
  const baseCents = selected?.price_cents ?? plan.price_cents;
  const days = selected?.duration_days ?? plan.duration_days;
  const rawDiscount = promo.discount_type === "percent"
    ? Math.floor(baseCents * (promo.discount_value ?? 0) / 100)
    : promo.discount_type === "fixed" ? promo.discount_value ?? 0 : 0;
  const discount = Math.min(baseCents, promo.max_discount_cents ? Math.min(rawDiscount, promo.max_discount_cents) : rawDiscount);
  const finalCents = trial ? 0 : Math.max(0, baseCents - discount);
  const stats = [
    { label: t("user.plan.trafficLimit"), value: plan.traffic_limit ? formatBytes(plan.traffic_limit) : t("user.plan.unlimited") },
    { label: t("user.plan.speedLimit"), value: plan.speed_limit ? `${plan.speed_limit} Mbps` : t("user.plan.unlimited") },
    { label: t("user.plan.maxDevices"), value: String(plan.max_devices) },
  ];
  const canAccept = promo.is_valid && promo.is_eligible !== false;
  const campaignName = promo.headline || promo.code;
  const date = promo.valid_until ? new Date(promo.valid_until).toLocaleString(i18n.resolvedLanguage ?? i18n.language, {
    year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
  }) : "";

  const options = prices.map((price) => ({
    value: String(price.duration_days),
    label: `${t("user.plan.daysLabel", { days: price.duration_days })} · ${formatPriceCents(price.price_cents)}`,
  }));
  const content = <SpaceBetween size="s">
    {!canAccept && <Alert type="info">{t("user.promo.error.ineligible")}</Alert>}
    <SpaceBetween size="xs">
      {showCampaign && <Box variant="small" color="text-body-secondary">{campaignName}</Box>}
      <SpaceBetween direction="horizontal" size="s" alignItems="center">
        <Box variant="h2" fontSize="heading-m" fontWeight="normal" padding="n">{plan.name}</Box>
        {trial ? <Badge color="blue">{t("user.promo.trialBenefit", { days: promo.trial_days })}</Badge>
          : promo.discount_type === "percent" && finalCents < baseCents
            ? <Badge color="blue">{t("user.promo.offer.discountBadge", { value: promo.discount_value })}</Badge>
            : null}
      </SpaceBetween>
      {promo.description && <Box color="text-body-secondary">{promo.description}</Box>}
      <SpaceBetween direction="horizontal" size="s" alignItems="center">
        {finalCents < baseCents && <Box color="text-body-secondary"><s>{formatPriceCents(baseCents)}</s></Box>}
        <Box fontSize="heading-l" fontWeight="normal">{formatPriceCents(finalCents)}</Box>
        <Box variant="small" color="text-body-secondary">
          {trial ? t("user.promo.offer.trialPeriod", { days: promo.trial_days }) : t("user.plan.perDays", { days })}
        </Box>
      </SpaceBetween>
      {trial && promo.requires_card === true && <Box variant="small" color="text-body-secondary">
        {t("user.promo.offer.afterTrial", { price: formatPriceCents(baseCents), days })}
      </Box>}
    </SpaceBetween>
    {!trial && prices.length > 1 && <FormField label={t("user.plan.order.chooseDuration")} stretch>
      <Select options={options} selectedOption={options.find((option) => option.value === String(days)) ?? null}
        onChange={({ detail }) => onDurationChange(Number(detail.selectedOption.value))} />
    </FormField>}
    {!trial && <Box variant="small" color="text-body-secondary">{t("user.promo.offer.termScope", { days })}</Box>}
    {plan.features.length > 0 && <SpaceBetween size="xxs">
      {plan.features.map((feature, index) => <StatusIndicator key={index} type={feature.included ? "success" : "stopped"}>
        {feature.text}
      </StatusIndicator>)}
    </SpaceBetween>}
    <ColumnLayout columns={3} minColumnWidth={80}>
      {stats.map((stat) => <div key={stat.label}>
        <Box variant="small" color="text-body-secondary">{stat.label}</Box>
        <Box>{stat.value}</Box>
      </div>)}
    </ColumnLayout>
    <SpaceBetween direction="horizontal" size="xs">
      {canAccept && <Button variant="primary" disabled={!selected} onClick={() => onAccept(days)}>
        {promo.cta_label || t(trial ? "user.promo.cta.trial" : "user.promo.cta.discount")}
      </Button>}
      <Button variant="link" onClick={onDismiss}>{t("user.promo.notNow")}</Button>
    </SpaceBetween>
    <SpaceBetween size="xxs">
      {trial ? <>
        {promo.requires_card === true && <Box variant="small" color="text-body-secondary">{t("user.promo.offer.cardRequired")}</Box>}
        <Box variant="small" color="text-body-secondary">{t(promo.requires_card === true ? "user.promo.offer.autoRenew" : "user.promo.offer.noAutoRenew", {
          price: formatPriceCents(baseCents), days,
        })}</Box>
      </> : <Box variant="small" color="text-body-secondary">{t("user.promo.terms")}</Box>}
      {date && <Box variant="small" color="text-body-secondary">{t("user.promo.until", { date })}</Box>}
    </SpaceBetween>
  </SpaceBetween>;
  return <div className="promo-offer">{showCampaign ? <Container>{content}</Container> : content}</div>;
}
