import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Cards,
  Button,
  SpaceBetween,
  Box,
  Modal,
  FormField,
  Input,
  Spinner,
  Flashbar,
  type FlashbarProps,
  CopyToClipboard,
  StatusIndicator,
  Tabs,
} from "@cloudscape-design/components";
import { QRCodeSVG } from "qrcode.react";
import type { Device } from "../../api/types";
import * as userApi from "../../api/user";

export default function Devices() {
  const { t } = useTranslation();
  const [devices, setDevices] = useState<Device[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [showAddModal, setShowAddModal] = useState(false);
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [showQrModal, setShowQrModal] = useState(false);
  const [selectedDevice, setSelectedDevice] = useState<Device | null>(null);
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  const getSubscriptionUrl = (device: Device, format?: string): string => {
    const base = device.subscription_url || `${window.location.origin}/sub/${device.xray_uuid}`;
    if (format && format !== "v2ray") {
      return `${base}?format=${format}`;
    }
    return base;
  };

  const subscriptionFormats = [
    { id: "v2ray", label: "V2Ray" },
    { id: "clash", label: "Clash" },
    { id: "singbox", label: "Sing-box" },
    { id: "surfboard", label: "Surfboard" },
    { id: "quantumult", label: "Quantumult" },
  ];

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.devices.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
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

  return (
    <ContentLayout header={<Header variant="h1">{t("user.devices.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />
        <Cards
          cardDefinition={{
            header: (item) => item.name || t("user.devices.unnamed"),
            sections: [
              {
                id: "uuid",
                header: t("user.devices.xrayUuid"),
                content: (item) => (
                  <CopyToClipboard
                    copyButtonAriaLabel="Copy UUID"
                    textToCopy={item.xray_uuid}
                    copySuccessText={t("common.copied")}
                    copyErrorText={t("common.error")}
                    variant="inline"
                  />
                ),
              },
              {
                id: "subscription",
                header: t("user.devices.subscriptionUrl"),
                content: (item) => (
                  <Tabs
                    tabs={subscriptionFormats.map((fmt) => ({
                      id: fmt.id,
                      label: fmt.label,
                      content: (
                        <CopyToClipboard
                          copyButtonAriaLabel={`Copy ${fmt.label} URL`}
                          textToCopy={getSubscriptionUrl(item, fmt.id)}
                          copySuccessText={t("common.copied")}
                          copyErrorText={t("common.error")}
                          variant="inline"
                        />
                      ),
                    }))}
                  />
                ),
              },
              {
                id: "actions",
                content: (item) => (
                  <SpaceBetween direction="horizontal" size="xs">
                    <Button
                      iconName="view-full"
                      variant="icon"
                      onClick={() => {
                        setSelectedDevice(item);
                        setShowQrModal(true);
                      }}
                      ariaLabel={t("user.devices.showQr")}
                    />
                    <Button
                      iconName="remove"
                      variant="icon"
                      onClick={() => {
                        setSelectedDevice(item);
                        setShowDeleteModal(true);
                      }}
                      ariaLabel={t("common.delete")}
                    />
                  </SpaceBetween>
                ),
              },
            ],
          }}
          items={devices}
          header={
            <Header
              counter={`(${devices.length}/${profile?.max_devices ?? "∞"})`}
              actions={
                <Button variant="primary" onClick={() => setShowAddModal(true)}>
                  {t("user.devices.addDevice")}
                </Button>
              }
            >
              {t("user.devices.title")}
            </Header>
          }
          empty={
            <Box textAlign="center" padding="l">
              <SpaceBetween size="m">
                <b>{t("user.devices.empty")}</b>
                <Button variant="primary" onClick={() => setShowAddModal(true)}>
                  {t("user.devices.addDevice")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        />
      </SpaceBetween>

      <Modal
        visible={showAddModal}
        onDismiss={() => setShowAddModal(false)}
        header={t("user.devices.addDevice")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setShowAddModal(false)}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" onClick={handleAddDevice} loading={submitting}>
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
              <Button variant="link" onClick={() => setShowDeleteModal(false)}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" onClick={handleDeleteDevice} loading={submitting}>
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
      >
        {selectedDevice && (
          <Box textAlign="center" padding="l">
            <SpaceBetween size="m">
              <QRCodeSVG value={getSubscriptionUrl(selectedDevice)} size={256} />
              <CopyToClipboard
                copyButtonAriaLabel="Copy URL"
                textToCopy={getSubscriptionUrl(selectedDevice)}
                copySuccessText={t("common.copied")}
                copyErrorText={t("common.error")}
                variant="inline"
              />
            </SpaceBetween>
          </Box>
        )}
      </Modal>
    </ContentLayout>
  );
}
