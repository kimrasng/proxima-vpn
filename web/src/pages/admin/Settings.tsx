import { useEffect, useState } from "react";
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
  Spinner,
  Table,
  Toggle,
} from "@cloudscape-design/components";
import { getSettings, updateSettings, triggerBackup, listBackups, getBackupDownloadUrl } from "../../api/admin";
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
  subscription_update_interval: "60",
};

export default function Settings() {
  const { t } = useTranslation();
  const [form, setForm] = useState<SettingsForm>(defaultForm);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [backups, setBackups] = useState<BackupEntry[]>([]);
  const [backupLoading, setBackupLoading] = useState(false);
  const [backupTriggerLoading, setBackupTriggerLoading] = useState(false);

  const fetchBackups = async () => {
    setBackupLoading(true);
    try {
      const data = await listBackups();
      setBackups(data.backups);
    } catch {
    } finally {
      setBackupLoading(false);
    }
  };

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
        setForm({ ...defaultForm, ...mapped });
      } catch {
        setError(t("admin.settings.fetchError"));
      } finally {
        setLoading(false);
      }
    };
    void fetchSettings();
    void fetchBackups();
  }, []);

  const handleTriggerBackup = async () => {
    setBackupTriggerLoading(true);
    try {
      await triggerBackup();
      setSuccess(true);
      setError(null);
      await fetchBackups();
    } catch {
      setError(t("admin.settings.backupTriggerError"));
    } finally {
      setBackupTriggerLoading(false);
    }
  };

  const handleSave = async () => {
    setSaving(true);
    setSuccess(false);
    try {
      await updateSettings(form as unknown as Record<string, unknown>);
      setSuccess(true);
      setError(null);
    } catch {
      setError(t("admin.settings.saveError"));
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.settings.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.settings.title")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}
        {success && (
          <Flashbar items={[{ type: "success", content: t("admin.settings.saveSuccess"), dismissible: true, onDismiss: () => setSuccess(false) }]} />
        )}

        <Container header={<Header variant="h2">{t("admin.settings.backup")}</Header>}>
          <SpaceBetween size="m">
            <FormField label={t("admin.settings.s3Endpoint")}>
              <Input value={form.s3_endpoint} onChange={({ detail }) => setForm({ ...form, s3_endpoint: detail.value })} />
            </FormField>
            <FormField label={t("admin.settings.s3Bucket")}>
              <Input value={form.s3_bucket} onChange={({ detail }) => setForm({ ...form, s3_bucket: detail.value })} />
            </FormField>
            <FormField label={t("admin.settings.s3AccessKey")}>
              <Input value={form.s3_access_key} onChange={({ detail }) => setForm({ ...form, s3_access_key: detail.value })} />
            </FormField>
            <FormField label={t("admin.settings.s3SecretKey")}>
              <Input value={form.s3_secret_key} type="password" onChange={({ detail }) => setForm({ ...form, s3_secret_key: detail.value })} />
            </FormField>
            <FormField label={t("admin.settings.s3Region")}>
              <Input value={form.s3_region} onChange={({ detail }) => setForm({ ...form, s3_region: detail.value })} />
            </FormField>
            <FormField label={t("admin.settings.backupSchedule")} description={t("admin.settings.cronHint")}>
              <Input value={form.backup_schedule} onChange={({ detail }) => setForm({ ...form, backup_schedule: detail.value })} />
            </FormField>
            <Box>
              <Button
                variant="normal"
                loading={backupTriggerLoading}
                onClick={() => void handleTriggerBackup()}
              >
                {t("admin.settings.runBackup")}
              </Button>
            </Box>
            <Table
              header={<Header variant="h3">{t("admin.settings.backupList")}</Header>}
              loading={backupLoading}
              items={backups}
              empty={<Box textAlign="center">{t("admin.settings.noBackups")}</Box>}
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

        <Container header={<Header variant="h2">{t("admin.settings.telegram")}</Header>}>
          <SpaceBetween size="m">
            <Toggle
              checked={form.telegram_enabled}
              onChange={({ detail }) => setForm({ ...form, telegram_enabled: detail.checked })}
            >
              {t("admin.settings.telegramEnabled")}
            </Toggle>
            <FormField label={t("admin.settings.telegramBotToken")}>
              <Input value={form.telegram_bot_token} onChange={({ detail }) => setForm({ ...form, telegram_bot_token: detail.value })} />
            </FormField>
            <FormField label={t("admin.settings.telegramChatId")}>
              <Input value={form.telegram_chat_id} onChange={({ detail }) => setForm({ ...form, telegram_chat_id: detail.value })} />
            </FormField>
          </SpaceBetween>
        </Container>

        <Container header={<Header variant="h2">{t("admin.settings.security")}</Header>}>
          <FormField label={t("admin.settings.sessionTimeout")} description={t("admin.settings.secondsHint")}>
            <Input value={form.session_timeout} type="number" onChange={({ detail }) => setForm({ ...form, session_timeout: detail.value })} />
          </FormField>
        </Container>

        <Container header={<Header variant="h2">{t("admin.settings.registration")}</Header>}>
          <Toggle
            checked={form.self_registration}
            onChange={({ detail }) => setForm({ ...form, self_registration: detail.checked })}
          >
            {t("admin.settings.selfRegistration")}
          </Toggle>
        </Container>

        <Container header={<Header variant="h2">{t("admin.settings.subscription")}</Header>}>
          <FormField label={t("admin.settings.updateInterval")} description={t("admin.settings.secondsHint")}>
            <Input value={form.subscription_update_interval} type="number" onChange={({ detail }) => setForm({ ...form, subscription_update_interval: detail.value })} />
          </FormField>
        </Container>

        <Box float="right">
          <Button variant="primary" loading={saving} onClick={() => void handleSave()}>
            {t("admin.settings.save")}
          </Button>
        </Box>
      </SpaceBetween>
    </ContentLayout>
  );
}
