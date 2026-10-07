import { Box, Button, Container, CopyToClipboard, Header, Link, SpaceBetween, StatusIndicator } from "@cloudscape-design/components";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import type { ActivityEntry } from "../../api/types";

export function ActivityResource({ entry }: { readonly entry: ActivityEntry }) {
  const navigate = useNavigate();
  const routes: Readonly<Record<string, string>> = { node: "/admin/nodes", user: "/admin/users" };
  const route = entry.target_type ? routes[entry.target_type] : undefined;
  const label = entry.target_id || entry.target_type || "—";
  return route && entry.target_id ? (
    <Link href={`${route}/${encodeURIComponent(entry.target_id)}`} onFollow={(event) => {
      event.preventDefault();
      navigate(`${route}/${encodeURIComponent(entry.target_id ?? "")}`);
    }}>{label}</Link>
  ) : <span>{label}</span>;
}

export function ActivityDetails({ entry, eventLabel, onClose }: {
  readonly entry: ActivityEntry;
  readonly eventLabel: string;
  readonly onClose: () => void;
}) {
  const { t } = useTranslation();
  const requestId = entry.detail.request_id;
  const rawJson = JSON.stringify(entry, null, 2);
  const fields = [
    { label: t("admin.activity.col.severity"), value: <StatusIndicator type={entry.severity}>{t(`admin.nodeEvents.severity.${entry.severity}`, { defaultValue: entry.severity })}</StatusIndicator> },
    { label: t("admin.dashboard.col.time"), value: new Date(entry.created_at).toLocaleString() },
    { label: t("admin.dashboard.col.event"), value: eventLabel },
    { label: t("admin.activity.eventType"), value: entry.event_type },
    { label: t("admin.activity.col.actor"), value: entry.actor_label || entry.actor_id || entry.actor_type },
    { label: t("admin.activity.actorType"), value: entry.actor_type },
    { label: t("admin.activity.col.target"), value: <ActivityResource entry={entry} /> },
    { label: t("admin.activity.resourceType"), value: entry.target_type || "—" },
    { label: t("admin.activity.eventId"), value: entry.id },
    ...(typeof requestId === "string" && requestId ? [{ label: t("admin.activity.requestId"), value: requestId }] : []),
  ];
  return (
    <aside className="activity-details" aria-label={t("admin.activity.details")}>
      <Container header={<Header variant="h2" actions={<Button variant="icon" iconName="close"
        ariaLabel={t("common.close")} onClick={onClose} />}>{t("admin.activity.details")}</Header>}>
        <SpaceBetween size="m">
          {fields.map((field) => <div className="activity-details__field" key={field.label}>
            <Box variant="awsui-key-label">{field.label}</Box>
            <div>{field.value}</div>
          </div>)}
          <Header variant="h3" actions={<CopyToClipboard textToCopy={rawJson} variant="icon"
            copyButtonAriaLabel={t("admin.activity.copyJson")} copySuccessText={t("admin.activity.copied")}
            copyErrorText={t("admin.activity.copyError")} />}>{t("admin.activity.rawJson")}</Header>
          <Box variant="pre"><code className="activity-json">{rawJson}</code></Box>
        </SpaceBetween>
      </Container>
    </aside>
  );
}
