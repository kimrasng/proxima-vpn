import { useTranslation } from "react-i18next";
import { StatusIndicator } from "@cloudscape-design/components";
import type { SupportStatus as Status } from "../../api/types";

export default function SupportStatus({ status, pill = false }: { status: Status; pill?: boolean }) {
  const { t } = useTranslation();
  const label = status === "open" ? t("support.status.open") : status === "answered" ? t("support.status.answered") : t("support.status.closed");
  if (pill) return <span className={`support-pill support-pill--${status}`}>{label}</span>;
  if (status === "open") return <StatusIndicator type="pending">{label}</StatusIndicator>;
  if (status === "answered") return <StatusIndicator type="success">{label}</StatusIndicator>;
  return <StatusIndicator type="stopped">{label}</StatusIndicator>;
}
