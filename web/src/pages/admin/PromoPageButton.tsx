import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@cloudscape-design/components";
import PromoPageEditor from "./PromoPageEditor";

export default function PromoPageButton({ code, fixedPlan }: { readonly code: string; readonly fixedPlan?: boolean }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return <>
    <Button variant="inline-link" onClick={() => setOpen(true)}>{t("admin.promotions.page.edit")}</Button>
    {open && <PromoPageEditor code={code} fixedPlan={fixedPlan} onClose={() => setOpen(false)} />}
  </>;
}
