import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  Container,
  ContentLayout,
  Flashbar,
  FormField,
  Header,
  Input,
  SpaceBetween,
  StatusIndicator,
} from "@cloudscape-design/components";
import { QRCodeSVG } from "qrcode.react";
import { admin2FAStatus, admin2FASetup, admin2FAEnable, admin2FADisable } from "../../api/admin";
import type { TwoFASetup } from "../../api/types";

export default function TwoFactor() {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState(false);
  const [setupData, setSetupData] = useState<TwoFASetup | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState(false);
  const [statusLoading, setStatusLoading] = useState(true);

  useEffect(() => {
    admin2FAStatus()
      .then((data) => setEnabled(data.enabled))
      .catch(() => {})
      .finally(() => setStatusLoading(false));
  }, []);

  const handleSetup = async () => {
    setActionLoading(true);
    setError(null);
    try {
      const data = await admin2FASetup();
      setSetupData(data);
    } catch {
      setError(t("admin.twoFactor.setupError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEnable = async () => {
    if (!setupData) return;
    setActionLoading(true);
    setError(null);
    try {
      await admin2FAEnable({ secret: setupData.secret, code });
      setEnabled(true);
      setSetupData(null);
      setCode("");
      setSuccess(t("admin.twoFactor.enableSuccess"));
    } catch {
      setError(t("admin.twoFactor.enableError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleDisable = async () => {
    setActionLoading(true);
    setError(null);
    try {
      await admin2FADisable({ totp_code: code });
      setEnabled(false);
      setCode("");
      setSuccess(t("admin.twoFactor.disableSuccess"));
    } catch {
      setError(t("admin.twoFactor.disableError"));
    } finally {
      setActionLoading(false);
    }
  };

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.twoFactor.title")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}
        {success && (
          <Flashbar items={[{ type: "success", content: success, dismissible: true, onDismiss: () => setSuccess(null) }]} />
        )}

        <Container header={<Header variant="h2">{t("admin.twoFactor.status")}</Header>}>
          <SpaceBetween size="m">
            {statusLoading ? (
              <StatusIndicator type="loading">{t("admin.twoFactor.loading", "Loading...")}</StatusIndicator>
            ) : (
              <StatusIndicator type={enabled ? "success" : "warning"}>
                {enabled ? t("admin.twoFactor.enabled") : t("admin.twoFactor.disabled")}
              </StatusIndicator>
            )}

            {!enabled && !setupData && (
              <Button variant="primary" loading={actionLoading} onClick={() => void handleSetup()}>
                {t("admin.twoFactor.enableBtn")}
              </Button>
            )}

            {!enabled && setupData && (
              <SpaceBetween size="m">
                <Box variant="awsui-key-label">{t("admin.twoFactor.scanQR", "Scan this QR code with Google Authenticator or any TOTP app")}</Box>
                <div style={{ background: "#fff", display: "inline-block", padding: "12px", borderRadius: "8px" }}>
                  <QRCodeSVG value={setupData.url} size={200} level="M" />
                </div>
                <Box variant="awsui-key-label">{t("admin.twoFactor.secret")}</Box>
                <Box variant="code" fontSize="heading-s">{setupData.secret}</Box>
                <FormField label={t("admin.twoFactor.verifyCode")} description={t("admin.twoFactor.verifyHint")}>
                  <Input
                    value={code}
                    onChange={({ detail }) => setCode(detail.value)}
                    placeholder="000000"
                  />
                </FormField>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleEnable()}>
                  {t("admin.twoFactor.verify")}
                </Button>
              </SpaceBetween>
            )}

            {enabled && (
              <SpaceBetween size="m">
                <FormField label={t("admin.twoFactor.disableCode")} description={t("admin.twoFactor.disableHint")}>
                  <Input
                    value={code}
                    onChange={({ detail }) => setCode(detail.value)}
                    placeholder="000000"
                  />
                </FormField>
                <Button loading={actionLoading} onClick={() => void handleDisable()}>
                  {t("admin.twoFactor.disableBtn")}
                </Button>
              </SpaceBetween>
            )}
          </SpaceBetween>
        </Container>
      </SpaceBetween>
    </ContentLayout>
  );
}
