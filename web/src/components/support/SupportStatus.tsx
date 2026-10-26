import { useTranslation } from "react-i18next";
import { StatusIndicator } from "@cloudscape-design/components";
import type { SupportStatus as Status } from "../../api/types";

export default function SupportStatus({ status }: { status: Status }) {
  const { t } = useTranslation();
  if (status === "open") return <StatusIndicator type="pending">{t("support.status.open")}</StatusIndicator>;
  if (status === "answered") return <StatusIndicator type="success">{t("support.status.answered")}</StatusIndicator>;
  return <StatusIndicator type="stopped">{t("support.status.closed")}</StatusIndicator>;
}
