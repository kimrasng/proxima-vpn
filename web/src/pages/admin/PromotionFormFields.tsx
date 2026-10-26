import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Autosuggest,
  Button,
  ColumnLayout,
  DatePicker,
  FormField,
  Input,
  Multiselect,
  type MultiselectProps,
  SegmentedControl,
  SpaceBetween,
  TimeInput,
  Toggle,
  TokenGroup,
} from "@cloudscape-design/components";
import type { ReactNode } from "react";
import { getUser, listUsers } from "../../api/admin";
import type { Plan, PromotionDiscountType, User } from "../../api/types";
import {
  splitList,
  windowFromToday,
  windowSpanDays,
  type PromotionForm,
  type PromotionFormErrors,
  type WindowUnit,
} from "./promotionFormModel";
import "./promotionForm.css";

interface PromotionFormFieldsProps {
  form: PromotionForm;
  // Omitted until the first save attempt so a fresh form is not shown as invalid.
  errors?: PromotionFormErrors;
  plans: Plan[];
  showActive: boolean;
  onChange: (patch: Partial<PromotionForm>) => void;
}

function isDiscountType(value: string): value is PromotionDiscountType {
  return value === "percent" || value === "fixed";
}

// Unambiguous characters only (no 0/O, 1/I) so a code read aloud or typed from an image still works.
function randomCode(): string {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const bytes = crypto.getRandomValues(new Uint8Array(8));
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
}

const DURATION_PRESETS = [7, 30, 90, 180, 365];

const WINDOW_PRESETS: { amount: number; unit: WindowUnit }[] = [
  { amount: 7, unit: "days" },
  { amount: 14, unit: "days" },
  { amount: 30, unit: "days" },
  { amount: 1, unit: "months" },
  { amount: 3, unit: "months" },
  { amount: 6, unit: "months" },
  { amount: 12, unit: "months" },
];

function isWindowUnit(value: string): value is WindowUnit {
  return value === "days" || value === "months";
}

function Section({ step, title, description, children }: { step: number; title: string; description: string; children: ReactNode }) {
  return (
    <section className="promo-form__section">
      <div className="promo-form__section-head">
        <span className="promo-form__step" aria-hidden="true">{step}</span>
        <div>
          <h3 className="promo-form__title">{title}</h3>
          <p className="promo-form__desc">{description}</p>
        </div>
      </div>
      <div className="promo-form__body">{children}</div>
    </section>
  );
}

export default function PromotionFormFields({
  form,
  errors = {},
  plans,
  showActive,
  onChange,
}: PromotionFormFieldsProps) {
  const { t } = useTranslation();

  const planOptions: MultiselectProps.Options = plans.map((plan) => ({
    label: plan.name,
    value: plan.id,
  }));

  const selectedPlanOptions = planOptions.filter(
    (option) => "value" in option && option.value && form.planIds.includes(option.value),
  );

  const durationList = splitList(form.durationDays);
  const userIdList = splitList(form.allowedUserIds);
  const [customDays, setCustomDays] = useState("");
  const [windowAmount, setWindowAmount] = useState("");
  const [windowUnit, setWindowUnit] = useState<WindowUnit>("days");
  const windowAmountValue = Number(windowAmount);
  const windowAmountValid =
    Number.isInteger(windowAmountValue) && windowAmountValue > 0 && windowAmountValue <= (windowUnit === "days" ? 3650 : 120);
  const applyWindow = (amount: number, unit: WindowUnit) => onChange(windowFromToday(amount, unit));
  // A preset stays highlighted only while the dates still match what it set, so a
  // hand-edited date clears it instead of showing a stale selection.
  const isPresetActive = (amount: number, unit: WindowUnit) => {
    const preset = windowFromToday(amount, unit);
    return form.validFromDate === preset.validFromDate && form.validUntilDate === preset.validUntilDate && form.validUntilTime === "23:59";
  };
  const spanDays = windowSpanDays(form.validFromDate, form.validUntilDate);
  const [userQuery, setUserQuery] = useState("");
  const [userOptions, setUserOptions] = useState<User[]>([]);
  const [userLoading, setUserLoading] = useState(false);
  // Email labels for chosen IDs; saved promotions only carry IDs, so unknown ones are looked up once.
  const [userLabels, setUserLabels] = useState<Record<string, string>>({});

  useEffect(() => {
    const query = userQuery.trim();
    if (query.length < 2) { setUserOptions([]); return; }
    let active = true;
    setUserLoading(true);
    const handle = window.setTimeout(() => {
      listUsers({ search: query, limit: 10 })
        .then((res) => { if (active) setUserOptions(res.users); })
        .catch(() => { if (active) setUserOptions([]); })
        .finally(() => { if (active) setUserLoading(false); });
    }, 250);
    return () => { active = false; window.clearTimeout(handle); };
  }, [userQuery]);

  const missingLabels = userIdList.filter((id) => !(id in userLabels)).join(",");
  useEffect(() => {
    if (!missingLabels) return;
    for (const id of missingLabels.split(",")) {
      getUser(id)
        .then((user) => setUserLabels((cur) => ({ ...cur, [id]: user.email })))
        .catch(() => setUserLabels((cur) => ({ ...cur, [id]: id })));
    }
  }, [missingLabels]);

  const setDurations = (days: number[]) =>
    onChange({ durationDays: [...new Set(days)].sort((a, b) => a - b).join(", ") });
  const toggleDuration = (days: number) => {
    const current = durationList.map(Number);
    setDurations(current.includes(days) ? current.filter((d) => d !== days) : [...current, days]);
  };
  const addCustomDays = () => {
    const days = Number(customDays);
    if (!Number.isInteger(days) || days <= 0) return;
    setDurations([...durationList.map(Number), days]);
    setCustomDays("");
  };
  const addUser = (user: User) => {
    setUserLabels((cur) => ({ ...cur, [user.id]: user.email }));
    if (!userIdList.includes(user.id)) onChange({ allowedUserIds: [...userIdList, user.id].join(", ") });
    setUserQuery("");
  };

  const value = Number(form.discountValue);
  const hasValue = form.discountValue.trim() !== "" && Number.isFinite(value) && value > 0;
  const discountText = !hasValue
    ? t("admin.promotions.summary.noDiscount")
    : form.discountType === "percent"
      ? t("admin.promotions.summary.percent", { value })
      : t("admin.promotions.summary.fixed", { value: value.toFixed(2) });
  const windowText = form.validFromDate && form.validUntilDate
    ? `${form.validFromDate.replace(/-/g, ".")} ${form.validFromTime} ~ ${form.validUntilDate.replace(/-/g, ".")} ${form.validUntilTime}`
    : t("admin.promotions.summary.noWindow");
  const durations = durationList;
  const userIds = userIdList;
  const minOrder = Number(form.minOrderDollars || "0");
  const conditions = [
    form.unlimitedRedemptions
      ? t("admin.promotions.summary.totalUnlimited")
      : t("admin.promotions.summary.total", { count: Number(form.maxRedemptions) || 0 }),
    t("admin.promotions.summary.perUser", { count: Number(form.maxRedemptionsPerUser) || 0 }),
    minOrder > 0 ? t("admin.promotions.summary.minOrder", { value: minOrder.toFixed(2) }) : null,
    form.planIds.length ? t("admin.promotions.summary.plans", { count: form.planIds.length }) : t("admin.promotions.summary.allPlans"),
    durations.length ? t("admin.promotions.summary.durations", { list: durations.join(", ") }) : null,
    userIds.length ? t("admin.promotions.summary.users", { count: userIds.length }) : null,
    form.firstPurchaseOnly ? t("admin.promotions.summary.firstOnly") : null,
  ].filter((item): item is string => !!item);

  return (
    <div className="promo-form">
      <div className="promo-form__summary" aria-live="polite">
        <div className="promo-form__summary-top">
          <code className="promo-form__code">{form.code.trim() || t("admin.promotions.summary.noCode")}</code>
          <span className="promo-form__discount">{discountText}</span>
          {showActive && (
            <span className={`promo-form__state promo-form__state--${form.isActive ? "on" : "off"}`}>
              {form.isActive ? t("admin.promotions.form.activeOn") : t("admin.promotions.form.activeOff")}
            </span>
          )}
        </div>
        <div className="promo-form__summary-window">{windowText}</div>
        <ul className="promo-form__chips">
          {conditions.map((item) => <li key={item}>{item}</li>)}
        </ul>
      </div>

      <Section step={1} title={t("admin.promotions.section.discount")} description={t("admin.promotions.section.discountDesc")}>
        <SpaceBetween size="l">
          <FormField
            stretch
            label={t("admin.promotions.form.code")}
            description={t("admin.promotions.form.codeHint")}
            errorText={errors.code}
            secondaryControl={<Button iconName="refresh" onClick={() => onChange({ code: randomCode() })}>{t("admin.promotions.form.generate")}</Button>}
          >
            <Input
              value={form.code}
              placeholder="WELCOME10"
              onChange={({ detail }) => onChange({ code: detail.value })}
            />
          </FormField>
          <ColumnLayout columns={2} minColumnWidth={220}>
            <FormField label={t("admin.promotions.form.discountType")}>
              <SegmentedControl
                selectedId={form.discountType}
                options={[
                  { id: "percent", text: t("admin.promotions.form.discountTypePercentLong") },
                  { id: "fixed", text: t("admin.promotions.form.discountTypeFixedLong") },
                ]}
                onChange={({ detail }) => {
                  if (isDiscountType(detail.selectedId)) onChange({ discountType: detail.selectedId });
                }}
              />
            </FormField>
            <FormField
              label={
                form.discountType === "percent"
                  ? t("admin.promotions.form.discountValuePercent")
                  : t("admin.promotions.form.discountValueFixed")
              }
              constraintText={
                form.discountType === "percent"
                  ? t("admin.promotions.form.discountValuePercentHint")
                  : t("admin.promotions.form.discountValueFixedHint")
              }
              errorText={errors.discountValue}
            >
              <div className="promo-form__affix">
                {form.discountType === "fixed" && <span className="promo-form__unit">$</span>}
                <Input
                  value={form.discountValue}
                  type="number"
                  placeholder={form.discountType === "percent" ? "10" : "5.00"}
                  step={form.discountType === "percent" ? 1 : 0.01}
                  inputMode={form.discountType === "percent" ? "numeric" : "decimal"}
                  onChange={({ detail }) => onChange({ discountValue: detail.value })}
                />
                {form.discountType === "percent" && <span className="promo-form__unit">%</span>}
              </div>
            </FormField>
          </ColumnLayout>
        </SpaceBetween>
      </Section>

      <Section step={2} title={t("admin.promotions.section.window")} description={t("admin.promotions.section.windowDesc")}>
        <div className="promo-window">
          <div className="promo-window__row">
            <span className="promo-window__label">{t("admin.promotions.window.fromToday")}</span>
            <div className="promo-form__chips-pick" role="group" aria-label={t("admin.promotions.window.fromToday")}>
              {WINDOW_PRESETS.map(({ amount, unit }) => {
                const on = isPresetActive(amount, unit);
                return (
                  <button
                    key={`${amount}-${unit}`}
                    type="button"
                    aria-pressed={on}
                    className={`promo-chip${on ? " promo-chip--on" : ""}`}
                    onClick={() => applyWindow(amount, unit)}
                  >
                    {unit === "months" && amount === 12
                      ? t("admin.promotions.window.oneYear")
                      : t(unit === "days" ? "admin.promotions.window.days" : "admin.promotions.window.months", { count: amount })}
                  </button>
                );
              })}
            </div>
          </div>
          <div className="promo-window__row">
            <span className="promo-window__label">{t("admin.promotions.window.custom")}</span>
            <div className="promo-window__custom">
              <span className="promo-window__text">{t("admin.promotions.window.todayPrefix")}</span>
              <span className="promo-window__amount">
                <Input
                  value={windowAmount}
                  type="number"
                  inputMode="numeric"
                  placeholder="45"
                  ariaLabel={t("admin.promotions.window.amountLabel")}
                  onChange={({ detail }) => setWindowAmount(detail.value)}
                  onKeyDown={({ detail }) => {
                    if (detail.key === "Enter" && windowAmountValid) applyWindow(windowAmountValue, windowUnit);
                  }}
                />
              </span>
              <SegmentedControl
                selectedId={windowUnit}
                label={t("admin.promotions.window.unitLabel")}
                options={[
                  { id: "days", text: t("admin.promotions.window.unitDays") },
                  { id: "months", text: t("admin.promotions.window.unitMonths") },
                ]}
                onChange={({ detail }) => {
                  if (isWindowUnit(detail.selectedId)) setWindowUnit(detail.selectedId);
                }}
              />
              <Button disabled={!windowAmountValid} onClick={() => applyWindow(windowAmountValue, windowUnit)}>
                {t("admin.promotions.window.apply")}
              </Button>
            </div>
          </div>
        </div>
        <ColumnLayout columns={2} minColumnWidth={260}>
          <FormField label={t("admin.promotions.form.validFrom")} errorText={errors.validFrom}>
            <div className="promo-form__datetime">
              <DatePicker
                value={form.validFromDate}
                placeholder="YYYY/MM/DD"
                ariaRequired
                onChange={({ detail }) => onChange({ validFromDate: detail.value })}
              />
              <TimeInput
                value={form.validFromTime}
                format="hh:mm"
                placeholder="hh:mm"
                onChange={({ detail }) => onChange({ validFromTime: detail.value })}
              />
            </div>
          </FormField>
          <FormField label={t("admin.promotions.form.validUntil")} errorText={errors.validUntil}>
            <div className="promo-form__datetime">
              <DatePicker
                value={form.validUntilDate}
                placeholder="YYYY/MM/DD"
                ariaRequired
                onChange={({ detail }) => onChange({ validUntilDate: detail.value })}
              />
              <TimeInput
                value={form.validUntilTime}
                format="hh:mm"
                placeholder="hh:mm"
                onChange={({ detail }) => onChange({ validUntilTime: detail.value })}
              />
            </div>
          </FormField>
        </ColumnLayout>
        {spanDays !== null && spanDays > 0 && (
          <p className="promo-window__span">{t("admin.promotions.window.span", { count: spanDays })}</p>
        )}
      </Section>

      <Section step={3} title={t("admin.promotions.section.limits")} description={t("admin.promotions.section.limitsDesc")}>
        <ColumnLayout columns={3} minColumnWidth={200}>
          <FormField
            label={t("admin.promotions.form.maxRedemptions")}
            constraintText={t("admin.promotions.form.maxRedemptionsHint")}
            errorText={errors.maxRedemptions}
          >
            <SpaceBetween size="xs">
              <Toggle
                checked={form.unlimitedRedemptions}
                onChange={({ detail }) =>
                  onChange({
                    unlimitedRedemptions: detail.checked,
                    maxRedemptions: detail.checked ? "" : form.maxRedemptions,
                  })
                }
              >
                {t("admin.promotions.form.unlimited")}
              </Toggle>
              {!form.unlimitedRedemptions && (
                <Input
                  value={form.maxRedemptions}
                  type="number"
                  placeholder="100"
                  inputMode="numeric"
                  onChange={({ detail }) => onChange({ maxRedemptions: detail.value })}
                />
              )}
            </SpaceBetween>
          </FormField>
          <FormField
            label={t("admin.promotions.form.maxRedemptionsPerUser")}
            constraintText={t("admin.promotions.form.maxRedemptionsPerUserHint")}
            errorText={errors.maxRedemptionsPerUser}
          >
            <Input
              value={form.maxRedemptionsPerUser}
              type="number"
              inputMode="numeric"
              onChange={({ detail }) => onChange({ maxRedemptionsPerUser: detail.value })}
            />
          </FormField>
          <FormField
            label={t("admin.promotions.form.minOrder")}
            constraintText={t("admin.promotions.form.minOrderHint")}
            errorText={errors.minOrderDollars}
          >
            <div className="promo-form__affix">
              <span className="promo-form__unit">$</span>
              <Input
                value={form.minOrderDollars}
                type="number"
                step={0.01}
                inputMode="decimal"
                onChange={({ detail }) => onChange({ minOrderDollars: detail.value })}
              />
            </div>
          </FormField>
        </ColumnLayout>
      </Section>

      <Section step={4} title={t("admin.promotions.section.target")} description={t("admin.promotions.section.targetDesc")}>
        <SpaceBetween size="l">
          <FormField
            stretch
            label={t("admin.promotions.form.planIds")}
            constraintText={t("admin.promotions.form.planIdsHint")}
          >
            <Multiselect
              selectedOptions={selectedPlanOptions}
              options={planOptions}
              placeholder={t("admin.promotions.form.allPlans")}
              onChange={({ detail }) =>
                onChange({
                  planIds: detail.selectedOptions
                    .map((option) => option.value)
                    .filter((value): value is string => !!value),
                })
              }
            />
          </FormField>
          <FormField
            stretch
            label={t("admin.promotions.form.durationDays")}
            description={durationList.length ? t("admin.promotions.form.durationPicked", { list: durationList.join(", ") }) : t("admin.promotions.form.durationAll")}
            errorText={errors.durationDays}
          >
            <div className="promo-form__chips-pick" role="group" aria-label={t("admin.promotions.form.durationDays")}>
              <button
                type="button"
                aria-pressed={durationList.length === 0}
                className={`promo-chip${durationList.length === 0 ? " promo-chip--on" : ""}`}
                onClick={() => onChange({ durationDays: "" })}
              >
                {t("admin.promotions.form.allDurations")}
              </button>
              {[...new Set([...DURATION_PRESETS, ...durationList.map(Number).filter((d) => Number.isInteger(d) && d > 0)])].sort((a, b) => a - b).map((days) => {
                const on = durationList.map(Number).includes(days);
                return (
                  <button key={days} type="button" aria-pressed={on} className={`promo-chip${on ? " promo-chip--on" : ""}`} onClick={() => toggleDuration(days)}>
                    {t("admin.promotions.form.daysChip", { count: days })}
                  </button>
                );
              })}
              <span className="promo-form__custom-days">
                <Input
                  value={customDays}
                  type="number"
                  inputMode="numeric"
                  placeholder={t("admin.promotions.form.customDays")}
                  ariaLabel={t("admin.promotions.form.customDays")}
                  onChange={({ detail }) => setCustomDays(detail.value)}
                  onKeyDown={({ detail }) => { if (detail.key === "Enter") addCustomDays(); }}
                />
                <Button iconName="add-plus" variant="icon" ariaLabel={t("admin.promotions.form.addDays")} disabled={!customDays.trim()} onClick={addCustomDays} />
              </span>
            </div>
          </FormField>

          <FormField
            stretch
            label={t("admin.promotions.form.allowedUserIds")}
            description={userIdList.length ? t("admin.promotions.form.usersPicked", { count: userIdList.length }) : t("admin.promotions.form.usersAll")}
          >
            <SpaceBetween size="xs">
              <Autosuggest
                value={userQuery}
                placeholder={t("admin.promotions.form.userSearch")}
                ariaLabel={t("admin.promotions.form.userSearch")}
                filteringType="manual"
                hideEnteredTextOption
                enteredTextLabel={(text) => text}
                statusType={userLoading ? "loading" : "finished"}
                loadingText={t("admin.promotions.form.userLoading")}
                empty={userQuery.trim().length >= 2 ? t("admin.promotions.form.userEmpty") : t("admin.promotions.form.userTypeMore")}
                options={userOptions.map((user) => ({
                  value: user.id,
                  label: user.email,
                  description: [user.name, user.plan_name].filter(Boolean).join(" · "),
                  disabled: userIdList.includes(user.id),
                }))}
                onChange={({ detail }) => setUserQuery(detail.value)}
                onSelect={({ detail }) => {
                  const picked = userOptions.find((user) => user.id === detail.value);
                  if (picked) addUser(picked);
                }}
              />
              {userIdList.length > 0 && (
                <TokenGroup
                  items={userIdList.map((id) => ({ label: userLabels[id] ?? t("admin.promotions.form.userLoading"), description: userLabels[id] && userLabels[id] !== id ? undefined : id }))}
                  onDismiss={({ detail }) => onChange({ allowedUserIds: userIdList.filter((_, index) => index !== detail.itemIndex).join(", ") })}
                />
              )}
            </SpaceBetween>
          </FormField>

          <div className="promo-form__switch-card">
            <Toggle
              checked={form.firstPurchaseOnly}
              description={t("admin.promotions.form.firstPurchaseOnlyHint")}
              onChange={({ detail }) => onChange({ firstPurchaseOnly: detail.checked })}
            >
              {t("admin.promotions.form.firstPurchaseOnly")}
            </Toggle>
          </div>
          {showActive && (
            <div className="promo-form__switch-card">
              <Toggle
                checked={form.isActive}
                description={t("admin.promotions.form.activeHint")}
                onChange={({ detail }) => onChange({ isActive: detail.checked })}
              >
                {t("admin.promotions.form.activeLabel")}
              </Toggle>
            </div>
          )}
        </SpaceBetween>
      </Section>
    </div>
  );
}
