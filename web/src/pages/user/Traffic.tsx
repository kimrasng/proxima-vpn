import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import PortalEmpty from "../../components/PortalEmpty";
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
  Button,
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
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
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
      <ContentLayout header={<Header variant="h1" description={t("user.traffic.description")}>{t("user.traffic.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  if (!stats || (!stats.traffic_limit && stats.traffic_used === 0)) {
    return (
      <ContentLayout header={<Header variant="h1" description={t("user.traffic.description")}>{t("user.traffic.title")}</Header>}>
        <SpaceBetween size="l">
          <Flashbar items={flash} />
          <PortalEmpty
            title={t("user.traffic.noPlan")}
            description={t("user.traffic.noPlanHint")}
            action={<Button variant="primary" onClick={() => navigate("/portal/plan")}>{t("user.plan.checkoutModal.viewPlans")}</Button>}
          />
        </SpaceBetween>
      </ContentLayout>
    );
  }

  const usedFormatted = formatBytes(stats.traffic_used);
  const limitFormatted = stats.traffic_limit ? formatBytes(stats.traffic_limit) : "∞";
  const percentage = stats.percentage ?? 0;

  return (
    <ContentLayout header={<Header variant="h1" description={t("user.traffic.description")}>{t("user.traffic.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />
        <Container header={<Header variant="h2">{t("user.traffic.usage")}</Header>}>
          <SpaceBetween size="l">
            <ProgressBar
              value={percentage}
              description={`${usedFormatted} / ${limitFormatted}`}
              additionalInfo={stats.traffic_limit ? t("user.dashboard.trafficRemaining", { remaining: formatBytes(Math.max(0, stats.traffic_limit - stats.traffic_used)) }) : undefined}
              status={percentage >= 100 ? "error" : "in-progress"}
            />
            <ColumnLayout columns={3} variant="text-grid">
              <Box>
                <Box variant="awsui-key-label">{t("user.traffic.usageLabel")}</Box>
                <Box variant="h2" padding="n">{usedFormatted}</Box>
              </Box>
              <Box>
                <Box variant="awsui-key-label">{t("user.traffic.daysRemaining")}</Box>
                <Box variant="h2" padding="n">
                  {stats.days_remaining != null ? `${stats.days_remaining} ${t("user.traffic.days")}` : "-"}
                </Box>
              </Box>
              <Box>
                <Box variant="awsui-key-label">{t("user.traffic.expiresAt")}</Box>
                <Box variant="h2" padding="n">
                  {stats.plan_expires_at ? new Date(stats.plan_expires_at).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language) : "-"}
                </Box>
              </Box>
            </ColumnLayout>
          </SpaceBetween>
        </Container>
      </SpaceBetween>
    </ContentLayout>
  );
}
