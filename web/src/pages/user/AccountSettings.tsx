import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Container,
  SpaceBetween,
  Box,
  Button,
  FormField,
  Input,
  Spinner,
  Flashbar,
  type FlashbarProps,
  CopyToClipboard,
  Modal,
  Alert,
} from "@cloudscape-design/components";
import type { UserProfile } from "../../api/types";
import * as userApi from "../../api/user";

export default function AccountSettings() {
  const { t } = useTranslation();
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  const [name, setName] = useState("");
  const [savingProfile, setSavingProfile] = useState(false);

  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [savingPassword, setSavingPassword] = useState(false);

  const [subToken, setSubToken] = useState("");
  const [showRegenModal, setShowRegenModal] = useState(false);
  const [regenerating, setRegenerating] = useState(false);

  useEffect(() => {
    const load = async () => {
      try {
        const data = await userApi.getProfile();
        setProfile(data);
        setName(data.name);
      } catch {
        setFlash([{ type: "error", content: t("user.account.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
      } finally {
        setLoading(false);
      }
    };
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSaveProfile = async () => {
    try {
      setSavingProfile(true);
      const updated = await userApi.updateProfile({ name });
      setProfile(updated);
      setFlash([{ type: "success", content: t("user.account.profileSaved"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } catch {
      setFlash([{ type: "error", content: t("user.account.profileError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setSavingProfile(false);
    }
  };

  const handleSavePassword = async () => {
    if (newPassword !== confirmPassword) {
      setFlash([{ type: "error", content: t("user.account.passwordMismatch"), dismissible: true, onDismiss: () => setFlash([]) }]);
      return;
    }
    if (!currentPassword || !newPassword) {
      setFlash([{ type: "error", content: t("user.account.passwordRequired"), dismissible: true, onDismiss: () => setFlash([]) }]);
      return;
    }
    try {
      setSavingPassword(true);
      await userApi.updateProfile({ password: newPassword });
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      setFlash([{ type: "success", content: t("user.account.passwordSaved"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } catch {
      setFlash([{ type: "error", content: t("user.account.passwordError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setSavingPassword(false);
    }
  };

  const handleRegenerate = async () => {
    try {
      setRegenerating(true);
      const result = await userApi.regenerateSubToken();
      setSubToken(result.sub_token);
      setShowRegenModal(false);
      setFlash([{ type: "success", content: t("user.account.tokenRegenerated"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } catch {
      setFlash([{ type: "error", content: t("user.account.tokenError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setRegenerating(false);
    }
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.account.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={<Header variant="h1">{t("user.account.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />

        <Container header={<Header variant="h2">{t("user.account.profileSection")}</Header>}>
          <SpaceBetween size="m">
            <FormField label={t("user.account.email")}>
              <Input value={profile?.email ?? ""} disabled />
            </FormField>
            <FormField label={t("user.account.name")}>
              <Input value={name} onChange={({ detail }) => setName(detail.value)} />
            </FormField>
            <Button variant="primary" onClick={handleSaveProfile} loading={savingProfile}>
              {t("common.save")}
            </Button>
          </SpaceBetween>
        </Container>

        <Container header={<Header variant="h2">{t("user.account.passwordSection")}</Header>}>
          <SpaceBetween size="m">
            <FormField label={t("user.account.currentPassword")}>
              <Input type="password" value={currentPassword} onChange={({ detail }) => setCurrentPassword(detail.value)} />
            </FormField>
            <FormField label={t("user.account.newPassword")}>
              <Input type="password" value={newPassword} onChange={({ detail }) => setNewPassword(detail.value)} />
            </FormField>
            <FormField label={t("user.account.confirmPassword")}>
              <Input type="password" value={confirmPassword} onChange={({ detail }) => setConfirmPassword(detail.value)} />
            </FormField>
            <Button variant="primary" onClick={handleSavePassword} loading={savingPassword}>
              {t("common.save")}
            </Button>
          </SpaceBetween>
        </Container>

        <Container header={<Header variant="h2">{t("user.account.subscriptionSection")}</Header>}>
          <SpaceBetween size="m">
            <FormField label={t("user.account.subToken")}>
              <CopyToClipboard
                copyButtonAriaLabel="Copy token"
                textToCopy={subToken || "••••••••"}
                copySuccessText={t("common.copied")}
                copyErrorText={t("common.error")}
                variant="inline"
              />
            </FormField>
            <Button onClick={() => setShowRegenModal(true)}>{t("user.account.regenerate")}</Button>
          </SpaceBetween>
        </Container>
      </SpaceBetween>

      <Modal
        visible={showRegenModal}
        onDismiss={() => setShowRegenModal(false)}
        header={t("user.account.regenerateTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setShowRegenModal(false)}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" onClick={handleRegenerate} loading={regenerating}>
                {t("common.confirm")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        <Alert type="warning">{t("user.account.regenerateWarning")}</Alert>
      </Modal>
    </ContentLayout>
  );
}
