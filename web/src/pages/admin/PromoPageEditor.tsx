import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert, Box, Button, ColumnLayout, ExpandableSection, FormField, Input, Link,
  Modal, Multiselect, Select, SpaceBetween, StatusIndicator, Tabs, Textarea,
} from "@cloudscape-design/components";
import { listPlans } from "../../api/admin";
import { ApiError } from "../../api/client";
import { getPromoPage, savePromoPage, type PromoPage } from "../../api/promotionKinds";
import type { Plan } from "../../api/types";
import { adminError } from "../../utils/adminError";
import "./promoPageEditor.css";

const empty: PromoPage = { headline: "", description: "", cta_label: "", plan_id: null, countries: [], browsers: [], ip_cidrs: [], email_domains: [] };
// ISO 3166-1 alpha-2, rather than all two-letter combinations accepted by Intl.
const countryCodes = (
  "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ " +
  "CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR " +
  "GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP " +
  "KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ " +
  "NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW " +
  "SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ " +
  "UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW"
).split(" ");
const browserNames = ["chrome", "safari", "firefox", "edge", "unknown"] as const;
const textLimits = { headline: 80, description: 300, cta_label: 30 } as const;
const ruleFields = ["countries", "browsers", "ip_cidrs", "email_domains"] as const;
type FieldErrors = Partial<Record<keyof PromoPage, string>>;

const validationKeys = {
  "countries must be two-letter country codes": { field: "countries", key: "countryError" },
  "unsupported browser": { field: "browsers", key: "browserError" },
  "invalid IP or CIDR": { field: "ip_cidrs", key: "ipError" },
  "invalid email domain": { field: "email_domains", key: "emailError" },
  "invalid plan_id": { field: "plan_id", key: "planError" },
} as const satisfies Record<string, { field: keyof PromoPage; key: string }>;

function splitRules(value: string): string[] {
  return value.split(/[,\n]/).map((item) => item.trim()).filter(Boolean);
}

export default function PromoPageEditor({ code, fixedPlan, onClose }: {
  readonly code: string | null;
  readonly fixedPlan?: boolean;
  readonly onClose: () => void;
}) {
  const { t, i18n } = useTranslation();
  const [page, setPage] = useState<PromoPage>(empty);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loadedCode, setLoadedCode] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<{ cause: unknown } | null>(null);
  const [reload, setReload] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [errors, setErrors] = useState<FieldErrors>({});
  const [activeTab, setActiveTab] = useState("content");
  const [advanced, setAdvanced] = useState(false);
  const ready = code !== null && loadedCode === code && !loading && !loadError;

  useEffect(() => {
    if (code === null) return;
    let active = true;
    setLoading(true);
    setLoadedCode(null);
    setLoadError(null);
    setSaveError("");
    setErrors({});
    setActiveTab("content");
    setAdvanced(false);
    void Promise.all([getPromoPage(code), fixedPlan ? Promise.resolve([]) : listPlans()]).then(
      ([loaded, planList]) => {
        if (!active) return;
        setPage({
          ...loaded,
          countries: loaded.countries ?? [],
          browsers: loaded.browsers ?? [],
          ip_cidrs: loaded.ip_cidrs ?? [],
          email_domains: loaded.email_domains ?? [],
        });
        setPlans(planList);
        setLoadedCode(code);
      },
      (cause: unknown) => { if (active) setLoadError({ cause }); },
    ).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [code, fixedPlan, reload]);

  const language = i18n.resolvedLanguage ?? i18n.language;
  const countryOptions = useMemo(() => {
    const names = new Intl.DisplayNames([language], { type: "region" });
    const options = countryCodes.map((value) => ({ value, label: `${names.of(value)} (${value})` }));
    options.sort((a, b) => a.label.localeCompare(b.label, language));
    return [...options, ...page.countries.filter((value) => !countryCodes.includes(value))
      .map((value) => ({ value, label: value }))];
  }, [language, page.countries]);
  const browserOptions = [
    ...browserNames.map((value) => ({ value, label: t(`admin.promotions.page.browser.${value}`) })),
    ...page.browsers.filter((value) => !browserNames.some((name) => name === value))
      .map((value) => ({ value, label: value })),
  ];

  const update = (patch: Partial<PromoPage>) => {
    setPage((current) => ({ ...current, ...patch }));
    setErrors((current) => {
      const next = { ...current };
      for (const field of ["headline", "description", "cta_label", "plan_id", ...ruleFields] as const) {
        if (field in patch) delete next[field];
      }
      return next;
    });
  };

  const revealErrors = (next: FieldErrors) => {
    setErrors(next);
    const eligibilityError = ruleFields.some((field) => next[field]);
    setActiveTab(eligibilityError ? "eligibility" : "content");
    if (next.ip_cidrs || next.email_domains) setAdvanced(true);
  };

  const save = async () => {
    if (!code || !ready || saving) return;
    setSaveError("");
    const payload: PromoPage = {
      ...page,
      headline: page.headline.trim(),
      description: page.description.trim(),
      cta_label: page.cta_label.trim(),
      countries: page.countries.map((value) => value.trim()).filter(Boolean),
      browsers: page.browsers.map((value) => value.trim()).filter(Boolean),
      ip_cidrs: splitRules(page.ip_cidrs.join("\n")),
      email_domains: splitRules(page.email_domains.join("\n")),
    };
    const next: FieldErrors = {};
    for (const field of ["headline", "description", "cta_label"] as const) {
      if (Array.from(payload[field]).length > textLimits[field]) {
        next[field] = t("admin.promotions.page.textLimit", { count: textLimits[field] });
      }
    }
    for (const field of ruleFields) {
      if (payload[field].length > 100) next[field] = t("admin.promotions.page.ruleLimit");
    }
    if (Object.keys(next).length > 0) {
      revealErrors(next);
      setSaveError(t("admin.promotions.page.validationError"));
      return;
    }
    setErrors({});
    setSaving(true);
    try {
      await savePromoPage(code, payload);
      onClose();
    } catch (cause) {
      const message = adminError(cause, "");
      const known = Object.entries(validationKeys).find(([value]) => value === message)?.[1];
      if (cause instanceof ApiError && cause.status === 400 && known) {
        revealErrors({ [known.field]: t(`admin.promotions.page.${known.key}`) });
        setSaveError(t("admin.promotions.page.validationError"));
      } else if (cause instanceof ApiError && cause.status === 400 && message === "text too long") {
        revealErrors(Object.fromEntries(Object.entries(textLimits).map(([field, count]) =>
          [field, t("admin.promotions.page.textLimit", { count })])));
        setSaveError(t("admin.promotions.page.validationError"));
      } else if (cause instanceof ApiError && cause.status === 400 && message === "too many eligibility rules") {
        revealErrors(Object.fromEntries(ruleFields.map((field) => [field, t("admin.promotions.page.ruleLimit")])));
        setSaveError(t("admin.promotions.page.validationError"));
      } else {
        setSaveError(adminError(cause, t("admin.promotions.page.saveError")));
      }
    } finally {
      setSaving(false);
    }
  };

  const label = (key: string) => <span className="promo-page-editor__label">{t(`admin.promotions.page.${key}`)}</span>;
  const autoOption = { value: "", label: t("admin.promotions.page.planAuto") };
  const planOptions = [autoOption, ...plans.map((plan) => ({ value: plan.id, label: plan.name }))];
  const selectedPlan = planOptions.find((option) => option.value === (page.plan_id ?? "")) ??
    { value: page.plan_id ?? "", label: page.plan_id ?? autoOption.label };
  const missingEndpoint = loadError?.cause instanceof ApiError &&
    (loadError.cause.status === 404 || loadError.cause.status === 405);
  const previewUrl = code ? `/p/${encodeURIComponent(code)}` : "";

  return (
    <Modal visible={code !== null} size="large" className="promo-page-editor"
      closeAriaLabel={t("admin.promotions.page.cancel")}
      onDismiss={() => { if (!saving) onClose(); }}
      header={<SpaceBetween size="xxs">
        <Box variant="h2" fontSize="heading-m" fontWeight="normal" margin="n">{t("admin.promotions.page.title")}</Box>
        <Box variant="small" color="text-body-secondary"><span className="promo-page-editor__code">{code}</span></Box>
      </SpaceBetween>}
      footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
        <Button variant="link" disabled={saving} onClick={onClose}>{t("admin.promotions.page.cancel")}</Button>
        <Button variant="primary" loading={saving} disabled={!ready} onClick={() => void save()}>{t("admin.promotions.page.save")}</Button>
      </SpaceBetween></Box>}>
      {loading ? <StatusIndicator type="loading">{t("admin.promotions.page.loading")}</StatusIndicator>
        : loadError ? <Alert type="error" header={t("admin.promotions.page.loadError")}
          action={<Button onClick={() => setReload((value) => value + 1)}>{t("admin.promotions.page.retry")}</Button>}>
          {missingEndpoint ? t("admin.promotions.page.loadUnavailable")
            : adminError(loadError.cause, t("admin.promotions.page.loadErrorHint"))}
        </Alert>
          : ready && <SpaceBetween size="m">
            {saveError && <Alert type="error" header={t("admin.promotions.page.saveErrorTitle")}>{saveError}</Alert>}
            <Tabs activeTabId={activeTab} onChange={({ detail }) => setActiveTab(detail.activeTabId)} tabs={[
              {
                id: "content", label: t("admin.promotions.page.contentTab"),
                content: <SpaceBetween size="m">
                  <Box color="text-body-secondary">{t("admin.promotions.page.contentHint")}</Box>
                  <FormField stretch label={label("headline")} constraintText={t("admin.promotions.page.headlineHint")} errorText={errors.headline}>
                    <Input value={page.headline} disabled={saving} placeholder={t("admin.promotions.page.headlinePlaceholder")}
                      onChange={({ detail }) => update({ headline: detail.value })} />
                  </FormField>
                  <FormField stretch label={label("description")} constraintText={t("admin.promotions.page.descriptionHint")} errorText={errors.description}>
                    <Textarea value={page.description} disabled={saving} rows={3} placeholder={t("admin.promotions.page.descriptionPlaceholder")}
                      onChange={({ detail }) => update({ description: detail.value })} />
                  </FormField>
                  <ColumnLayout columns={2}>
                    <FormField stretch label={label("cta")} constraintText={t("admin.promotions.page.ctaHint")} errorText={errors.cta_label}>
                      <Input value={page.cta_label} disabled={saving} placeholder={t("admin.promotions.page.ctaPlaceholder")}
                        onChange={({ detail }) => update({ cta_label: detail.value })} />
                    </FormField>
                    <FormField stretch label={label("plan")} constraintText={t(fixedPlan ? "admin.promotions.page.fixedPlanHint" : "admin.promotions.page.planHint")}
                      errorText={errors.plan_id}>
                      {fixedPlan ? <Box>{t("admin.promotions.page.fixedPlan")}</Box>
                        : <Select selectedOption={selectedPlan} options={planOptions} disabled={saving}
                          expandToViewport filteringType="auto" filteringPlaceholder={t("admin.promotions.page.search")}
                          filteringAriaLabel={t("admin.promotions.page.search")} noMatch={t("admin.promotions.page.noMatches")}
                          onChange={({ detail }) => update({ plan_id: detail.selectedOption.value || null })} />}
                    </FormField>
                  </ColumnLayout>
                  {fixedPlan && <Alert type="info">{t("admin.promotions.page.renewalHint")}</Alert>}
                </SpaceBetween>,
              },
              {
                id: "eligibility", label: t("admin.promotions.page.eligibilityTab"),
                content: <SpaceBetween size="m">
                  <Box color="text-body-secondary">{t("admin.promotions.page.eligibilityHint")}</Box>
                  <FormField stretch label={label("countries")} constraintText={t("admin.promotions.page.countriesHint")} errorText={errors.countries}>
                    <Multiselect options={countryOptions} selectedOptions={page.countries.map((value) =>
                      countryOptions.find((option) => option.value === value) ?? { value, label: value })}
                      disabled={saving} filteringType="auto" expandToViewport
                      placeholder={t("admin.promotions.page.unrestricted")} filteringPlaceholder={t("admin.promotions.page.search")}
                      filteringAriaLabel={t("admin.promotions.page.search")} noMatch={t("admin.promotions.page.noMatches")}
                      deselectAriaLabel={(option) => t("admin.promotions.page.removeSelection", { name: option.label })}
                      onChange={({ detail }) => update({ countries: detail.selectedOptions.flatMap((option) => option.value ? [option.value] : []) })} />
                  </FormField>
                  <FormField stretch label={label("browsers")} constraintText={t("admin.promotions.page.browsersHint")} errorText={errors.browsers}>
                    <Multiselect options={browserOptions} selectedOptions={page.browsers.map((value) =>
                      browserOptions.find((option) => option.value === value) ?? { value, label: value })}
                      disabled={saving} filteringType="auto" expandToViewport
                      placeholder={t("admin.promotions.page.unrestricted")} filteringPlaceholder={t("admin.promotions.page.search")}
                      filteringAriaLabel={t("admin.promotions.page.search")} noMatch={t("admin.promotions.page.noMatches")}
                      deselectAriaLabel={(option) => t("admin.promotions.page.removeSelection", { name: option.label })}
                      onChange={({ detail }) => update({ browsers: detail.selectedOptions.flatMap((option) => option.value ? [option.value] : []) })} />
                  </FormField>
                  <ExpandableSection expanded={advanced} onChange={({ detail }) => setAdvanced(detail.expanded)}
                    headerText={label("advanced")}>
                    <SpaceBetween size="m">
                      <FormField stretch label={label("ip_cidrs")} constraintText={t("admin.promotions.page.ip_cidrsHint")} errorText={errors.ip_cidrs}>
                        <Textarea rows={3} value={page.ip_cidrs.join("\n")} disabled={saving}
                          placeholder={t("admin.promotions.page.ipPlaceholder")}
                          onChange={({ detail }) => update({ ip_cidrs: detail.value.split("\n") })} />
                      </FormField>
                      <FormField stretch label={label("email_domains")} constraintText={t("admin.promotions.page.email_domainsHint")} errorText={errors.email_domains}>
                        <Textarea rows={2} value={page.email_domains.join("\n")} disabled={saving}
                          placeholder={t("admin.promotions.page.emailPlaceholder")}
                          onChange={({ detail }) => update({ email_domains: detail.value.split("\n") })} />
                      </FormField>
                    </SpaceBetween>
                  </ExpandableSection>
                </SpaceBetween>,
              },
            ]} />
            {previewUrl && <Link href={previewUrl} external>{t("admin.promotions.page.preview")}</Link>}
          </SpaceBetween>}
    </Modal>
  );
}
