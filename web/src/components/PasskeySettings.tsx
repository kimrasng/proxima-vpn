import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Box, Button, Container, FormField, Input, Modal, SpaceBetween, StatusIndicator } from "@cloudscape-design/components";
import type { FlashbarProps } from "@cloudscape-design/components";
import { browserSupportsWebAuthn, startRegistration, WebAuthnError } from "@simplewebauthn/browser";
import { ApiError } from "../api/client";
import { beginPasskeyRegistration, deletePasskey, getPasskeys, finishPasskeyRegistration } from "../api/passkey";
import type { Passkey } from "../api/passkey";
import { focusFirstInvalid } from "../utils/formValidation";

type Props = { onMessage: (message: FlashbarProps.MessageDefinition) => void };

export default function PasskeySettings({ onMessage }: Props) {
  const { t, i18n } = useTranslation();
  const [items, setItems] = useState<Passkey[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [showRemove, setShowRemove] = useState(false);
  const [selected, setSelected] = useState<Passkey | null>(null);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [attempted, setAttempted] = useState(false);
  const [passwordError, setPasswordError] = useState("");
  const supported = browserSupportsWebAuthn();

  const load = useCallback(async () => {
    try { setItems(await getPasskeys()); }
    catch { onMessage({ type: "error", content: t("user.passkey.loadError"), dismissible: true }); }
    finally { setLoading(false); }
  }, [onMessage, t]);
  useEffect(() => { void load(); }, [load]);

  const closeAdd = () => { setShowAdd(false); setPassword(""); setName(""); setAttempted(false); setPasswordError(""); };
  const add = async () => {
    setAttempted(true);
    if (!password.trim()) { focusFirstInvalid(); return; }
    setSaving(true); setPasswordError("");
    try {
      const begin = await beginPasskeyRegistration(password);
      const credential = await startRegistration({ optionsJSON: begin.options });
      await finishPasskeyRegistration(name.trim(), credential);
      closeAdd(); await load();
      onMessage({ type: "success", content: t("user.passkey.added"), dismissible: true });
    } catch (error) {
      if (error instanceof ApiError && error.status === 403) setPasswordError(t("user.passkey.incorrectPassword"));
      else if (error instanceof WebAuthnError && ["ERROR_CEREMONY_ABORTED", "ERROR_NOT_ALLOWED", "ERROR_AUTHENTICATOR_PREVIOUSLY_REGISTERED"].includes(error.code)) {
        onMessage({ type: "info", content: error.code === "ERROR_AUTHENTICATOR_PREVIOUSLY_REGISTERED" ? t("user.passkey.alreadyRegistered") : t("user.passkey.cancelled"), dismissible: true, dismissLabel: t("common.close") });
      } else {
        const body = error instanceof ApiError ? error.body as { error?: string } | undefined : undefined;
        onMessage({ type: "error", content: body?.error || t("user.passkey.addError"), dismissible: true, dismissLabel: t("common.close") });
      }
    } finally { setSaving(false); }
  };

  const remove = async () => {
    if (!selected) return;
    setSaving(true);
    try { await deletePasskey(selected.id); setShowRemove(false); setSelected(null); await load(); onMessage({ type: "success", content: t("user.passkey.removed"), dismissible: true }); }
    catch { onMessage({ type: "error", content: t("user.passkey.removeError"), dismissible: true }); }
    finally { setSaving(false); }
  };

  const date = (value: string | null) => value ? new Intl.DateTimeFormat(i18n.resolvedLanguage || i18n.language, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : t("user.passkey.never");

  return <>
    <Container header={<Box variant="h2">{t("user.passkey.title")}</Box>}>
      <SpaceBetween size="m">
        {!supported && <Alert type="warning">{t("user.passkey.unsupported")}</Alert>}
        {loading ? <StatusIndicator type="loading">{t("common.loading")}</StatusIndicator> : items.length === 0 ? <Box color="text-body-secondary">{t("user.passkey.empty")}</Box> : items.map((item) => <div key={item.id} style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem" }}>
          <div><strong>{item.name}</strong><div><small>{t("user.passkey.created", { date: date(item.created_at) })} · {t("user.passkey.lastUsed", { date: date(item.last_used_at) })}</small></div></div>
          <Button onClick={() => { setSelected(item); setShowRemove(true); }}>{t("user.passkey.remove")}</Button>
        </div>)}
        <Button iconName="add-plus" disabled={!supported} onClick={() => setShowAdd(true)}>{t("user.passkey.add")}</Button>
      </SpaceBetween>
    </Container>
    <Modal visible={showAdd} onDismiss={closeAdd} header={t("user.passkey.addTitle")} footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs"><Button variant="link" onClick={closeAdd}>{t("common.cancel")}</Button><Button variant="primary" loading={saving} onClick={() => void add()}>{t("user.passkey.add")}</Button></SpaceBetween></Box>}>
      <SpaceBetween size="m">
        <FormField label={t("user.passkey.nameLabel")}><Input value={name} onChange={({ detail }) => setName(detail.value)} placeholder={t("user.passkey.namePlaceholder")} /></FormField>
        <FormField label={t("user.passkey.passwordLabel")} errorText={passwordError || (attempted && !password.trim() ? t("user.passkey.passwordRequired") : undefined)}><Input type="password" value={password} onChange={({ detail }) => { setPassword(detail.value); if (attempted) setPasswordError(""); }} autoComplete="current-password" /></FormField>
      </SpaceBetween>
    </Modal>
    <Modal visible={showRemove} onDismiss={() => setShowRemove(false)} header={t("user.passkey.removeTitle")} footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs"><Button variant="link" onClick={() => setShowRemove(false)}>{t("common.cancel")}</Button><Button variant="primary" onClick={() => void remove()} loading={saving}>{t("user.passkey.remove")}</Button></SpaceBetween></Box>}>{t("user.passkey.removeConfirm", { name: selected?.name || "" })}</Modal>
  </>;
}
