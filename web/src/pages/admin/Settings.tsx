import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  Flashbar,
  FormField,
  Header,
  Input,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  Textarea,
  Toggle,
} from "@cloudscape-design/components";
import { getSettings, updateSettings, triggerBackup, listBackups, getBackupDownloadUrl, testS3Connection, testTelegramConnection, type ConnectionResult } from "../../api/admin";
import { ApiError } from "../../api/client";
import ConnectionTestResult from "./ConnectionTestResult";
import type { BackupEntry } from "../../api/types";

interface SettingsForm {
  s3_endpoint: string;
  s3_bucket: string;
  s3_access_key: string;
  s3_secret_key: string;
  s3_region: string;
  backup_schedule: string;
  telegram_enabled: boolean;
  telegram_bot_token: string;
  telegram_chat_id: string;
  session_timeout: string;
  self_registration: boolean;
  signup_email_domain_restriction: boolean;
  signup_email_domains: string;
  subscription_update_interval: string;
}

const defaultForm: SettingsForm = {
  s3_endpoint: "",
  s3_bucket: "",
  s3_access_key: "",
  s3_secret_key: "",
  s3_region: "",
  backup_schedule: "0 2 * * *",
  telegram_enabled: false,
  telegram_bot_token: "",
  telegram_chat_id: "",
  session_timeout: "3600",
  self_registration: true,
  signup_email_domain_restriction: false,
  // Same list the server seeds, so the field is never empty-looking before the first load.
  signup_email_domains:
    "gmail.com, qq.com, 163.com, yahoo.com, sina.com, 126.com, outlook.com, yeah.net, foxmail.com, icloud.com, hotmail.com, aliyun.com, sohu.com, mail.com, live.com, protonmail.com",
  subscription_update_interval: "60",
};

const backupKeys: (keyof SettingsForm)[] = ["s3_endpoint", "s3_bucket", "s3_access_key", "s3_secret_key", "s3_region", "backup_schedule"];

// Mirrors the server's normalisation so the admin sees exactly what will be stored.
function parseDomains(raw: string): string[] {
  const seen = new Set<string>();
  for (const part of raw.split(/[\s,;]+/)) {
    let d = part.trim().toLowerCase();
    const at = d.lastIndexOf("@");
    if (at >= 0) d = d.slice(at + 1);
    d = d.replace(/^\.+|\.+$/g, "");
    if (/^(?=.{3,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(d)) seen.add(d);
  }
  return [...seen];
}

function positiveSeconds(raw: string): number | null {
  return /^\d+$/.test(raw.trim()) && Number(raw) > 0 ? Number(raw) : null;
}

// "3600" -> "1시간", "90061" -> "1일 1시간 1분 1초"; the server stores plain seconds.
function humanDuration(total: number, t: TFunction): string {
  const parts: string[] = [];
  const units: [number, string][] = [[86400, "admin.settings.durDay"], [3600, "admin.settings.durHour"], [60, "admin.settings.durMinute"], [1, "admin.settings.durSecond"]];
  let rest = total;
  for (const [size, key] of units) {
    const n = Math.floor(rest / size);
    if (n > 0) parts.push(t(key, { count: n }));
    rest %= size;
  }
  return parts.join(" ");
}

export default function Settings() {
  const { t } = useTranslation();
  const tRef = useRef(t);
  tRef.current = t;
  const [form, setForm] = useState<SettingsForm>(defaultForm);
  const [saved, setSaved] = useState<SettingsForm>(defaultForm);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [backups, setBackups] = useState<BackupEntry[]>([]);
  const [backupLoading, setBackupLoading] = useState(false);
  const [backupTriggerLoading, setBackupTriggerLoading] = useState(false);
  type TestState = { running: boolean; result: ConnectionResult | null; error: string | null };
  const idle: TestState = { running: false, result: null, error: null };
  const [s3Test, setS3Test] = useState<TestState>(idle);
  const [tgTest, setTgTest] = useState<TestState>(idle);

  // Tests run on the form as typed, so an admin can check credentials before saving.
  const runTest = async (kind: "s3" | "telegram") => {
    const setState = kind === "s3" ? setS3Test : setTgTest;
    setState({ running: true, result: null, error: null });
    try {
      const result = kind === "s3"
        ? await testS3Connection({ s3_endpoint: form.s3_endpoint, s3_bucket: form.s3_bucket, s3_access_key: form.s3_access_key, s3_secret_key: form.s3_secret_key, s3_region: form.s3_region })
        : await testTelegramConnection({ telegram_bot_token: form.telegram_bot_token, telegram_chat_id: form.telegram_chat_id });
      setState({ running: false, result, error: null });
    } catch (err) {
      const message = err instanceof ApiError ? (err.body as { error?: string } | undefined)?.error : undefined;
      setState({ running: false, result: null, error: message || t("admin.settings.test.requestError") });
    }
  };

  const fetchBackups = useCallback(async () => {
    setBackupLoading(true);
    try {
      const data = await listBackups();
      setBackups(data.backups);
    } catch (err) {
      // Backups are an optional feature (only enabled when S3 is configured
      // server-side, see api-server/cmd/main.go), so a 404 here is the normal
      // case for most deployments and shouldn't alarm the admin - but log it
      // so a real failure (S3 misconfigured, network error) isn't invisible.
      console.warn("failed to list backups", err);
    } finally {
      setBackupLoading(false);
    }
  }, []);

  useEffect(() => {
    const fetchSettings = async () => {
      try {
        const data = await getSettings();
        const mapped: Partial<SettingsForm> = {};
        for (const setting of data) {
          const key = setting.key as keyof SettingsForm;
          if (key in defaultForm) {
            const val = setting.value;
            if (typeof defaultForm[key] === "boolean") {
              (mapped as Record<string, unknown>)[key] = val === true || val === "true";
            } else {
              (mapped as Record<string, unknown>)[key] = String(val ?? "");
            }
          }
        }
        const loaded = { ...defaultForm, ...mapped };
        setForm(loaded);
        setSaved(loaded);
      } catch {
        setError(tRef.current("admin.settings.fetchError"));
      } finally {
        setLoading(false);
      }
    };
    void fetchSettings();
    void fetchBackups();
  }, [fetchBackups]);

  const set = <K extends keyof SettingsForm>(key: K, value: SettingsForm[K]) => setForm((current) => ({ ...current, [key]: value }));

  const domainList = parseDomains(form.signup_email_domains);
  const sessionSeconds = positiveSeconds(form.session_timeout);
  const intervalSeconds = positiveSeconds(form.subscription_update_interval);
  const dirty = JSON.stringify(form) !== JSON.stringify(saved);
  const backupDirty = backupKeys.some((key) => form[key] !== saved[key]);
  const backupReady = Boolean(saved.s3_endpoint && saved.s3_bucket && saved.s3_access_key && saved.s3_secret_key);
  const invalid = sessionSeconds === null || intervalSeconds === null || (form.signup_email_domain_restriction && domainList.length === 0);

  const handleTriggerBackup = async () => {
    setBackupTriggerLoading(true);
    try {
      await triggerBackup();
      setNotice(t("admin.settings.backupTriggerSuccess"));
      setError(null);
      await fetchBackups();
    } catch {
      setError(t("admin.settings.backupTriggerError"));
    } finally {
      setBackupTriggerLoading(false);
    }
  };

  const handleSave = async () => {
    if (form.signup_email_domain_restriction && domainList.length === 0) {
      // Saving this would lock every new user out of registration.
      setError(t("admin.settings.signupDomainsEmpty"));
      return;
    }
    if (invalid) {
      setError(t("admin.settings.fixErrors"));
      return;
    }
    setSaving(true);
    setNotice(null);
    try {
      // Store the cleaned list, not the raw text, so what the server enforces is what the admin saw.
      const next = { ...form, signup_email_domains: domainList.join(", ") };
      await updateSettings(next as unknown as Record<string, unknown>);
      setForm(next);
      setSaved(next);
      setNotice(t("admin.settings.saveSuccess"));
      setError(null);
    } catch {
      setError(t("admin.settings.saveError"));
    } finally {
      setSaving(false);
    }
  };

  const saveButton = (
    <Button variant="primary" loading={saving} disabled={!dirty} onClick={() => void handleSave()}>
      {t("admin.settings.save")}
    </Button>
  );

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.settings.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={t("admin.settings.description")}
          actions={
            <SpaceBetween direction="horizontal" size="xs" alignItems="center">
              {dirty && <StatusIndicator type="warning">{t("admin.settings.unsaved")}</StatusIndicator>}
              <Button disabled={!dirty || saving} onClick={() => setForm(saved)}>{t("admin.settings.discard")}</Button>
              {saveButton}
            </SpaceBetween>
          }
        >
          {t("admin.settings.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}
        {notice && (
          <Flashbar items={[{ type: "success", content: notice, dismissible: true, onDismiss: () => setNotice(null) }]} />
        )}

        <Container header={<Header variant="h2" description={t("admin.settings.registrationDesc")}>{t("admin.settings.registration")}</Header>}>
          <SpaceBetween size="l">
            <Toggle
              checked={form.self_registration}
              onChange={({ detail }) => set("self_registration", detail.checked)}
              description={t("admin.settings.selfRegistrationHint")}
            >
              {t("admin.settings.selfRegistration")}
            </Toggle>
            <Toggle
              checked={form.signup_email_domain_restriction}
              onChange={({ detail }) => set("signup_email_domain_restriction", detail.checked)}
              description={t("admin.settings.signupDomainRestrictionHint")}
            >
              {t("admin.settings.signupDomainRestriction")}
            </Toggle>
            <FormField
              stretch
              label={t("admin.settings.signupDomains")}
              description={form.signup_email_domain_restriction ? t("admin.settings.signupDomainsHint") : t("admin.settings.signupDomainsOff")}
              errorText={
                form.signup_email_domain_restriction && domainList.length === 0
                  ? t("admin.settings.signupDomainsEmpty")
                  : undefined
              }
              constraintText={t("admin.settings.signupDomainsCount", { count: domainList.length })}
            >
              <Textarea
                value={form.signup_email_domains}
                rows={4}
                disabled={!form.signup_email_domain_restriction}
                placeholder="gmail.com, qq.com"
                onChange={({ detail }) => set("signup_email_domains", detail.value)}
              />
            </FormField>
          </SpaceBetween>
        </Container>

        <ColumnLayout columns={2}>
          <Container fitHeight header={<Header variant="h2" description={t("admin.settings.securityDesc")}>{t("admin.settings.security")}</Header>}>
            <FormField
              stretch
              label={t("admin.settings.sessionTimeout")}
              description={t("admin.settings.sessionTimeoutHint")}
              errorText={sessionSeconds === null ? t("admin.settings.invalidSeconds") : undefined}
              constraintText={sessionSeconds !== null ? t("admin.settings.durEquals", { value: humanDuration(sessionSeconds, t) }) : undefined}
            >
              <Input value={form.session_timeout} type="number" inputMode="numeric" onChange={({ detail }) => set("session_timeout", detail.value)} />
            </FormField>
          </Container>
          <Container fitHeight header={<Header variant="h2" description={t("admin.settings.subscriptionDesc")}>{t("admin.settings.subscription")}</Header>}>
            <FormField
              stretch
              label={t("admin.settings.updateInterval")}
              description={t("admin.settings.updateIntervalHint")}
              errorText={intervalSeconds === null ? t("admin.settings.invalidSeconds") : undefined}
              constraintText={intervalSeconds !== null ? t("admin.settings.durEquals", { value: humanDuration(intervalSeconds, t) }) : undefined}
            >
              <Input value={form.subscription_update_interval} type="number" inputMode="numeric" onChange={({ detail }) => set("subscription_update_interval", detail.value)} />
            </FormField>
          </Container>
        </ColumnLayout>

        <Container header={<Header variant="h2" description={t("admin.settings.telegramDesc")}>{t("admin.settings.telegram")}</Header>}>
          <SpaceBetween size="l">
            <Toggle
              checked={form.telegram_enabled}
              onChange={({ detail }) => set("telegram_enabled", detail.checked)}
              description={t("admin.settings.telegramEnabledHint")}
            >
              {t("admin.settings.telegramEnabled")}
            </Toggle>
            <ColumnLayout columns={2}>
              <FormField stretch label={t("admin.settings.telegramBotToken")} description={t("admin.settings.telegramBotTokenHint")}>
                <Input
                  value={form.telegram_bot_token}
                  type="password"
                  disabled={!form.telegram_enabled}
                  placeholder="123456789:AA..."
                  onChange={({ detail }) => set("telegram_bot_token", detail.value)}
                />
              </FormField>
              <FormField stretch label={t("admin.settings.telegramChatId")} description={t("admin.settings.telegramChatIdHint")}>
                <Input
                  value={form.telegram_chat_id}
                  disabled={!form.telegram_enabled}
                  placeholder="-1001234567890"
                  onChange={({ detail }) => set("telegram_chat_id", detail.value)}
                />
              </FormField>
            </ColumnLayout>
            <div className="conn-test-row">
              <Button iconName="send" loading={tgTest.running} disabled={!form.telegram_bot_token.trim() || !form.telegram_chat_id.trim()} onClick={() => void runTest("telegram")}>
                {t("admin.settings.test.telegram.button")}
              </Button>
              <Box color="text-body-secondary" fontSize="body-s">{t("admin.settings.test.telegram.hint")}</Box>
            </div>
            <ConnectionTestResult kind="telegram" result={tgTest.result} error={tgTest.error} />
          </SpaceBetween>
        </Container>

        <Container
          header={
            <Header
              variant="h2"
              description={t("admin.settings.backupDesc")}
              info={
                backupReady
                  ? <StatusIndicator type="success">{t("admin.settings.backupConfigured")}</StatusIndicator>
                  : <StatusIndicator type="stopped">{t("admin.settings.backupNotConfigured")}</StatusIndicator>
              }
            >
              {t("admin.settings.backup")}
            </Header>
          }
        >
          <SpaceBetween size="l">
            <ColumnLayout columns={2}>
              <FormField stretch label={t("admin.settings.s3Endpoint")} description={t("admin.settings.s3EndpointHint")}>
                <Input value={form.s3_endpoint} placeholder="https://s3.ap-northeast-2.amazonaws.com" onChange={({ detail }) => set("s3_endpoint", detail.value)} />
              </FormField>
              <FormField stretch label={t("admin.settings.s3Region")} description={t("admin.settings.s3RegionHint")}>
                <Input value={form.s3_region} placeholder="ap-northeast-2" onChange={({ detail }) => set("s3_region", detail.value)} />
              </FormField>
              <FormField stretch label={t("admin.settings.s3Bucket")} description={t("admin.settings.s3BucketHint")}>
                <Input value={form.s3_bucket} placeholder="proxima-backups" onChange={({ detail }) => set("s3_bucket", detail.value)} />
              </FormField>
              <FormField stretch label={t("admin.settings.backupSchedule")} description={t("admin.settings.cronHint")}>
                <Input value={form.backup_schedule} placeholder="0 2 * * *" onChange={({ detail }) => set("backup_schedule", detail.value)} />
              </FormField>
              <FormField stretch label={t("admin.settings.s3AccessKey")} description={t("admin.settings.s3KeyHint")}>
                <Input value={form.s3_access_key} onChange={({ detail }) => set("s3_access_key", detail.value)} />
              </FormField>
              <FormField stretch label={t("admin.settings.s3SecretKey")} description={t("admin.settings.s3KeyHint")}>
                <Input value={form.s3_secret_key} type="password" onChange={({ detail }) => set("s3_secret_key", detail.value)} />
              </FormField>
            </ColumnLayout>
            <div className="conn-test-row">
              <Button iconName="status-positive" loading={s3Test.running} disabled={!form.s3_endpoint.trim() || !form.s3_bucket.trim()} onClick={() => void runTest("s3")}>
                {t("admin.settings.test.s3.button")}
              </Button>
              <Box color="text-body-secondary" fontSize="body-s">{t("admin.settings.test.s3.hint")}</Box>
            </div>
            <ConnectionTestResult kind="s3" result={s3Test.result} error={s3Test.error} />
            <SpaceBetween direction="horizontal" size="s" alignItems="center">
              <Button
                iconName="upload"
                loading={backupTriggerLoading}
                disabled={!backupReady || backupDirty}
                onClick={() => void handleTriggerBackup()}
              >
                {t("admin.settings.runBackup")}
              </Button>
              <Box color="text-body-secondary" fontSize="body-s">
                {!backupReady
                  ? t("admin.settings.runBackupNeedsConfig")
                  : backupDirty
                    ? t("admin.settings.runBackupNeedsSave")
                    : t("admin.settings.runBackupHint")}
              </Box>
            </SpaceBetween>
            <Table
              variant="embedded"
              header={<Header variant="h3" counter={backups.length ? `(${backups.length})` : undefined}>{t("admin.settings.backupList")}</Header>}
              loading={backupLoading}
              items={backups}
              empty={<Box textAlign="center" color="text-body-secondary">{t("admin.settings.noBackups")}</Box>}
              columnDefinitions={[
                {
                  id: "key",
                  header: t("admin.settings.backupKey"),
                  cell: (item: BackupEntry) => (
                    <Box>{item.key.length > 60 ? `...${item.key.slice(-57)}` : item.key}</Box>
                  ),
                },
                {
                  id: "size",
                  header: t("admin.settings.backupSize"),
                  cell: (item: BackupEntry) => {
                    if (item.size == null) return "-";
                    if (item.size >= 1024 * 1024) return `${(item.size / (1024 * 1024)).toFixed(1)} MB`;
                    return `${(item.size / 1024).toFixed(1)} KB`;
                  },
                },
                {
                  id: "last_modified",
                  header: t("admin.settings.backupDate"),
                  cell: (item: BackupEntry) =>
                    item.last_modified ? new Date(item.last_modified).toLocaleString() : "-",
                },
                {
                  id: "actions",
                  header: t("admin.settings.backupDownload"),
                  cell: (item: BackupEntry) => (
                    <Button
                      variant="inline-link"
                      onClick={() => window.open(getBackupDownloadUrl(item.key), "_blank")}
                    >
                      {t("admin.settings.backupDownload")}
                    </Button>
                  ),
                },
              ]}
            />
          </SpaceBetween>
        </Container>

        <Box float="right">
          <SpaceBetween direction="horizontal" size="xs" alignItems="center">
            {dirty && <StatusIndicator type="warning">{t("admin.settings.unsaved")}</StatusIndicator>}
            {saveButton}
          </SpaceBetween>
        </Box>
      </SpaceBetween>
    </ContentLayout>
  );
}
