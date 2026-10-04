import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert, Box, Button, ColumnLayout, Container, CopyToClipboard, FormField, Header,
  Input, Modal, Select, SpaceBetween, Spinner, StatusIndicator,
} from "@cloudscape-design/components";
import { QRCodeSVG } from "qrcode.react";
import type { UserSummary } from "../api/types";
import * as userApi from "../api/user";
import { useUserResource } from "../hooks/useUserResource";
import { getAccountSubscriptionUrl, getSubscriptionUrl, SUBSCRIPTION_FORMATS } from "../utils/subscriptionUrl";

export default function DeviceQuickConnect({
  summary, onCreated, refreshKey,
}: { summary: UserSummary | null; onCreated: () => void; refreshKey: number }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const devices = useUserResource(userApi.listDevices, refreshKey);
  const domains = useUserResource(userApi.listPublicSubscriptionDomains, refreshKey);
  const profile = useUserResource(userApi.getProfile, refreshKey);
  const [deviceId, setDeviceId] = useState<string>();
  const [domainId, setDomainId] = useState<string>();
  const [format, setFormat] = useState("v2ray");
  const [showAdd, setShowAdd] = useState(false);
  const [showQr, setShowQr] = useState(false);
  const [name, setName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [addError, setAddError] = useState(false);
  const [created, setCreated] = useState(false);

  const orderedDomains = [...(domains.data ?? [])].sort((a, b) =>
    Number(b.is_default) - Number(a.is_default) || a.display_order - b.display_order,
  );
  const selectedDomain = orderedDomains.find((domain) => domain.id === domainId) ?? orderedDomains[0];
  const selectedDevice = devices.data?.find((device) => device.id === deviceId) ?? devices.data?.[0];
  const expired = summary?.plan_expires_at ? new Date(summary.plan_expires_at).getTime() <= Date.now() : false;
  const usable = !!summary?.plan_name && summary.status === "active" && !expired;
  const count = Math.max(summary?.devices ?? 0, devices.data?.length ?? 0);
  const atLimit = !!summary && summary.max_devices > 0 && count >= summary.max_devices;
  const accountUrl = profile.data?.sub_token ? getAccountSubscriptionUrl(profile.data.sub_token, format, selectedDomain?.domain) : "";
  const legacyUrl = selectedDevice?.subscription_url ? getSubscriptionUrl(selectedDevice, format, selectedDomain?.domain) : "";
  const url = accountUrl || legacyUrl;

  const addDevice = async () => {
    if (submitting || !usable || atLimit) return;
    setSubmitting(true);
    setAddError(false);
    try {
      const device = await userApi.createDevice({ name: name.trim() || undefined });
      // Creation does not return the account's token-bearing subscription URL.
      // Select the new ID, then fetch the authoritative list before copying/QR.
      devices.setData((current) => [...(current ?? []), device]);
      devices.refresh();
      setDeviceId(device.id);
      setCreated(true);
      setShowAdd(false);
      setName("");
      onCreated();
    } catch {
      setAddError(true);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Container header={
      <Header variant="h2" description={t("user.dashboard.connectDescription")} actions={
        <Button variant="link" onClick={() => navigate("/portal/devices")}>{t("user.dashboard.manageDevices")}</Button>
      }>{t("user.dashboard.quickConnect")}</Header>
    }>
      <SpaceBetween size="m">
        {summary === null && <Box color="text-body-secondary">{t("user.dashboard.accountUnavailable")}</Box>}
        {summary !== null && !usable && (
          <Alert type="info" action={summary?.status === "suspended" ? undefined :
            <Button onClick={() => navigate("/portal/plan")}>{t("user.dashboard.browsePlans")}</Button>
          }>{t(summary?.status === "suspended" ? "user.dashboard.suspendedHint" : "user.dashboard.connectNeedsPlan")}</Alert>
        )}
        {devices.loading ? <Spinner /> : devices.error ? (
          <Alert type="error" action={<Button onClick={devices.refresh}>{t("user.dashboard.retry")}</Button>}>
            {t("user.devices.loadError")}
          </Alert>
        ) : (
          <>
            {created && !!url && <StatusIndicator type="success">{t("user.dashboard.deviceReady")}</StatusIndicator>}
            {atLimit && <StatusIndicator type="warning">{t("user.devices.limitReached")}</StatusIndicator>}
            <Button variant="primary" disabled={!usable || atLimit} onClick={() => { setAddError(false); setShowAdd(true); }}>
              {t("user.devices.addDevice")}
            </Button>
            {(selectedDevice || accountUrl) ? (
              <>
                {!accountUrl && selectedDevice && <FormField label={t("user.dashboard.selectDevice")}>
                  <Select selectedOption={{ value: selectedDevice.id, label: selectedDevice.name || t("user.devices.unnamed") }}
                    options={devices.data?.map((device) => ({ value: device.id, label: device.name || t("user.devices.unnamed") }))}
                    onChange={({ detail }) => { setDeviceId(detail.selectedOption.value); setCreated(false); }} />
                </FormField>}
                <ColumnLayout columns={2}>
                  <FormField label={t("user.dashboard.clientFormat")}>
                    <Select selectedOption={{ value: format, label: SUBSCRIPTION_FORMATS.find((item) => item.id === format)?.label }}
                      options={SUBSCRIPTION_FORMATS.map((item) => ({ value: item.id, label: item.label }))}
                      onChange={({ detail }) => setFormat(detail.selectedOption.value ?? "v2ray")} />
                  </FormField>
                  {orderedDomains.length > 0 && (
                    <FormField label={t("user.dashboard.subscriptionDomain")}>
                      <Select selectedOption={selectedDomain ? { value: selectedDomain.id, label: selectedDomain.domain } : null}
                        options={orderedDomains.map((domain) => ({ value: domain.id, label: domain.domain }))}
                        onChange={({ detail }) => setDomainId(detail.selectedOption.value)} />
                    </FormField>
                  )}
                </ColumnLayout>
                {domains.error && <Alert type="warning">{t("user.dashboard.domainFallback")}</Alert>}
                {domains.loading ? <Spinner /> : !url ? (
                  <Alert type="warning" action={<Button onClick={devices.refresh}>{t("user.dashboard.retry")}</Button>}>
                    {t("user.dashboard.urlUnavailable")}
                  </Alert>
                ) : (
                  <>
                    <FormField label={t(accountUrl ? "user.devices.accountUrl" : "user.devices.subscriptionUrl")} description={t(accountUrl ? "user.devices.accountUrlHint" : "user.dashboard.importHint")}>
                      <Input value={url} readOnly ariaLabel={t(accountUrl ? "user.devices.accountUrl" : "user.devices.subscriptionUrl")} />
                    </FormField>
                    <SpaceBetween direction="horizontal" size="xs">
                      <CopyToClipboard textToCopy={url} copyButtonText={t("user.dashboard.copyUrl")}
                        copySuccessText={t("common.copied")} copyErrorText={t("user.dashboard.copyError")} />
                      <Button onClick={() => setShowQr(true)}>{t("user.devices.showQr")}</Button>
                    </SpaceBetween>
                    {accountUrl && <Box variant="small" color="text-body-secondary">{t("user.devices.hwidHint")}</Box>}
                    {accountUrl && selectedDevice && legacyUrl && <FormField label={t("user.devices.legacyLinks")} description={t("user.devices.legacyHint")}>
                      <Select selectedOption={{ value: selectedDevice.id, label: selectedDevice.name || t("user.devices.unnamed") }}
                        options={devices.data?.map(device => ({ value: device.id, label: device.name || t("user.devices.unnamed") }))}
                        onChange={({ detail }) => setDeviceId(detail.selectedOption.value)} />
                      <Input value={legacyUrl} readOnly ariaLabel={t("user.devices.legacyLinks")} />
                      <CopyToClipboard textToCopy={legacyUrl} copyButtonText={t("user.dashboard.copyUrl")}
                        copySuccessText={t("common.copied")} copyErrorText={t("user.dashboard.copyError")} />
                    </FormField>}
                    {orderedDomains.length > 1 && <Box variant="small" color="text-body-secondary">
                      {t("user.devices.subscriptionDomainWarning")}
                    </Box>}
                  </>
                )}
              </>
            ) : <Box color="text-body-secondary">{t("user.dashboard.firstDeviceHint")}</Box>}
          </>
        )}
      </SpaceBetween>
      <Modal visible={showAdd} closeAriaLabel={t("user.announcements.close")} onDismiss={() => { if (!submitting) setShowAdd(false); }} header={t("user.devices.addDevice")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
          <Button variant="link" disabled={submitting} onClick={() => setShowAdd(false)}>{t("common.cancel")}</Button>
          <Button variant="primary" loading={submitting} disabled={!usable || atLimit} onClick={() => void addDevice()}>
            {t("user.dashboard.addAndGetUrl")}
          </Button>
        </SpaceBetween></Box>}>
        <SpaceBetween size="m">
          {addError && <Alert type="error">{t("user.devices.addError")}</Alert>}
          <FormField label={t("user.devices.deviceName")} description={t("user.devices.deviceNameDesc")}>
            <Input value={name} disabled={submitting} onChange={({ detail }) => setName(detail.value)}
              placeholder={t("user.devices.deviceNamePlaceholder")} />
          </FormField>
        </SpaceBetween>
      </Modal>
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
