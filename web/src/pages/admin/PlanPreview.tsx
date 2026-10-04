import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Box, Container, FormField, Header, Icon, Select, SpaceBetween, StatusIndicator } from "@cloudscape-design/components";
import { featureLanguages, type PlanForm } from "./planFormModel";
import { formatPriceCents, planSavings } from "../../utils/planPricing";

export default function PlanPreview({ form }: { form: PlanForm }) {
  const { t, i18n } = useTranslation();
  const [language, setLanguage] = useState(i18n.language.split("-")[0] || "en");
  const [duration, setDuration] = useState<string | null>(null);
  const prices = form.prices.filter(row => row.durationDays.trim() && row.priceDollars.trim() && Number.isSafeInteger(Number(row.durationDays)) && Number(row.durationDays) > 0 && Number.isFinite(Number(row.priceDollars)) && Number(row.priceDollars) >= 0).map(row => ({ duration_days: Number(row.durationDays), price_cents: Math.round(Number(row.priceDollars) * 100) }));
  const selected = prices.find(p => String(p.duration_days) === duration) ?? prices[0];
  const savings = selected ? planSavings(prices, selected.duration_days) : null;
  const features = form.features.map(row => ({ ...row, label: row.text[language]?.trim() || row.text.en?.trim() })).filter(row => row.label);
  return (
    <Container header={<Header variant="h2" description={t("admin.plans.editor.previewHint")}>{t("admin.plans.editor.preview")}</Header>}>
      <SpaceBetween size="l">
        <FormField label={t("admin.plans.editor.language")} constraintText={t("admin.plans.editor.languageHint")}>
          <Select selectedOption={{ value: language, label: t(`admin.plans.features.langTab.${language}`) }} options={featureLanguages.map(code => ({ value: code, label: t(`admin.plans.features.langTab.${code}`) }))} onChange={({ detail }) => setLanguage(detail.selectedOption.value ?? "en")} />
        </FormField>
        <Box color="text-body-secondary">{t("admin.plans.editor.previewStatus")}</Box>
        <StatusIndicator type={form.is_active ? "success" : "stopped"}>{t(`admin.plans.editor.${form.is_active ? "active" : "inactive"}`)}</StatusIndicator>
        <Header variant="h2">{form.name.trim() || t("admin.plans.editor.unnamed")}</Header>
        {selected ? <SpaceBetween size="s">
          <FormField label={t("admin.plans.editor.duration")}>
            <Select selectedOption={{ value: String(selected.duration_days), label: `${selected.duration_days} ${t("user.plan.durationDays")}` }} options={prices.map(p => ({ value: String(p.duration_days), label: `${p.duration_days} ${t("user.plan.durationDays")} · ${formatPriceCents(p.price_cents)}` }))} onChange={({ detail }) => setDuration(detail.selectedOption.value ?? null)} />
          </FormField>
          <Box variant="h1">{formatPriceCents(selected.price_cents)}</Box>
          {savings && <Badge color="green">{t("user.plan.order.savings", { percent: savings.percent })}</Badge>}
        </SpaceBetween> : <Box color="text-body-secondary">{t("admin.plans.editor.noPrices")}</Box>}
        <SpaceBetween size="s">
          <Box><Box variant="awsui-key-label">{t("admin.plans.editor.monthlyTraffic")}</Box>{Number(form.traffic_limit) > 0 ? `${form.traffic_limit} GB` : "∞"}</Box>
          <Box><Box variant="awsui-key-label">{t("admin.plans.form.maxDevices")}</Box>{form.max_devices || "—"}</Box>
          <Box><Box variant="awsui-key-label">{t("admin.plans.form.speedLimit")}</Box>{Number(form.speed_limit) > 0 ? `${form.speed_limit} Mbps` : "∞"}</Box>
          {features.map((row, index) => <SpaceBetween key={index} direction="horizontal" size="xs"><Icon name={row.included ? "status-positive" : "status-negative"} /><span>{row.label}</span></SpaceBetween>)}
        </SpaceBetween>
      </SpaceBetween>
    </Container>
  );
}
