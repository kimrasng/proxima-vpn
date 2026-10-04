import { useTranslation } from "react-i18next";
import {
  AttributeEditor,
  Badge,
  Box,
  Button,
  Checkbox,
  ColumnLayout,
  Container,
  Header,
  ExpandableSection,
  FormField,
  Input,
  Select,
  SpaceBetween,
  Tabs,
} from "@cloudscape-design/components";
import PlanDurationInput from "./PlanDurationInput";
import type { NodeGroup } from "../../api/types";
import { featureLanguages, type PlanForm, type PriceRow, type FeatureRow } from "./planFormModel";

interface PlanFormFieldsProps {
  editing?: boolean;
  form: PlanForm;
  nodeGroups: NodeGroup[];
  onChange: (patch: Partial<PlanForm>) => void;
  onFormUpdate: (update: (form: PlanForm) => PlanForm) => void;
}

export default function PlanFormFields({
  editing = false,
  form,
  nodeGroups,
  onChange,
  onFormUpdate,
}: PlanFormFieldsProps) {
  const { t } = useTranslation();

  const groupOptions = nodeGroups.map((g) => ({ label: g.name, value: g.id }));

  const updatePriceRow = (index: number, patch: Partial<PriceRow>) =>
    onFormUpdate((f) => ({
      ...f,
      prices: f.prices.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    }));

  const updateFeatureRow = (index: number, patch: Partial<FeatureRow>) =>
    onFormUpdate((f) => ({
      ...f,
      features: f.features.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    }));

  // Array position is the persisted display order.
  const moveFeatureRow = (index: number, direction: -1 | 1) =>
    onFormUpdate((f) => {
      const target = index + direction;
      if (target < 0 || target >= f.features.length) return f;
      const features = [...f.features];
      const moved = features[index];
      const displaced = features[target];
      if (!moved || !displaced) return f;
      features[index] = displaced;
      features[target] = moved;
      return { ...f, features };
    });

  return (
    <SpaceBetween size="l">
      <Container header={<Header variant="h2">{t("admin.plans.editor.basics")}</Header>}>
      <SpaceBetween size="m">
      <ColumnLayout columns={2} minColumnWidth={220}>
        <FormField label={t("admin.plans.form.name")}>
          <Input value={form.name} onChange={({ detail }) => onChange({ name: detail.value })} />
        </FormField>
        <FormField label={t("admin.plans.form.nodeGroup")}>
          <Select
            selectedOption={groupOptions.find((o) => o.value === form.node_group_id) ?? null}
            options={groupOptions}
            onChange={({ detail }) => onChange({ node_group_id: detail.selectedOption.value ?? "" })}
          />
        </FormField>
      </ColumnLayout>

      <FormField label={t("admin.plans.editor.planId")} constraintText={t(editing ? "admin.plans.editor.idImmutable" : "admin.plans.editor.idHint")}>
        <Input value={form.id} disabled={editing} onChange={({ detail }) => onChange({ id: detail.value })} />
      </FormField>
      <Checkbox checked={form.advertise} onChange={({ detail }) => onChange({ advertise: detail.checked })} description={t("admin.plans.editor.advertiseHint")}>{t("admin.plans.editor.advertise")}</Checkbox>
      <Checkbox checked={form.is_active} onChange={({ detail }) => onChange({ is_active: detail.checked })}>{t("admin.plans.form.active")}</Checkbox>
      </SpaceBetween>
      </Container>

      <Container header={<Header variant="h2">{t("admin.plans.editor.limits")}</Header>}>
      <ColumnLayout columns={2} minColumnWidth={200}>
        <FormField
          label={t("admin.plans.form.trafficLimit")}
          constraintText={t("admin.plans.form.trafficHint")}
        >
          <Input
            value={form.traffic_limit}
            type="number"
            onChange={({ detail }) => onChange({ traffic_limit: detail.value })}
          />
        </FormField>
        <FormField label={t("admin.plans.editor.assignmentDuration")} constraintText={`${t("admin.plans.editor.assignmentHint")} ${t("admin.plans.editor.durationHint")}`}>
          <PlanDurationInput days={form.duration_days} onChange={duration_days => onChange({ duration_days })} />
        </FormField>
        <FormField
          label={t("admin.plans.form.speedLimit")}
          constraintText={t("admin.plans.form.speedHint")}
        >
          <Input
            value={form.speed_limit}
            type="number"
            onChange={({ detail }) => onChange({ speed_limit: detail.value })}
          />
        </FormField>
        <FormField label={t("admin.plans.form.maxDevices")} constraintText={t("admin.plans.form.maxDevicesHint")}>
          <Input
            value={form.max_devices}
            type="number"
            onChange={({ detail }) => onChange({ max_devices: detail.value })}
          />
        </FormField>
        <Box>
          <FormField
            label={t("admin.plans.form.maxConcurrent")}
            constraintText={t("admin.plans.form.maxConcurrentHint")}
          >
            <Input
              value={form.max_concurrent}
              type="number"
              placeholder={form.max_devices}
              onChange={({ detail }) => onChange({ max_concurrent: detail.value })}
            />
          </FormField>
        </Box>
      </ColumnLayout>
      <Box variant="small" color="text-body-secondary">{t("admin.plans.form.observationHint")}</Box>

      </Container>

      {/* Pricing stays visible while editing alongside the live preview. */}
      <ExpandableSection
        variant="container"
        defaultExpanded
        headerText={t("admin.plans.pricing.title")}
        headerDescription={t("admin.plans.pricing.hint")}
        headerCounter={
          form.prices.length > 0 ? `(${form.prices.length})` : undefined
        }
      >
        <SpaceBetween size="m">
        <Box color="text-body-secondary">{t("admin.plans.editor.durationHint")}</Box>
        <AttributeEditor
          items={form.prices}
          addButtonText={t("admin.plans.pricing.addRow")}
          removeButtonText={t("admin.plans.pricing.removeRow")}
          empty={t("admin.plans.pricing.empty")}
          onAddButtonClick={() =>
            onFormUpdate((f) => ({
              ...f,
              prices: [...f.prices, { durationDays: "", priceDollars: "" }],
            }))
          }
          onRemoveButtonClick={({ detail }) =>
            onFormUpdate((f) => ({
              ...f,
              prices: f.prices.filter((_, i) => i !== detail.itemIndex),
            }))
          }
          definition={[
            {
              label: t("admin.plans.editor.duration"),
              control: (item: PriceRow, index) => (
                <PlanDurationInput days={item.durationDays} onChange={durationDays => updatePriceRow(index, { durationDays })} />
              ),
            },
            {
              label: t("admin.plans.pricing.priceLabel"),
              control: (item: PriceRow, index) => (
                <Input
                  value={item.priceDollars}
                  type="number"
                  step={0.01}
                  inputMode="decimal"
                  placeholder={t("admin.plans.pricing.priceHint")}
                  onChange={({ detail }) => updatePriceRow(index, { priceDollars: detail.value })}
                />
              ),
            },
          ]}
        />
        </SpaceBetween>
      </ExpandableSection>

      <ExpandableSection
        variant="container"
        defaultExpanded
        headerText={t("admin.plans.features.title")}
        headerDescription={t("admin.plans.features.hint")}
        headerCounter={
          form.features.length > 0 ? `(${form.features.length})` : undefined
        }
        headerActions={
          <Button
            iconName="add-plus"
            onClick={() =>
              onFormUpdate((f) => ({
                ...f,
                features: [...f.features, { included: true, text: {} }],
              }))
            }
          >
            {t("admin.plans.features.addRow")}
          </Button>
        }
      >
        {form.features.length === 0 ? (
          <Box color="text-status-inactive">{t("admin.plans.features.empty")}</Box>
        ) : (
          <SpaceBetween size="m">
            {form.features.map((row, index) => (
              <FeatureEditor
                key={index}
                row={row}
                index={index}
                total={form.features.length}
                onUpdate={updateFeatureRow}
                onMove={moveFeatureRow}
                onRemove={(i) =>
                  onFormUpdate((f) => ({
                    ...f,
                    features: f.features.filter((_, x) => x !== i),
                  }))
                }
              />
            ))}
          </SpaceBetween>
        )}
      </ExpandableSection>
    </SpaceBetween>
  );
}

interface FeatureEditorProps {
  row: FeatureRow;
  index: number;
  total: number;
  onUpdate: (index: number, patch: Partial<FeatureRow>) => void;
  onMove: (index: number, direction: -1 | 1) => void;
  onRemove: (index: number) => void;
}

// One row per bullet. The three translations sit behind tabs instead of three
// stacked inputs, which is what made the section unusable past a few bullets;
// the per-tab badge is how you tell which languages are still empty without
// opening each one.
function FeatureEditor({ row, index, total, onUpdate, onMove, onRemove }: FeatureEditorProps) {
  const { t } = useTranslation();

  return (
    <Box>
      <SpaceBetween size="xs">
        <SpaceBetween direction="horizontal" size="xs" alignItems="center">
          <Box variant="awsui-key-label">
            {t("admin.plans.features.rowLabel", { position: index + 1 })}
          </Box>
          <Checkbox
            checked={row.included}
            onChange={({ detail }) => onUpdate(index, { included: detail.checked })}
          >
            {t("admin.plans.features.includedLabel")}
          </Checkbox>
          <Button
            variant="inline-icon"
            iconName="angle-up"
            ariaLabel={t("admin.plans.features.moveUp")}
            disabled={index === 0}
            onClick={() => onMove(index, -1)}
          />
          <Button
            variant="inline-icon"
            iconName="angle-down"
            ariaLabel={t("admin.plans.features.moveDown")}
            disabled={index === total - 1}
            onClick={() => onMove(index, 1)}
          />
          <Button
            variant="inline-icon"
            iconName="remove"
            ariaLabel={t("admin.plans.features.removeRow")}
            onClick={() => onRemove(index)}
          />
        </SpaceBetween>
        <Tabs
          tabs={featureLanguages.map((code) => {
            const filled = (row.text[code] ?? "").trim().length > 0;
            return {
              id: code,
              label: (
                <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
                  <span>{t(`admin.plans.features.langTab.${code}`)}</span>
                  <Badge color={filled ? "green" : "grey"}>
                    {filled
                      ? t("admin.plans.features.langFilled")
                      : t("admin.plans.features.langEmpty")}
                  </Badge>
                </SpaceBetween>
              ),
              content: (
                <FormField label={t(`admin.plans.features.textLang.${code}`)}>
                  <Input
                    value={row.text[code] ?? ""}
                    placeholder={t("admin.plans.features.textPlaceholder")}
                    onChange={({ detail }) =>
                      onUpdate(index, { text: { ...row.text, [code]: detail.value } })
                    }
                  />
                </FormField>
              ),
            };
          })}
        />
      </SpaceBetween>
    </Box>
  );
}
