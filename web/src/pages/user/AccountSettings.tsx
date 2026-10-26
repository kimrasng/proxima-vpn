import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  type FlashbarProps,
  Form,
  FormField,
  Header,
  Icon,
  Input,
  KeyValuePairs,
  SpaceBetween,
  Spinner,
  Tabs,
} from "@cloudscape-design/components";
import type { UserProfile } from "../../api/types";
import * as userApi from "../../api/user";
import { focusFirstInvalid, hasFieldErrors } from "../../utils/formValidation";
import PasskeySettings from "../../components/PasskeySettings";
import ConnectedApps from "../../components/ConnectedApps";
import "./accountSettings.css";

const TABS = ["profile", "security", "subscription", "apps"] as const;
type TabId = (typeof TABS)[number];

export default function AccountSettings() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const requested = params.get("tab");
  const activeTab: TabId = TABS.includes(requested as TabId) ? (requested as TabId) : "profile";
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const notify = useCallback((message: FlashbarProps.MessageDefinition) => setFlash([{ ...message, dismissible: true, onDismiss: () => setFlash([]) }]), []);

  const [name, setName] = useState("");
  const [savingProfile, setSavingProfile] = useState(false);

  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [savingPassword, setSavingPassword] = useState(false);
  const [showPasswordErrors, setShowPasswordErrors] = useState(false);
  const passwordErrors = {
    currentPassword: currentPassword ? undefined : t("user.account.currentPasswordRequired"),
    newPassword: !newPassword ? t("user.account.newPasswordRequired")
      : newPassword.length < 8 ? t("user.account.passwordTooShort") : undefined,
    confirmPassword: newPassword && confirmPassword !== newPassword ? t("user.account.passwordMismatch") : undefined,
  };
  const visiblePasswordErrors: Partial<typeof passwordErrors> = showPasswordErrors ? passwordErrors : {};

  useEffect(() => {
    let active = true;
    userApi.getProfile()
      .then((data) => { if (active) { setProfile(data); setName(data.name); } })
      .catch(() => { if (active) notify({ type: "error", content: t("user.account.loadError") }); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [notify, t]);

  const handleSaveProfile = async () => {
    try {
      setSavingProfile(true);
      const updated = await userApi.updateProfile({ name });
      setProfile(updated);
      notify({ type: "success", content: t("user.account.profileSaved") });
    } catch {
      notify({ type: "error", content: t("user.account.profileError") });
    } finally {
      setSavingProfile(false);
    }
  };

  const handleSavePassword = async () => {
    setShowPasswordErrors(true);
    if (hasFieldErrors(passwordErrors)) {
      focusFirstInvalid();
      return;
    }
    try {
      setSavingPassword(true);
      await userApi.updateProfile({ password: newPassword });
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      setShowPasswordErrors(false);
      notify({ type: "success", content: t("user.account.passwordSaved") });
    } catch {
      notify({ type: "error", content: t("user.account.passwordError") });
    } finally {
      setSavingPassword(false);
    }
  };

  const header = <Header variant="h1" description={t("user.account.description")}>{t("user.account.title")}</Header>;
  if (loading) {
    return <ContentLayout header={header}><Box textAlign="center" padding="xl"><Spinner size="large" /></Box></ContentLayout>;
  }

  const joined = profile && "created_at" in profile && typeof (profile as { created_at?: string }).created_at === "string"
    ? new Date((profile as { created_at: string }).created_at).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language)
    : null;

  return (
    <ContentLayout header={header}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />
        <Tabs
          activeTabId={activeTab}
          onChange={({ detail }) => setParams(detail.activeTabId === "profile" ? {} : { tab: detail.activeTabId }, { replace: true })}
          ariaLabel={t("user.account.tabs.label")}
          tabs={[
            {
              id: "profile",
              label: t("user.account.tabs.profile"),
              content: (
                <Container header={<Header variant="h2" description={t("user.account.profileHint")}>{t("user.account.profileSection")}</Header>}>
                  <div className="account-settings__form">
                    <Form actions={<Button variant="primary" onClick={() => void handleSaveProfile()} loading={savingProfile}
                      disabled={name.trim() === (profile?.name ?? "")}>{t("common.save")}</Button>}>
                      <SpaceBetween size="l">
                        <FormField label={t("user.account.email")} description={t("user.account.emailHint")}>
                          <Input value={profile?.email ?? ""} readOnly />
                        </FormField>
                        <FormField label={t("user.account.name")} description={t("user.account.nameHint")}>
                          <Input value={name} onChange={({ detail }) => setName(detail.value)} />
                        </FormField>
                        {joined && <KeyValuePairs items={[{ label: t("user.account.joined"), value: joined }]} />}
                      </SpaceBetween>
                    </Form>
                  </div>
                </Container>
              ),
            },
            {
              id: "security",
              label: t("user.account.tabs.security"),
              content: (
                <SpaceBetween size="l">
                  <PasskeySettings onMessage={notify} />
                  <Container header={<Header variant="h2" description={t("user.account.passwordHint")}>{t("user.account.passwordSection")}</Header>}>
                    <ColumnLayout columns={2} minColumnWidth={320}>
                      <div className="account-settings__form">
                        <Form actions={<Button variant="primary" onClick={() => void handleSavePassword()} loading={savingPassword}>
                          {t("user.account.changePassword")}</Button>}>
                          <SpaceBetween size="l">
                            <FormField label={t("user.account.currentPassword")} errorText={visiblePasswordErrors.currentPassword}>
                              <Input type="password" autoComplete="current-password" value={currentPassword}
                                onChange={({ detail }) => setCurrentPassword(detail.value)} />
                            </FormField>
                            <FormField label={t("user.account.newPassword")} constraintText={t("user.account.passwordRule")}
                              errorText={visiblePasswordErrors.newPassword}>
                              <Input type="password" autoComplete="new-password" value={newPassword}
                                onChange={({ detail }) => setNewPassword(detail.value)} />
                            </FormField>
                            <FormField label={t("user.account.confirmPassword")} errorText={visiblePasswordErrors.confirmPassword}>
                              <Input type="password" autoComplete="new-password" value={confirmPassword}
                                onChange={({ detail }) => setConfirmPassword(detail.value)} />
                            </FormField>
                          </SpaceBetween>
                        </Form>
                      </div>
                      <Box color="text-body-secondary">
                        <SpaceBetween size="s">
                          <Box variant="h4">{t("user.account.passwordTipsTitle")}</Box>
                          <ul className="account-settings__tips">
                            <li>{t("user.account.passwordTip1")}</li>
                            <li>{t("user.account.passwordTip2")}</li>
                            <li>{t("user.account.passwordTip3")}</li>
                          </ul>
                        </SpaceBetween>
                      </Box>
                    </ColumnLayout>
                  </Container>
                </SpaceBetween>
              ),
            },
            {
              id: "subscription",
              label: t("user.account.tabs.subscription"),
              content: (
                <Container header={<Header variant="h2" description={t("user.account.subscriptionHint")}>{t("user.account.subscriptionSection")}</Header>}>
                  <SpaceBetween size="l">
                    <div className="account-settings__explain">
                      <Icon name="key" size="medium" variant="subtle" />
                      <Box>{t("user.account.subscriptionExplain")}</Box>
                    </div>
                    <ColumnLayout columns={2} minColumnWidth={260}>
                      <SpaceBetween size="xs">
                        <Box variant="h4">{t("user.account.linkUseTitle")}</Box>
                        <Box color="text-body-secondary">{t("user.account.linkUseBody")}</Box>
                        <Box><Button variant="inline-link" onClick={() => navigate("/portal/devices")}>{t("user.account.linkUseAction")}</Button></Box>
                      </SpaceBetween>
                      <SpaceBetween size="xs">
                        <Box variant="h4">{t("user.account.linkLeakTitle")}</Box>
                        <Box color="text-body-secondary">{t("user.account.linkLeakBody")}</Box>
                        <Box><Button onClick={() => navigate("/portal/subscription-reset")} disabled={!profile?.sub_token}>
                          {t("user.account.linkLeakAction")}</Button></Box>
                      </SpaceBetween>
                    </ColumnLayout>
                    {!profile?.sub_token && <Alert type="info">{t("user.account.noSubscriptionYet")}</Alert>}
                  </SpaceBetween>
                </Container>
              ),
            },
            {
              id: "apps",
              label: t("user.account.tabs.apps"),
              content: <ConnectedApps onMessage={notify} />,
            },
          ]}
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
