import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Select, SpaceBetween } from "@cloudscape-design/components";

type Unit = "day" | "week" | "month";
const factors: Record<Unit, number> = { day: 1, week: 7, month: 30 };

// The existing grant and pricing contracts store days. Months are fixed 30-day
// prepaid terms, not calendar billing periods.
export default function PlanDurationInput({ days, onChange }: { days: string; onChange: (days: string) => void }) {
  const { t } = useTranslation();
  const [unit, setUnit] = useState<Unit>(() => Number(days) > 0 && Number(days) % 30 === 0 ? "month" : Number(days) > 0 && Number(days) % 7 === 0 ? "week" : "day");
  const amount = days === "" ? "" : String(Number(days) / factors[unit]);
  const options = (Object.keys(factors) as Unit[]).map(value => ({ value, label: t(`admin.plans.editor.units.${value}`) }));
  return <SpaceBetween direction="horizontal" size="xs">
    <Input ariaLabel={t("admin.plans.editor.durationAmount")} type="number" value={amount} onChange={({ detail }) => onChange(detail.value === "" ? "" : String(Number(detail.value) * factors[unit]))} />
    <Select ariaLabel={t("admin.plans.editor.durationUnit")} selectedOption={options.find(option => option.value === unit) ?? null} options={options} onChange={({ detail }) => {
      const next = detail.selectedOption.value;
      if (next !== "day" && next !== "week" && next !== "month") return;
      setUnit(next);
      onChange(amount === "" ? "" : String(Number(amount) * factors[next]));
    }} />
  </SpaceBetween>;
}
