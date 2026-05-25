import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Container,
  SpaceBetween,
  Box,
  Cards,
  Button,
  Spinner,
  Flashbar,
  type FlashbarProps,
  StatusIndicator,
  ColumnLayout,
  Badge,
} from "@cloudscape-design/components";
import type { UserProfile, Plan, PlanRequest } from "../../api/types";
import * as userApi from "../../api/user";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "∞";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

export default function PlanInfo() {
  const { t } = useTranslation();
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [requests, setRequests] = useState<PlanRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [showPlans, setShowPlans] = useState(false);

  const loadData = async () => {
    try {
      setLoading(true);
      const [profileData, planList, requestList] = await Promise.all([
        userApi.getProfile(),
        userApi.listPlans(),
        userApi.listMyPlanRequests(),
      ]);
      setProfile(profileData);
      setPlans(planList);
      setRequests(requestList);
    } catch {
      setFlash([{ type: "error", content: t("user.plan.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSelectPlan = async (planId: string) => {
    try {
      setSubmitting(true);
      await userApi.createPlanRequest(planId);
      setFlash([{ type: "success", content: t("user.plan.requestSuccess"), dismissible: true, onDismiss: () => setFlash([]) }]);
      await loadData();
    } catch {
      setFlash([{ type: "error", content: t("user.plan.requestError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setSubmitting(false);
    }
  };

  const hasPendingRequest = (planId: string) =>
    requests.some((r) => r.plan_id === planId && r.status === "pending");

  const hasAnyPendingRequest = requests.some((r) => r.status === "pending");

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.plan.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  const isExpired = profile?.plan_expires_at
    ? new Date(profile.plan_expires_at) < new Date()
    : false;

  const hasPlan = !!profile?.plan_name && !isExpired;

  return (
    <ContentLayout header={<Header variant="h1">{t("user.plan.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />

        {hasPlan && profile && (
          <Container header={<Header variant="h2">{t("user.plan.currentPlan")}</Header>}>
            <ColumnLayout columns={3} variant="text-grid">
              <SpaceBetween size="xs">
                <Box variant="awsui-key-label">{t("user.plan.planName")}</Box>
                <Box>{profile.plan_name}</Box>
              </SpaceBetween>
              <SpaceBetween size="xs">
                <Box variant="awsui-key-label">{t("user.plan.trafficLimit")}</Box>
                <Box>{profile.traffic_limit ? formatBytes(profile.traffic_limit) : "∞"}</Box>
              </SpaceBetween>
              <SpaceBetween size="xs">
                <Box variant="awsui-key-label">{t("user.plan.expiresAt")}</Box>
                <Box>
                  {profile.plan_expires_at
                    ? new Date(profile.plan_expires_at).toLocaleDateString()
                    : "-"}
                </Box>
              </SpaceBetween>
              <SpaceBetween size="xs">
                <Box variant="awsui-key-label">{t("user.plan.status")}</Box>
                <Badge color="green">{t("user.plan.active")}</Badge>
              </SpaceBetween>
            </ColumnLayout>
            <Box margin={{ top: "l" }}>
              <Button onClick={() => setShowPlans(!showPlans)}>
                {t("user.plan.changePlan")}
              </Button>
            </Box>
          </Container>
        )}

        {!hasPlan && (
          <Container>
            <Box textAlign="center" padding="l">
              <StatusIndicator type="warning">
                {isExpired ? t("user.plan.expired") : t("user.plan.noPlan")}
              </StatusIndicator>
            </Box>
          </Container>
        )}

        {hasAnyPendingRequest && (
          <Container>
            <StatusIndicator type="pending">{t("user.plan.pendingRequest")}</StatusIndicator>
          </Container>
        )}

        {(!hasPlan || showPlans) && plans.length > 0 && (
          <Cards
            cardDefinition={{
              header: (item) => item.name,
              sections: [
                {
                  id: "traffic",
                  header: t("user.plan.trafficLimit"),
                  content: (item) => item.traffic_limit ? formatBytes(item.traffic_limit) : "∞",
                },
                {
                  id: "duration",
                  header: t("user.plan.duration"),
                  content: (item) => `${item.duration_days} ${t("user.plan.durationDays")}`,
                },
                {
                  id: "devices",
                  header: t("user.plan.maxDevices"),
                  content: (item) => String(item.max_devices),
                },
                {
                  id: "speed",
                  header: t("user.plan.speedLimit"),
                  content: (item) => item.speed_limit ? `${item.speed_limit} Mbps` : "∞",
                },
                {
                  id: "action",
                  content: (item) => (
                    <Button
                      variant="primary"
                      onClick={() => handleSelectPlan(item.id)}
                      loading={submitting}
                      disabled={hasPendingRequest(item.id)}
                    >
                      {hasPendingRequest(item.id) ? t("user.plan.requested") : t("user.plan.selectPlan")}
                    </Button>
                  ),
                },
              ],
            }}
            items={plans}
            header={<Header variant="h2">{t("user.plan.availablePlans")}</Header>}
          />
        )}
      </SpaceBetween>
    </ContentLayout>
  );
}
