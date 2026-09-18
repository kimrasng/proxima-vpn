import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Container,
  SpaceBetween,
  Box,
  ColumnLayout,
  ProgressBar,
  StatusIndicator,
  Spinner,
  Flashbar,
  type FlashbarProps,
} from "@cloudscape-design/components";
import type { UserSummary } from "../../api/types";
import * as userApi from "../../api/user";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

const STATUS_LABEL_KEYS: Record<string, string> = {
  active: "user.dashboard.statusActive",
  suspended: "user.dashboard.statusSuspended",
  expired: "user.dashboard.statusExpired",
};

export default function Dashboard() {
  const { t } = useTranslation();
  const [summary, setSummary] = useState<UserSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  useEffect(() => {
    const load = async () => {
      try {
        const data = await userApi.getSummary();
        setSummary(data);
      } catch {
        setFlash([{ type: "error", content: t("user.dashboard.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
      } finally {
        setLoading(false);
      }
    };
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.dashboard.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  if (!summary?.plan_name) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.dashboard.title")}</Header>}>
        <Flashbar items={flash} />
        <Box textAlign="center" padding="xl">
          <StatusIndicator type="info">{t("user.dashboard.noPlan")}</StatusIndicator>
        </Box>
      </ContentLayout>
    );
  }

  const usedFormatted = formatBytes(summary.traffic_used);
  const trafficLimit = summary.traffic_limit;
  const trafficPercentage =
    trafficLimit && trafficLimit > 0
      ? Math.min(100, (summary.traffic_used / trafficLimit) * 100)
      : 0;

  const overConcurrency =
    summary.max_concurrent > 0 && summary.online_ips > summary.max_concurrent;
  const atDeviceLimit =
    summary.max_devices > 0 && summary.devices >= summary.max_devices;

  const expiresAt = summary.plan_expires_at;
  const isExpired = expiresAt ? new Date(expiresAt) < new Date() : false;

  return (
    <ContentLayout header={<Header variant="h1">{t("user.dashboard.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />

        <Container header={<Header variant="h2">{t("user.dashboard.planSection")}</Header>}>
          <ColumnLayout columns={3} variant="text-grid">
            <div>
              <Box variant="awsui-key-label">{t("user.dashboard.planName")}</Box>
              <Box variant="h3" padding={{ top: "xs" }}>{summary.plan_name}</Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("user.dashboard.accountStatus")}</Box>
              <Box padding={{ top: "xs" }}>
                <StatusIndicator type={summary.status === "active" ? "success" : "warning"}>
                  {t(STATUS_LABEL_KEYS[summary.status] ?? "user.dashboard.statusUnknown")}
                </StatusIndicator>
              </Box>
            </div>
            <div>
              <Box variant="awsui-key-label">{t("user.dashboard.expiresAt")}</Box>
              <Box padding={{ top: "xs" }}>
                {expiresAt ? (
                  <StatusIndicator type={isExpired ? "warning" : "success"}>
                    {isExpired
                      ? t("user.dashboard.expiredOn", { date: new Date(expiresAt).toLocaleDateString() })
                      : new Date(expiresAt).toLocaleDateString()}
                  </StatusIndicator>
                ) : (
                  <Box variant="p">{t("user.dashboard.noExpiry")}</Box>
                )}
              </Box>
            </div>
          </ColumnLayout>
        </Container>

        <Container header={<Header variant="h2">{t("user.dashboard.trafficSection")}</Header>}>
          {trafficLimit ? (
            <ProgressBar
              value={trafficPercentage}
              label={t("user.dashboard.trafficLabel")}
              description={t("user.dashboard.trafficUsedOfLimit", {
                used: usedFormatted,
                limit: formatBytes(trafficLimit),
              })}
              additionalInfo={`${trafficPercentage.toFixed(1)}%`}
              status={trafficPercentage >= 100 ? "error" : "in-progress"}
            />
          ) : (
            <SpaceBetween size="xs">
              <Box variant="awsui-key-label">{t("user.dashboard.trafficLabel")}</Box>
              <Box variant="h3">{usedFormatted}</Box>
              <StatusIndicator type="info">{t("user.dashboard.trafficUnlimited")}</StatusIndicator>
            </SpaceBetween>
          )}
        </Container>

        <Container header={<Header variant="h2">{t("user.dashboard.usageSection")}</Header>}>
          <ColumnLayout columns={2} variant="text-grid">
            <SpaceBetween size="xs">
              {/* online_ips counts distinct live source addresses, not devices: one
                  device can present both an IPv4 and an IPv6 address. */}
              <Box variant="awsui-key-label">{t("user.dashboard.connections")}</Box>
              <Box variant="h3">
                {t("user.dashboard.countOfCap", {
                  current: summary.online_ips,
                  cap: summary.max_concurrent,
                })}
              </Box>
              <Box variant="small">{t("user.dashboard.connectionsHint")}</Box>
              {overConcurrency && (
                <StatusIndicator type="warning">
                  {t("user.dashboard.connectionsOverCap")}
                </StatusIndicator>
              )}
            </SpaceBetween>
            <SpaceBetween size="xs">
              <Box variant="awsui-key-label">{t("user.dashboard.devices")}</Box>
              <Box variant="h3">
                {t("user.dashboard.countOfCap", {
                  current: summary.devices,
                  cap: summary.max_devices,
                })}
              </Box>
              <Box variant="small">
                {t("user.dashboard.devicesOnlineHint", { online: summary.online })}
              </Box>
              {atDeviceLimit && (
                <StatusIndicator type="warning">
                  {t("user.dashboard.devicesAtLimit")}
                </StatusIndicator>
              )}
            </SpaceBetween>
          </ColumnLayout>
        </Container>
      </SpaceBetween>
    </ContentLayout>
  );
}
