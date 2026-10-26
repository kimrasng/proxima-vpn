import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert, Box, Button, ColumnLayout, Container, ContentLayout,
  Grid, Header, Icon, Modal, ProgressBar, SpaceBetween,
  Spinner, StatusIndicator, Table,
} from "@cloudscape-design/components";
import {
  borderRadiusContainer, colorBackgroundStatusInfo, colorBorderStatusInfo, colorBackgroundNotificationBlue,
} from "@cloudscape-design/design-tokens";
import type { Announcement } from "../../api/types";
import * as userApi from "../../api/user";
import DeviceQuickConnect from "../../components/DeviceQuickConnect";
import { useUserResource } from "../../hooks/useUserResource";
import "./dashboard.css";

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(units.length - 1, Math.max(0, Math.floor(Math.log(bytes) / Math.log(1024))));
  const value = bytes / 1024 ** index;
  return `${Number.isInteger(value) ? value : value.toFixed(1)} ${units[index]}`;
}

const STATUS_LABEL_KEYS: Record<string, string> = {
  active: "user.dashboard.statusActive",
  suspended: "user.dashboard.statusSuspended",
  expired: "user.dashboard.statusExpired",
};

export default function Dashboard() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const summary = useUserResource(userApi.getSummary);
  const nodes = useUserResource(userApi.listAvailableNodes);
  const announcements = useUserResource(userApi.listAnnouncements);
  const tickets = useUserResource(userApi.listResetTickets);
  const ticketCount = tickets.data?.length;
  const [selectedAnnouncement, setSelectedAnnouncement] = useState<Announcement | null>(null);
  const [connectionRefresh, setConnectionRefresh] = useState(0);
  const data = summary.data;
  const hasPlan = !!data?.plan_name;
  const expires = data?.plan_expires_at ? new Date(data.plan_expires_at) : null;
  const daysRemaining = expires ? Math.max(0, Math.ceil((expires.getTime() - Date.now()) / 86400000)) : null;
  const expired = data?.status === "expired" || (expires !== null && expires.getTime() <= Date.now());
  const trafficLimit = data?.traffic_limit ?? 0;
  const trafficPercentage = trafficLimit > 0 && data ? Math.min(100, data.traffic_used / trafficLimit * 100) : 0;
  const nextReset = data?.next_traffic_reset_at ? new Date(data.next_traffic_reset_at) : null;
  const untilReset = nextReset ? Math.max(0, nextReset.getTime() - Date.now()) : 0;
  const resetDays = Math.floor(untilReset / 86400000);
  const resetHours = Math.floor((untilReset % 86400000) / 3600000);
  const canReset = hasPlan && !expired && trafficLimit > 0;
  const resetAfterTerm = !!(nextReset && expires && nextReset.getTime() >= expires.getTime());
  const resetDate = nextReset?.toLocaleString(i18n.resolvedLanguage ?? i18n.language, { month: "long", day: "numeric", hour: "numeric", minute: "2-digit" }) ?? "";
  const dateFormat = (date: string | Date) => new Date(date).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language);
  const onlineNodes = nodes.data?.filter((node) => node.status === "online").length ?? 0;
  const latestAnnouncements = (announcements.data ?? [])
    .filter((item) => item.is_active && (!item.expires_at || new Date(item.expires_at).getTime() > Date.now()))
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    .slice(0, 3);

  return (
    <ContentLayout header={
      <Header variant="h1" description={t("user.dashboard.description")} actions={
        <SpaceBetween direction="horizontal" size="xs">
          <Button iconName="refresh" loading={summary.loading || nodes.loading || announcements.loading}
            onClick={() => { summary.refresh(); nodes.refresh(); announcements.refresh(); setConnectionRefresh((value) => value + 1); }}>{t("user.dashboard.refresh")}</Button>
          {hasPlan && <Button onClick={() => navigate("/portal/plan")}>{t("user.dashboard.renewPlan")}</Button>}
        </SpaceBetween>
      }>{t("user.dashboard.title")}</Header>
    }>
      <div className="user-dashboard">
      <SpaceBetween size="l">
        {summary.loading ? <Container><Spinner size="large" /></Container> : summary.error ? (
          <Alert type="error" action={<Button onClick={summary.refresh}>{t("user.dashboard.retry")}</Button>}>
            {t("user.dashboard.loadError")}
          </Alert>
        ) : data && !hasPlan ? (
          <>
            <div style={{
              background: colorBackgroundStatusInfo, border: `1px solid ${colorBorderStatusInfo}`,
              borderRadius: borderRadiusContainer, padding: "20px 24px",
            }}>
              <div style={{ display: "flex", alignItems: "center", gap: 24, flexWrap: "wrap" }}>
                <svg width="96" height="96" viewBox="0 0 96 96" role="img" aria-hidden="true">
                  <circle cx="48" cy="48" r="46" fill="#fff" opacity="0.7" />
                  <rect x="20" y="26" width="48" height="38" rx="4" fill="#fff" stroke="#5f6b7a" strokeWidth="2" />
                  <path d="M20 36h48" stroke="#5f6b7a" strokeWidth="2" />
                  <path d="M28 46h24M28 53h16" stroke="#aab4c0" strokeWidth="2" strokeLinecap="round" />
                  <path d="M62 52l12 4v8c0 7-5 11-12 14-7-3-12-7-12-14v-8z" fill={colorBackgroundNotificationBlue} />
                  <path d="M57 64l4 4 7-8" stroke="#fff" strokeWidth="2.5" fill="none" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
                <SpaceBetween size="xs">
                  <Box variant="h2" padding="n">{t("user.dashboard.noPlanTitle")}</Box>
                  <Box variant="p" padding="n">{t("user.dashboard.heroDescription")}</Box>
                  <Button variant="primary" onClick={() => navigate("/portal/plan")}>{t("user.dashboard.viewPlans")}</Button>
                </SpaceBetween>
              </div>
            </div>
            <Container header={<Header variant="h2" description={t("user.dashboard.guideSubtitle")}>{t("user.dashboard.guideTitle")}</Header>}>
              <SpaceBetween size="m">
                <ColumnLayout columns={3}>
                  {([
                    ["file", t("user.dashboard.step1Title"), t("user.dashboard.step1Description")],
                    ["download", t("user.dashboard.step2Title"), t("user.dashboard.step2Description")],
                    ["share", t("user.dashboard.step3Title"), t("user.dashboard.step3Description")],
                  ] as const).map(([icon, title, description], index) => (
                    <div key={icon} style={{ display: "flex", gap: 12, alignItems: "flex-start" }}>
                      <Box variant="h3" color="text-status-info" padding="n">{index + 1}</Box>
                      <SpaceBetween size="xxs">
                        <Box variant="h4" padding="n"><SpaceBetween direction="horizontal" size="xs">
                          <Icon name={icon} /><span>{title}</span>
                        </SpaceBetween></Box>
                        <Box color="text-body-secondary">{description}</Box>
                      </SpaceBetween>
                    </div>
                  ))}
                </ColumnLayout>
                <Alert type="info" action={<Button variant="link" iconName="external" iconAlign="right"
                  onClick={() => navigate("/portal/devices")}>{t("user.dashboard.guideLink")}</Button>}>
                  {t("user.dashboard.guideNote")}
                </Alert>
              </SpaceBetween>
            </Container>
          </>
        ) : data && (
          <Container header={<Header variant="h2" actions={
            <Button variant="link" onClick={() => navigate("/portal/plan")}>{t("user.dashboard.planDetails")}</Button>
          }>
            {data.plan_name}
          </Header>}>
            <SpaceBetween size="m">
              <>
                  <StatusIndicator type={data.status === "active" && !expired ? "success" : "warning"}>
                    {t(expired ? "user.dashboard.statusExpired" : STATUS_LABEL_KEYS[data.status] ?? "user.dashboard.statusUnknown")}
                  </StatusIndicator>
                  {data.status === "suspended" ? <Alert type="warning">{t("user.dashboard.suspendedHint")}</Alert> :
                    (expired || (daysRemaining !== null && daysRemaining <= 7)) && (
                      <Alert type="warning" action={<Button onClick={() => navigate("/portal/plan")}>{t("user.dashboard.renewPlan")}</Button>}>
                        {t(expired ? "user.dashboard.expiredHint" : "user.dashboard.expiringHint", { days: daysRemaining })}
                      </Alert>
                    )}
                  <ColumnLayout columns={2} variant="text-grid">
                    <SpaceBetween size="xs">
                      <Box variant="awsui-key-label">{t(trafficLimit > 0 ? "user.dashboard.monthlyRemaining" : "user.dashboard.monthlyTraffic")}</Box>
                      <Box variant="h2">{formatBytes(trafficLimit > 0 ? Math.max(0, trafficLimit - data.traffic_used) : data.traffic_used)}</Box>
                      {trafficLimit > 0 ? <>
                        <ProgressBar value={Math.max(0, 100 - trafficPercentage)}
                          ariaLabel={t("user.dashboard.monthlyRemaining")}
                          additionalInfo={t("user.dashboard.trafficUsedSummary", { used: formatBytes(data.traffic_used), limit: formatBytes(trafficLimit) })}
                          status={trafficPercentage >= 100 ? "error" : "in-progress"} />
                        {nextReset && <Box color="text-body-secondary">
                          <span style={{ display: "inline-flex", alignItems: "flex-start", gap: 6 }}>
                            <Icon name="calendar" variant="subtle" />
                            <span>{resetAfterTerm
                              ? t("user.dashboard.nextResetAfterTerm", { date: resetDate })
                              : t(resetDays > 0 ? "user.dashboard.nextResetIn" : "user.dashboard.nextResetInHours", { date: resetDate, days: resetDays, hours: resetHours })}</span>
                          </span>
                        </Box>}
                      </> : <Box color="text-body-secondary">{t("user.dashboard.trafficUnlimited")}</Box>}
                    </SpaceBetween>
                    <SpaceBetween size="xs">
                      <Box variant="awsui-key-label">{t("user.dashboard.expiresAt")}</Box>
                      <Box variant="h2">{daysRemaining === null ? t("user.dashboard.noExpiry") :
                        t(expired ? "user.dashboard.statusExpired" : "user.dashboard.daysLeft", { days: daysRemaining })}</Box>
                      {expires && <Box color="text-body-secondary">{dateFormat(expires)}</Box>}
                    </SpaceBetween>
                  </ColumnLayout>
                  {canReset && trafficPercentage >= 80 && (
                    <Alert
                      type={trafficPercentage >= 100 ? "error" : "warning"}
                      action={
                        <Button onClick={() => navigate("/portal/plan")}>
                          {(ticketCount ?? 0) > 0 ? t("user.dashboard.useTicket") : t("user.dashboard.seePlans")}
                        </Button>
                      }
                    >
                      {t(trafficPercentage >= 100 ? "user.dashboard.trafficExhausted" : "user.dashboard.trafficWarning")}
                      {(ticketCount ?? 0) > 0 && <> {t("user.dashboard.ticketHint", { count: ticketCount })}</>}
                    </Alert>
                  )}
                  <SpaceBetween size="xs">
                      <Box variant="awsui-key-label">{t("user.dashboard.connections")}</Box>
                      <Box>{data.max_concurrent > 0
                        ? t("user.dashboard.countOfCap", { current: data.online_ips, cap: data.max_concurrent })
                        : t("user.dashboard.countUnlimited", { current: data.online_ips })}</Box>
                      <Box variant="small" color="text-body-secondary">{t("user.dashboard.connectionsHint")}</Box>
                      {data.max_concurrent > 0 && data.online_ips > data.max_concurrent && <StatusIndicator type="warning">
                        {t("user.dashboard.connectionsOverCap")}
                      </StatusIndicator>}
                  </SpaceBetween>
              </>
            </SpaceBetween>
          </Container>
        )}


        <Grid gridDefinition={[{ colspan: { default: 12, s: 7 } }, { colspan: { default: 12, s: 5 } }]}>
          <DeviceQuickConnect summary={data} refreshKey={connectionRefresh} />
          <SpaceBetween size="l">
            <Container header={<Header variant="h2" description={t("user.dashboard.serverScope")} actions={
              <Button variant="link" onClick={() => navigate("/portal/nodes")}>{t("user.dashboard.viewAll")}</Button>
            }>{t("user.dashboard.serverStatus")}</Header>}>
              <SpaceBetween size="s">
                {nodes.error ? <Alert type="error" action={<Button onClick={nodes.refresh}>{t("user.dashboard.retry")}</Button>}>
                  {t("user.nodes.loadError")}
                </Alert> : nodes.loading ? <Spinner /> : data && !hasPlan ? (
                  <Box color="text-body-secondary">{t("user.dashboard.serverNeedsPlan")}</Box>
                ) : (nodes.data?.length ?? 0) === 0 ? (
                  <Box color="text-body-secondary">{t("user.nodes.empty")}</Box>
                ) : (
                  <>
                    {(nodes.data?.length ?? 0) > 0 && <StatusIndicator type={onlineNodes === nodes.data?.length ? "success" : "warning"}>
                      {t("user.dashboard.serversOnline", { online: onlineNodes, total: nodes.data?.length })}
                    </StatusIndicator>}
                    <Table variant="embedded" contentDensity="compact" wrapLines
                      items={[...(nodes.data ?? [])].sort((a, b) => Number(b.status === "online") - Number(a.status === "online")).slice(0, 4)}
                      columnDefinitions={[
                        { id: "name", header: t("user.nodes.name"), cell: (node) => <SpaceBetween size="xxs">
                          <Box>{node.name}</Box><Box variant="small" color="text-body-secondary">
                            {[node.country, node.region].filter(Boolean).join(" / ")}
                          </Box>
                        </SpaceBetween> },
                        { id: "status", header: t("user.nodes.status"), cell: (node) => <StatusIndicator
                          type={node.status === "online" ? "success" : node.status === "offline" ? "error" : "pending"}>
                          {t(node.status === "online" ? "user.nodes.online" : node.status === "offline" ? "user.nodes.offline" : "user.dashboard.statusUnknown")}
                        </StatusIndicator> },
                      ]}
                      empty={<Box color="text-body-secondary">{t("user.nodes.empty")}</Box>} />
                  </>
                )}
              </SpaceBetween>
            </Container>
            <Container header={<Header variant="h2" actions={
              <Button variant="link" onClick={() => navigate("/portal/announcements")}>{t("user.dashboard.viewAll")}</Button>
            }>{t("user.announcements.title")}</Header>}>
              {announcements.error ? <Alert type="error" action={<Button onClick={announcements.refresh}>{t("user.dashboard.retry")}</Button>}>
                {t("user.announcements.loadError")}
              </Alert> : announcements.loading ? <Spinner /> : latestAnnouncements.length === 0 ? (
                <Box color="text-body-secondary">{t("user.announcements.empty")}</Box>
              ) : <SpaceBetween size="m">
                {latestAnnouncements.map((item) => <SpaceBetween key={item.id} size="xxs">
                  <Button variant="inline-link" onClick={() => setSelectedAnnouncement(item)}>{item.title}</Button>
                  <Box variant="small" color="text-body-secondary">{dateFormat(item.created_at)}</Box>
                  <Box variant="p">{item.content.length > 100 ? `${item.content.slice(0, 100)}…` : item.content}</Box>
                </SpaceBetween>)}
              </SpaceBetween>}
            </Container>
          </SpaceBetween>
        </Grid>
      </SpaceBetween>
      </div>
      <Modal visible={selectedAnnouncement !== null} closeAriaLabel={t("user.announcements.close")} onDismiss={() => setSelectedAnnouncement(null)}
        header={selectedAnnouncement?.title} footer={<Box float="right">
          <Button onClick={() => setSelectedAnnouncement(null)}>{t("user.announcements.close")}</Button>
        </Box>}>
        {selectedAnnouncement && <SpaceBetween size="s">
          <Box variant="small" color="text-body-secondary">{dateFormat(selectedAnnouncement.created_at)}</Box>
          {selectedAnnouncement.content.split("\n").map((line, index) => <Box key={index} variant="p">{line || "\u00a0"}</Box>)}
        </SpaceBetween>}
      </Modal>
    </ContentLayout>
  );
}
