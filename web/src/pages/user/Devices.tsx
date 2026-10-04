import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Button,
  SpaceBetween,
  Box,
  Modal,
  FormField,
  Input,
  Spinner,
  Flashbar,
  type FlashbarProps,
  StatusIndicator,
  Container,
  ColumnLayout,
  Tabs,
} from "@cloudscape-design/components";
import { QRCodeSVG } from "qrcode.react";
import type { Device, PublicSubscriptionDomain, UserProfile, UserSummary } from "../../api/types";
import * as userApi from "../../api/user";

import { getAccountSubscriptionUrl, getSubscriptionUrl, SUBSCRIPTION_FORMATS } from "../../utils/subscriptionUrl";

function CopyableUrl({ url }: { url: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      const el = document.createElement("textarea");
      el.value = url;
      document.body.appendChild(el);
      el.select();
      document.execCommand("copy");
      document.body.removeChild(el);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8, width: "100%" }}>
      <code
        style={{
          flex: 1,
          padding: "6px 10px",
          borderRadius: 4,
          fontSize: 12,
          fontFamily: "monospace",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          background: "var(--color-background-input-default, #f4f4f4)",
          border: "1px solid var(--color-border-input-default, #aab7b8)",
          cursor: "text",
          userSelect: "all",
          display: "block",
        }}
        title={url}
      >
        {url}
      </code>
      <Button
        variant="inline-icon"
        iconName={copied ? "status-positive" : "copy"}
        ariaLabel={t("common.copy")}
        onClick={() => void handleCopy()}
      />
    </div>
  );
}

function SubscriptionDomainPicker({ urlForDomain }: { urlForDomain: (domain?: string) => string }) {
  const { t } = useTranslation();
  const [domains, setDomains] = useState<PublicSubscriptionDomain[]>([]);
  const [results, setResults] = useState<Record<string, boolean | undefined>>({});
  const [loading, setLoading] = useState(true);
  const [testing, setTesting] = useState(false);
  const [activeDomain, setActiveDomain] = useState<string | undefined>();

  useEffect(() => {
    const load = async () => {
      try {
        const publicDomains = await userApi.listPublicSubscriptionDomains();
        const ordered = [...publicDomains].sort((a, b) => {
          if (a.is_default !== b.is_default) return a.is_default ? -1 : 1;
          return a.display_order - b.display_order;
        });
        setDomains(ordered);
        setActiveDomain(ordered[0]?.domain);
      } catch {
        // The existing subscription URL is a usable fallback while a deployment
        // is upgraded ahead of its subscription-domain endpoint.
        setDomains([]);
      } finally {
        setLoading(false);
      }
    };
    void load();
  }, []);

  const runReachabilityTest = async () => {
    setTesting(true);
    const outcomes = await Promise.all(
      domains.map(async (domain) => {
        const url = urlForDomain(domain.domain);
        const controller = new AbortController();
        const timeout = window.setTimeout(() => controller.abort(), 8000);
        try {
          // A no-cors request is intentional: the test needs browser network
          // reachability, not readable subscription content or a CORS contract.
          await fetch(url, { method: "HEAD", mode: "no-cors", cache: "no-store", signal: controller.signal });
          return [domain.id, true] as const;
        } catch {
          return [domain.id, false] as const;
        } finally {
          window.clearTimeout(timeout);
        }
      }),
    );
    const nextResults = Object.fromEntries(outcomes);
    setResults(nextResults);
    const firstWorking = domains.find((domain) => nextResults[domain.id]);
    if (firstWorking) setActiveDomain(firstWorking.domain);
    setTesting(false);
  };

  const orderedDomains = useMemo(
    () => [...domains].sort((a, b) => {
      const aResult = results[a.id];
      const bResult = results[b.id];
      if (aResult === true && bResult !== true) return -1;
      if (bResult === true && aResult !== true) return 1;
      if (aResult === false && bResult !== false) return 1;
      if (bResult === false && aResult !== false) return -1;
      if (a.is_default !== b.is_default) return a.is_default ? -1 : 1;
      return a.display_order - b.display_order;
    }),
    [domains, results],
  );

  if (loading) {
    return <Spinner />;
  }

  if (domains.length === 0) {
    return <CopyableUrl url={urlForDomain()} />;
  }

  const selectedDomain = activeDomain ?? orderedDomains[0]?.domain;

  return (
    <SpaceBetween size="s">
      <Flashbar
        items={[{
          type: "warning",
          content: t("user.devices.subscriptionDomainWarning"),
        }]}
      />
      <Box variant="p">{t("user.devices.subscriptionDomainDescription")}</Box>
      <SpaceBetween direction="horizontal" size="xs">
        <Button loading={testing} onClick={() => void runReachabilityTest()}>
          {t("user.devices.testDomains")}
        </Button>
        {testing && <StatusIndicator type="loading">{t("user.devices.testingDomains")}</StatusIndicator>}
      </SpaceBetween>
      <Tabs
        activeTabId={selectedDomain}
        onChange={({ detail }) => setActiveDomain(detail.activeTabId)}
        tabs={orderedDomains.map((domain) => {
          const result = results[domain.id];
          const label = result === true
            ? `${domain.domain} ✓`
            : result === false
              ? `${domain.domain} ✕`
              : domain.domain;
          return {
            id: domain.domain,
            label,
            content: (
              <SpaceBetween size="xs">
                {result !== undefined && (
                  <StatusIndicator type={result ? "success" : "error"}>
                    {t(result ? "user.devices.domainReachable" : "user.devices.domainUnreachable")}
                  </StatusIndicator>
                )}
                <CopyableUrl url={urlForDomain(domain.domain)} />
              </SpaceBetween>
            ),
          };
        })}
      />
    </SpaceBetween>
  );
}

function DeviceCard({
  device,
  onDelete,
  onQr,
}: {
  device: Device;
  onDelete: (d: Device) => void;
  onQr: (d: Device) => void;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const handleCopyUuid = async () => {
    try {
      await navigator.clipboard.writeText(device.xray_uuid);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      const el = document.createElement("textarea");
      el.value = device.xray_uuid;
      document.body.appendChild(el);
      el.select();
      document.execCommand("copy");
      document.body.removeChild(el);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <Container
      header={
        <Header
          variant="h3"
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                variant="inline-icon"
                iconName="video-on"
                ariaLabel={t("user.devices.showQr")}
                onClick={() => onQr(device)}
              />
              <Button
                variant="inline-icon"
                iconName="remove"
                ariaLabel={t("common.delete")}
                onClick={() => onDelete(device)}
              />
            </SpaceBetween>
          }
        >
          {device.name || t("user.devices.unnamed")}
        </Header>
      }
    >
      <SpaceBetween size="m">
        <div>
          <Box variant="awsui-key-label">{t("user.devices.xrayUuid")}</Box>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 4 }}>
            <code
              style={{
                flex: 1,
                padding: "6px 10px",
                borderRadius: 4,
                fontSize: 12,
                fontFamily: "monospace",
                background: "var(--color-background-input-default, #f4f4f4)",
                border: "1px solid var(--color-border-input-default, #aab7b8)",
                userSelect: "all",
                display: "block",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
              title={device.xray_uuid}
            >
              {device.xray_uuid}
            </code>
            <Button
              variant="inline-icon"
              iconName={copied ? "status-positive" : "copy"}
              ariaLabel={t("common.copy")}
              onClick={() => void handleCopyUuid()}
            />
          </div>
        </div>

        <div>
          <Box variant="awsui-key-label">{t("user.devices.yamlUrl")}</Box>
          <Box margin={{ top: "xs" }}>
            <CopyableUrl url={getSubscriptionUrl(device, "clash")} />
          </Box>
        </div>

        <div>
          <Box variant="awsui-key-label">{t("user.devices.subscriptionUrl")}</Box>
          <Box margin={{ top: "xs" }}>
            <SubscriptionDomainPicker urlForDomain={domain => getSubscriptionUrl(device, undefined, domain)} />
          </Box>
        </div>
      </SpaceBetween>
    </Container>
  );
}

export default function Devices() {
  const { t } = useTranslation();
  const [devices, setDevices] = useState<Device[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [showAddModal, setShowAddModal] = useState(false);
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [showQrModal, setShowQrModal] = useState(false);
  const [accountQr, setAccountQr] = useState(false);
  const [selectedDevice, setSelectedDevice] = useState<Device | null>(null);
  const [qrFormat, setQrFormat] = useState("v2ray");
  const [newDeviceName, setNewDeviceName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [summary, setSummary] = useState<UserSummary | null>(null);

  const loadDevices = async () => {
    try {
      setLoading(true);
      const [deviceList, userProfile] = await Promise.all([
        userApi.listDevices(),
        userApi.getProfile(),
      ]);
      setDevices(deviceList);
      setProfile(userProfile);
      setSummary(await userApi.getSummary().catch(() => null));
    } catch {
      setFlash([{ type: "error", content: t("user.devices.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadDevices();
  }, []);

  const handleAddDevice = async () => {
    try {
      setSubmitting(true);
      await userApi.createDevice({ name: newDeviceName || undefined });
      setShowAddModal(false);
      setNewDeviceName("");
      setFlash([{ type: "success", content: t("user.devices.addSuccess"), dismissible: true, onDismiss: () => setFlash([]) }]);
      await loadDevices();
    } catch {
      setFlash([{ type: "error", content: t("user.devices.addError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setSubmitting(false);
    }
  };

  const handleDeleteDevice = async () => {
    if (!selectedDevice) return;
    try {
      setSubmitting(true);
      await userApi.deleteDevice(selectedDevice.id);
      setShowDeleteModal(false);
      setSelectedDevice(null);
      setFlash([{ type: "success", content: t("user.devices.deleteSuccess"), dismissible: true, onDismiss: () => setFlash([]) }]);
      await loadDevices();
    } catch {
      setFlash([{ type: "error", content: t("user.devices.deleteError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setSubmitting(false);
    }
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.devices.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  if (!profile?.plan_name) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.devices.title")}</Header>}>
        <Flashbar items={flash} />
        <Box textAlign="center" padding="xl">
          <StatusIndicator type="info">{t("user.devices.noPlan")}</StatusIndicator>
        </Box>
      </ContentLayout>
    );
  }

  const maxDevices = summary?.max_devices ?? 0;
  const atLimit = maxDevices > 0 && devices.length >= maxDevices;

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          counter={`(${devices.length}${maxDevices > 0 ? `/${maxDevices}` : ""})`}
          actions={
            <Button
              variant="primary"
              disabled={atLimit}
              onClick={() => setShowAddModal(true)}
            >
              {t("user.devices.addDevice")}
            </Button>
          }
          description={
            atLimit
              ? <StatusIndicator type="warning">{t("user.devices.limitReached")}</StatusIndicator>
              : undefined
          }
        >
          {t("user.devices.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        <Flashbar items={flash} />

        {profile?.sub_token && (
          <Container header={<Header variant="h2">{t("user.devices.accountUrl")}</Header>}>
            <SpaceBetween size="s">
              <Box variant="p">{t("user.devices.accountUrlHint")}</Box>
              <SubscriptionDomainPicker urlForDomain={domain => getAccountSubscriptionUrl(profile.sub_token!, undefined, domain)} />
              <Button onClick={() => { setAccountQr(true); setQrFormat("v2ray"); setShowQrModal(true); }}>{t("user.devices.showQr")}</Button>
              <Box variant="small" color="text-body-secondary">{t("user.devices.hwidHint")}</Box>
            </SpaceBetween>
          </Container>
        )}
        {devices.length > 0 && profile?.sub_token && <Box variant="h3">{t("user.devices.legacyLinks")}</Box>}
        {devices.length === 0 ? (
          <Box textAlign="center" padding="xl">
            <SpaceBetween size="m">
              <StatusIndicator type="info">{t("user.devices.empty")}</StatusIndicator>
              <Button variant="primary" onClick={() => setShowAddModal(true)}>
                {t("user.devices.addDevice")}
              </Button>
            </SpaceBetween>
          </Box>
        ) : (
          <ColumnLayout columns={devices.length === 1 ? 1 : 2} borders="none">
            {devices.map((device) => (
              <DeviceCard
                key={device.id}
                device={device}
                onDelete={(d) => { setSelectedDevice(d); setShowDeleteModal(true); }}
                onQr={(d) => { setAccountQr(false); setSelectedDevice(d); setQrFormat("v2ray"); setShowQrModal(true); }}
              />
            ))}
          </ColumnLayout>
        )}
      </SpaceBetween>

      <Modal
        visible={showAddModal}
        onDismiss={() => setShowAddModal(false)}
        header={t("user.devices.addDevice")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setShowAddModal(false)}>{t("common.cancel")}</Button>
              <Button variant="primary" onClick={() => void handleAddDevice()} loading={submitting}>
                {t("common.confirm")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        <FormField label={t("user.devices.deviceName")} description={t("user.devices.deviceNameDesc")}>
          <Input
            value={newDeviceName}
            onChange={({ detail }) => setNewDeviceName(detail.value)}
            placeholder={t("user.devices.deviceNamePlaceholder")}
          />
        </FormField>
      </Modal>

      <Modal
        visible={showDeleteModal}
        onDismiss={() => setShowDeleteModal(false)}
        header={t("user.devices.deleteConfirmTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setShowDeleteModal(false)}>{t("common.cancel")}</Button>
              <Button variant="primary" onClick={() => void handleDeleteDevice()} loading={submitting}>
                {t("common.delete")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        {t("user.devices.deleteConfirmMessage", { name: selectedDevice?.name || t("user.devices.unnamed") })}
      </Modal>

      <Modal
        visible={showQrModal}
        onDismiss={() => setShowQrModal(false)}
        header={t("user.devices.qrTitle")}
        size="medium"
      >
        {(accountQr ? !!profile?.sub_token : !!selectedDevice) && (
          <SpaceBetween size="l">
            <Box textAlign="center">
              <QRCodeSVG value={accountQr ? getAccountSubscriptionUrl(profile!.sub_token!, qrFormat) : getSubscriptionUrl(selectedDevice!, qrFormat)} size={220} />
            </Box>
            <Tabs
              activeTabId={qrFormat}
              onChange={({ detail }) => setQrFormat(detail.activeTabId)}
              tabs={SUBSCRIPTION_FORMATS.map((fmt) => ({
                id: fmt.id,
                label: fmt.label,
                content: (
                  <Box margin={{ top: "xs" }}>
                    <CopyableUrl url={accountQr ? getAccountSubscriptionUrl(profile!.sub_token!, fmt.id) : getSubscriptionUrl(selectedDevice!, fmt.id)} />
                  </Box>
                ),
              }))}
            />
          </SpaceBetween>
        )}
      </Modal>
    </ContentLayout>
  );
}
