import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Container,
  SpaceBetween,
  Box,
  ProgressBar,
  Spinner,
  Flashbar,
  type FlashbarProps,
  StatusIndicator,
  ColumnLayout,
} from "@cloudscape-design/components";
import type { TrafficStats } from "../../api/types";
import * as userApi from "../../api/user";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

export default function Traffic() {
  const { t } = useTranslation();
  const [stats, setStats] = useState<TrafficStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  useEffect(() => {
    const load = async () => {
      try {
        const data = await userApi.getTrafficStats();
        setStats(data);
      } catch {
        setFlash([{ type: "error", content: t("user.traffic.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
      } finally {
        setLoading(false);
      }
    };
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.traffic.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  if (!stats || (!stats.traffic_limit && stats.traffic_used === 0)) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.traffic.title")}</Header>}>
        <Flashbar items={flash} />
        <Box textAlign="center" padding="xl">
          <StatusIndicator type="info">{t("user.traffic.noPlan")}</StatusIndicator>
        </Box>
      </ContentLayout>
    );
  }

  const usedFormatted = formatBytes(stats.traffic_used);
  const limitFormatted = stats.traffic_limit ? formatBytes(stats.traffic_limit) : "∞";
  const percentage = stats.percentage ?? 0;

  return (
    <ContentLayout header={<Header variant="h1">{t("user.traffic.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />
        <Container header={<Header variant="h2">{t("user.traffic.usage")}</Header>}>
          <SpaceBetween size="l">
            <ProgressBar
              value={percentage}
              label={t("user.traffic.usageLabel")}
              description={`${usedFormatted} / ${limitFormatted} (${percentage.toFixed(1)}%)`}
              status={percentage >= 90 ? "error" : percentage >= 70 ? "in-progress" : "in-progress"}
            />
            <ColumnLayout columns={2}>
              <Box>
                <Box variant="awsui-key-label">{t("user.traffic.daysRemaining")}</Box>
                <Box variant="p">
                  {stats.days_remaining != null ? `${stats.days_remaining} ${t("user.traffic.days")}` : "-"}
                </Box>
              </Box>
              <Box>
                <Box variant="awsui-key-label">{t("user.traffic.expiresAt")}</Box>
                <Box variant="p">
                  {stats.plan_expires_at ? new Date(stats.plan_expires_at).toLocaleDateString() : "-"}
                </Box>
              </Box>
            </ColumnLayout>
          </SpaceBetween>
        </Container>
      </SpaceBetween>
    </ContentLayout>
  );
}
