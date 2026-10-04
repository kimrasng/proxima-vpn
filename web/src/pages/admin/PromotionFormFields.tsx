import { useTranslation } from "react-i18next";
import {
  Box,
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
} from "@cloudscape-design/components";
import type { Plan, PromotionDiscountType } from "../../api/types";
import type { PromotionForm } from "./promotionFormModel";

interface PromotionFormFieldsProps {
  form: PromotionForm;
  plans: Plan[];
  showActive: boolean;
  onChange: (patch: Partial<PromotionForm>) => void;
}

function isDiscountType(value: string): value is PromotionDiscountType {
  return value === "percent" || value === "fixed";
}

export default function PromotionFormFields({
  form,
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

  return (
    <SpaceBetween size="l">
      <ColumnLayout columns={2} minColumnWidth={220}>
        <FormField
          label={t("admin.promotions.form.code")}
          constraintText={t("admin.promotions.form.codeHint")}
        >
          <Input
            value={form.code}
            onChange={({ detail }) => onChange({ code: detail.value })}
          />
        </FormField>
        <FormField label={t("admin.promotions.form.discountType")}>
          <SegmentedControl
            selectedId={form.discountType}
            options={[
              { id: "percent", text: t("admin.promotions.form.discountTypePercent") },
              { id: "fixed", text: t("admin.promotions.form.discountTypeFixed") },
            ]}
            onChange={({ detail }) => {
              if (isDiscountType(detail.selectedId)) onChange({ discountType: detail.selectedId });
            }}
          />
        </FormField>
      </ColumnLayout>

      <ColumnLayout columns={2} minColumnWidth={220}>
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
        >
          <Input
            value={form.discountValue}
            type="number"
            step={form.discountType === "percent" ? 1 : 0.01}
            inputMode={form.discountType === "percent" ? "numeric" : "decimal"}
            onChange={({ detail }) => onChange({ discountValue: detail.value })}
          />
        </FormField>
        <FormField
          label={t("admin.promotions.form.minOrder")}
          constraintText={t("admin.promotions.form.minOrderHint")}
        >
          <Input
            value={form.minOrderDollars}
            type="number"
            step={0.01}
            inputMode="decimal"
            onChange={({ detail }) => onChange({ minOrderDollars: detail.value })}
          />
        </FormField>
      </ColumnLayout>

      <ColumnLayout columns={2} minColumnWidth={220}>
        <FormField label={t("admin.promotions.form.validFrom")}>
          <SpaceBetween direction="horizontal" size="xs">
            <DatePicker
              value={form.validFromDate}
              placeholder="YYYY/MM/DD"
              onChange={({ detail }) => onChange({ validFromDate: detail.value })}
            />
            <TimeInput
              value={form.validFromTime}
              format="hh:mm"
              placeholder="hh:mm"
              onChange={({ detail }) => onChange({ validFromTime: detail.value })}
            />
          </SpaceBetween>
        </FormField>
        <FormField label={t("admin.promotions.form.validUntil")}>
          <SpaceBetween direction="horizontal" size="xs">
            <DatePicker
              value={form.validUntilDate}
              placeholder="YYYY/MM/DD"
              onChange={({ detail }) => onChange({ validUntilDate: detail.value })}
            />
            <TimeInput
              value={form.validUntilTime}
              format="hh:mm"
              placeholder="hh:mm"
              onChange={({ detail }) => onChange({ validUntilTime: detail.value })}
            />
          </SpaceBetween>
        </FormField>
      </ColumnLayout>

      <ColumnLayout columns={2} minColumnWidth={220}>
        <FormField
          label={t("admin.promotions.form.maxRedemptions")}
          constraintText={t("admin.promotions.form.maxRedemptionsHint")}
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
            <Input
              value={form.maxRedemptions}
              type="number"
              inputMode="numeric"
              disabled={form.unlimitedRedemptions}
              onChange={({ detail }) => onChange({ maxRedemptions: detail.value })}
            />
          </SpaceBetween>
        </FormField>
        <FormField
          label={t("admin.promotions.form.maxRedemptionsPerUser")}
          constraintText={t("admin.promotions.form.maxRedemptionsPerUserHint")}
        >
          <Input
            value={form.maxRedemptionsPerUser}
            type="number"
            inputMode="numeric"
            onChange={({ detail }) => onChange({ maxRedemptionsPerUser: detail.value })}
          />
        </FormField>
      </ColumnLayout>

      <FormField
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

      <ColumnLayout columns={2} minColumnWidth={220}>
        <FormField
          label={t("admin.promotions.form.durationDays")}
          constraintText={t("admin.promotions.form.durationDaysHint")}
        >
          <Input
            value={form.durationDays}
            placeholder={t("admin.promotions.form.allDurations")}
            onChange={({ detail }) => onChange({ durationDays: detail.value })}
          />
        </FormField>
        <FormField
          label={t("admin.promotions.form.allowedUserIds")}
          constraintText={t("admin.promotions.form.allowedUserIdsHint")}
        >
          <Input
            value={form.allowedUserIds}
            placeholder={t("admin.promotions.form.allUsers")}
            onChange={({ detail }) => onChange({ allowedUserIds: detail.value })}
          />
        </FormField>
      </ColumnLayout>

      <Box>
        <FormField label={t("admin.promotions.form.firstPurchaseOnly")}>
          <Toggle
            checked={form.firstPurchaseOnly}
            onChange={({ detail }) => onChange({ firstPurchaseOnly: detail.checked })}
          >
            {t("admin.promotions.form.firstPurchaseOnlyHint")}
          </Toggle>
        </FormField>
      </Box>

      {showActive && (
        <FormField label={t("admin.promotions.form.active")}>
          <Toggle
            checked={form.isActive}
            onChange={({ detail }) => onChange({ isActive: detail.checked })}
          >
            {form.isActive
              ? t("admin.promotions.form.activeOn")
              : t("admin.promotions.form.activeOff")}
          </Toggle>
        </FormField>
      )}
    </SpaceBetween>
  );
}
