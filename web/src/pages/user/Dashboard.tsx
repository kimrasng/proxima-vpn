import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert, Box, Button, ColumnLayout, Container, ContentLayout,
  ExpandableSection, Grid, Header, Modal, ProgressBar, SpaceBetween,
  Spinner, StatusIndicator, Table,
} from "@cloudscape-design/components";
import type { Announcement } from "../../api/types";
import * as userApi from "../../api/user";
import DeviceQuickConnect from "../../components/DeviceQuickConnect";
import { useUserResource } from "../../hooks/useUserResource";

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(units.length - 1, Math.max(0, Math.floor(Math.log(bytes) / Math.log(1024))));
  return `${(bytes / 1024 ** index).toFixed(1)} ${units[index]}`;
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
  const [selectedAnnouncement, setSelectedAnnouncement] = useState<Announcement | null>(null);
  const [connectionRefresh, setConnectionRefresh] = useState(0);
  const data = summary.data;
  const hasPlan = !!data?.plan_name;
  const expires = data?.plan_expires_at ? new Date(data.plan_expires_at) : null;
  const daysRemaining = expires ? Math.max(0, Math.ceil((expires.getTime() - Date.now()) / 86400000)) : null;
  const expired = data?.status === "expired" || (expires !== null && expires.getTime() <= Date.now());
  const trafficLimit = data?.traffic_limit ?? 0;
  const trafficPercentage = trafficLimit > 0 && data ? Math.min(100, data.traffic_used / trafficLimit * 100) : 0;
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
          <Button variant={hasPlan ? "normal" : "primary"} onClick={() => navigate("/portal/plan")}>
            {t(hasPlan ? "user.dashboard.renewPlan" : "user.dashboard.browsePlans")}
          </Button>
        </SpaceBetween>
      }>{t("user.dashboard.title")}</Header>
    }>
      <SpaceBetween size="l">
        {summary.loading ? <Container><Spinner size="large" /></Container> : summary.error ? (
          <Alert type="error" action={<Button onClick={summary.refresh}>{t("user.dashboard.retry")}</Button>}>
            {t("user.dashboard.loadError")}
          </Alert>
        ) : data && (
          <Container header={<Header variant="h2" description={t("user.dashboard.planSection")}>
            {hasPlan ? data.plan_name : t("user.dashboard.noPlanTitle")}
          </Header>}>
            <SpaceBetween size="m">
              {!hasPlan ? (
                <SpaceBetween size="s">
                  <Box variant="p">{t("user.dashboard.noPlanDescription")}</Box>
                  <Button variant="primary" onClick={() => navigate("/portal/plan")}>{t("user.dashboard.browsePlans")}</Button>
                </SpaceBetween>
              ) : (
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
                      <Box variant="awsui-key-label">{t("user.dashboard.monthlyTraffic")}</Box>
                      <Box variant="h2">{formatBytes(data.traffic_used)}</Box>
                      <Box color="text-body-secondary">{trafficLimit > 0
                        ? t("user.dashboard.trafficAllowance", { limit: formatBytes(trafficLimit) })
                        : t("user.dashboard.trafficUnlimited")}</Box>
                      {trafficLimit > 0 && <ProgressBar value={trafficPercentage}
                        label={t("user.dashboard.trafficUsedOfLimit", { used: formatBytes(data.traffic_used), limit: formatBytes(trafficLimit) })}
                        additionalInfo={t("user.dashboard.trafficRemaining", { remaining: formatBytes(Math.max(0, trafficLimit - data.traffic_used)) })}
                        status={trafficPercentage >= 100 ? "error" : "in-progress"} />}
                    </SpaceBetween>
                    <SpaceBetween size="xs">
                      <Box variant="awsui-key-label">{t("user.dashboard.expiresAt")}</Box>
                      <Box variant="h2">{daysRemaining === null ? t("user.dashboard.noExpiry") :
                        t(expired ? "user.dashboard.statusExpired" : "user.dashboard.daysLeft", { days: daysRemaining })}</Box>
                      {expires && <Box color="text-body-secondary">{dateFormat(expires)}</Box>}
                    </SpaceBetween>
                  </ColumnLayout>
                  {trafficPercentage >= 80 && <Alert type="warning">{t(trafficPercentage >= 100
                    ? "user.dashboard.trafficExhausted" : "user.dashboard.trafficWarning")}</Alert>}
                  <ExpandableSection headerText={t("user.dashboard.connections")}>
                    <SpaceBetween size="xs">
                      <Box>{data.max_concurrent > 0
                        ? t("user.dashboard.countOfCap", { current: data.online_ips, cap: data.max_concurrent })
                        : t("user.dashboard.countUnlimited", { current: data.online_ips })}</Box>
                      <Box variant="small" color="text-body-secondary">{t("user.dashboard.connectionsHint")}</Box>
                      {data.max_concurrent > 0 && data.online_ips > data.max_concurrent && <StatusIndicator type="warning">
                        {t("user.dashboard.connectionsOverCap")}
                      </StatusIndicator>}
                    </SpaceBetween>
                  </ExpandableSection>
                </>
              )}
            </SpaceBetween>
          </Container>
        )}

        <Grid gridDefinition={[{ colspan: { default: 12, m: 7 } }, { colspan: { default: 12, m: 5 } }]}>
          <DeviceQuickConnect summary={data} refreshKey={connectionRefresh} />
          <SpaceBetween size="l">
            <Container header={<Header variant="h2" description={t("user.dashboard.serverScope")} actions={
              <Button variant="link" onClick={() => navigate("/portal/nodes")}>{t("user.dashboard.viewAll")}</Button>
            }>{t("user.dashboard.serverStatus")}</Header>}>
              <SpaceBetween size="s">
                {nodes.error ? <Alert type="error" action={<Button onClick={nodes.refresh}>{t("user.dashboard.retry")}</Button>}>
                  {t("user.nodes.loadError")}
                </Alert> : nodes.loading ? <Spinner /> : (
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
