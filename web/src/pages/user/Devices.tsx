import { useEffect, useState } from "react";
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
import type { Device } from "../../api/types";
import * as userApi from "../../api/user";

const SUBSCRIPTION_FORMATS = [
  { id: "v2ray", label: "V2Ray" },
  { id: "clash", label: "Clash" },
  { id: "singbox", label: "Sing-box" },
  { id: "surfboard", label: "Surfboard" },
  { id: "quantumult", label: "Quantumult" },
];

function getSubscriptionUrl(device: Device, format?: string): string {
  const raw = device.subscription_url || `/sub/${device.xray_uuid}`;
  // subscription_url is a relative path (/sub/<token>/<id>); make it absolute so
  // copied links and QR codes work when imported into a client.
  const base = /^https?:\/\//i.test(raw)
    ? raw
    : `${window.location.origin}${raw.startsWith("/") ? "" : "/"}${raw}`;
  if (format && format !== "v2ray") return `${base}?format=${format}`;
  return base;
}

function CopyableUrl({ url }: { url: string }) {
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
        ariaLabel="Copy"
        onClick={() => void handleCopy()}
      />
    </div>
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
  const [activeFormat, setActiveFormat] = useState("v2ray");
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
              ariaLabel="Copy UUID"
              onClick={() => void handleCopyUuid()}
            />
          </div>
        </div>

        <div>
          <Box variant="awsui-key-label">{t("user.devices.subscriptionUrl")}</Box>
          <Box margin={{ top: "xs" }}>
            <Tabs
              activeTabId={activeFormat}
              onChange={({ detail }) => setActiveFormat(detail.activeTabId)}
              tabs={SUBSCRIPTION_FORMATS.map((fmt) => ({
                id: fmt.id,
                label: fmt.label,
                content: (
                  <Box margin={{ top: "xs" }}>
                    <CopyableUrl url={getSubscriptionUrl(device, fmt.id)} />
                  </Box>
                ),
              }))}
            />
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
  const [selectedDevice, setSelectedDevice] = useState<Device | null>(null);
  const [qrFormat, setQrFormat] = useState("v2ray");
  const [newDeviceName, setNewDeviceName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [profile, setProfile] = useState<{ plan_name?: string; max_devices?: number } | null>(null);

  const loadDevices = async () => {
    try {
      setLoading(true);
      const [deviceList, userProfile] = await Promise.all([
        userApi.listDevices(),
        userApi.getProfile(),
      ]);
      setDevices(deviceList);
      setProfile(userProfile);
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

  const maxDevices = profile?.max_devices ?? 0;
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
                onQr={(d) => { setSelectedDevice(d); setQrFormat("v2ray"); setShowQrModal(true); }}
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
        {selectedDevice && (
          <SpaceBetween size="l">
            <Box textAlign="center">
              <QRCodeSVG value={getSubscriptionUrl(selectedDevice, qrFormat)} size={220} />
            </Box>
            <Tabs
              activeTabId={qrFormat}
              onChange={({ detail }) => setQrFormat(detail.activeTabId)}
              tabs={SUBSCRIPTION_FORMATS.map((fmt) => ({
                id: fmt.id,
                label: fmt.label,
                content: (
                  <Box margin={{ top: "xs" }}>
                    <CopyableUrl url={getSubscriptionUrl(selectedDevice, fmt.id)} />
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
