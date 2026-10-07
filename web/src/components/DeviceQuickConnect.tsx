import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert, Box, Button, Container, CopyToClipboard, FormField, Header,
  Input, Modal, Select, SpaceBetween, Spinner,
} from "@cloudscape-design/components";
import { QRCodeSVG } from "qrcode.react";
import type { UserSummary } from "../api/types";
import * as userApi from "../api/user";
import { useUserResource } from "../hooks/useUserResource";
import { getAccountSubscriptionUrl } from "../utils/subscriptionUrl";

export default function DeviceQuickConnect({
  summary, refreshKey,
}: { summary: UserSummary | null; refreshKey: number }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const domains = useUserResource(userApi.listPublicSubscriptionDomains, refreshKey);
  const profile = useUserResource(userApi.getProfile, refreshKey);
  const [domainId, setDomainId] = useState<string>();
  const [showQr, setShowQr] = useState(false);

  const orderedDomains = [...(domains.data ?? [])].sort((a, b) =>
    Number(b.is_default) - Number(a.is_default) || a.display_order - b.display_order,
  );
  const selectedDomain = orderedDomains.find((domain) => domain.id === domainId) ?? orderedDomains[0];
  const expired = summary?.plan_expires_at ? new Date(summary.plan_expires_at).getTime() <= Date.now() : false;
  const usable = !!summary?.plan_name && summary.status === "active" && !expired;
  const url = profile.data?.sub_token ? getAccountSubscriptionUrl(profile.data.sub_token, selectedDomain?.domain) : "";

  return (
    <Container header={
      <Header variant="h2" description={t("user.dashboard.connectDescription")} actions={
        <Button variant="link" onClick={() => navigate("/portal/devices")}>{t("user.dashboard.subscriptionDetails")}</Button>
      }>{t("user.dashboard.quickConnect")}</Header>
    }>
      <SpaceBetween size="m">
        {summary === null && <Box color="text-body-secondary">{t("user.dashboard.accountUnavailable")}</Box>}
        {summary !== null && !usable && (
          <Alert type="info" action={summary?.status === "suspended" ? undefined :
            <Button onClick={() => navigate("/portal/plan")}>{t("user.dashboard.browsePlans")}</Button>
          }>{t(summary?.status === "suspended" ? "user.dashboard.suspendedHint" : "user.dashboard.connectNeedsPlan")}</Alert>
        )}
        {profile.loading || domains.loading ? <Spinner /> : profile.error ? (
          <Alert type="error" action={<Button onClick={profile.refresh}>{t("user.dashboard.retry")}</Button>}>
            {t("user.devices.loadError")}
          </Alert>
        ) : !url ? (
          <Alert type="warning" action={<Button onClick={profile.refresh}>{t("user.dashboard.retry")}</Button>}>
            {t("user.dashboard.urlUnavailable")}
          </Alert>
        ) : (
          <>
            {orderedDomains.length > 0 && (
              <FormField label={t("user.dashboard.subscriptionDomain")}>
                <Select selectedOption={selectedDomain ? { value: selectedDomain.id, label: selectedDomain.domain } : null}
                  options={orderedDomains.map((domain) => ({ value: domain.id, label: domain.domain }))}
                  onChange={({ detail }) => setDomainId(detail.selectedOption.value)} />
              </FormField>
            )}
            {domains.error && <Alert type="warning">{t("user.dashboard.domainFallback")}</Alert>}
            <FormField label={t("user.devices.accountUrl")} description={t("user.devices.accountUrlHint")}>
              <Input value={url} readOnly ariaLabel={t("user.devices.accountUrl")} />
            </FormField>
            <SpaceBetween direction="horizontal" size="xs">
              <CopyToClipboard textToCopy={url} copyButtonText={t("user.dashboard.copyUrl")}
                copySuccessText={t("common.copied")} copyErrorText={t("user.dashboard.copyError")} />
              <Button onClick={() => setShowQr(true)}>{t("user.devices.showQr")}</Button>
            </SpaceBetween>
            {orderedDomains.length > 1 && <Box variant="small" color="text-body-secondary">
              {t("user.devices.subscriptionDomainWarning")}
            </Box>}
          </>
        )}
      </SpaceBetween>
      <Modal visible={showQr} closeAriaLabel={t("user.announcements.close")} onDismiss={() => setShowQr(false)} header={t("user.devices.qrTitle")}>
        <SpaceBetween size="m">
          <Box textAlign="center"><QRCodeSVG value={url} size={220} title={t("user.devices.qrTitle")} /></Box>
          <Box variant="p">{t("user.dashboard.importHint")}</Box>
          <CopyToClipboard textToCopy={url} copyButtonText={t("user.dashboard.copyUrl")}
            copySuccessText={t("common.copied")} copyErrorText={t("user.dashboard.copyError")} />
        </SpaceBetween>
      </Modal>
    </Container>
  );
}
