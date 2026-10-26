import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Box, Button, FormField, Header, Input, Modal, Select, SpaceBetween, StatusIndicator, Table, Toggle } from "@cloudscape-design/components";
import { listPlans } from "../../api/admin";
import { deleteFreeTrial, listFreeTrials, saveFreeTrial, type FreeTrialPromotion } from "../../api/promotionKinds";
import type { Plan } from "../../api/types";

const emptyForm = {
  code: "", plan_id: "", duration_days: "30", trial_days: "7", max_redemptions: "",
  max_redemptions_per_user: "1", requires_card: true, is_active: true,
};

export default function FreeTrialPromotions() {
  const { t } = useTranslation();
  const [items, setItems] = useState<FreeTrialPromotion[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editor, setEditor] = useState<{ id?: string } | null>(null);
  const [deleting, setDeleting] = useState<FreeTrialPromotion | null>(null);
  const [form, setForm] = useState(emptyForm);
  const [showErrors, setShowErrors] = useState(false);
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [trials, planList] = await Promise.all([listFreeTrials(), listPlans()]);
      setItems(trials);
      setPlans(planList);
      setError(false);
    } catch {
      setError(true);
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);
  const positive = (value: string) => /^\d+$/.test(value) && Number.isSafeInteger(Number(value)) && Number(value) > 0 && Number(value) <= 2147483647;
  const invalid = {
    code: !form.code.trim(),
    plan_id: !form.plan_id,
    duration_days: !positive(form.duration_days),
    trial_days: !positive(form.trial_days),
    max_redemptions: !!form.max_redemptions && !positive(form.max_redemptions),
    max_redemptions_per_user: !positive(form.max_redemptions_per_user),
  };
  const save = async () => {
    setShowErrors(true);
    if (Object.values(invalid).some(Boolean) || !editor) return;
    setSaving(true);
    try {
      await saveFreeTrial({
        code: form.code.trim(), plan_id: form.plan_id, duration_days: Number(form.duration_days),
        trial_days: Number(form.trial_days), max_redemptions: form.max_redemptions ? Number(form.max_redemptions) : null,
        max_redemptions_per_user: Number(form.max_redemptions_per_user), requires_card: form.requires_card, is_active: form.is_active,
      }, editor.id);
      setEditor(null);
      await load();
    } catch { setError(true); } finally { setSaving(false); }
  };
  const remove = async () => {
    if (!deleting) return;
    setSaving(true);
    try { await deleteFreeTrial(deleting.id); setDeleting(null); await load(); }
    catch { setError(true); } finally { setSaving(false); }
  };
  const fieldError = (invalid: boolean) => showErrors && invalid ? t("admin.freeTrial.validation") : undefined;
  return <SpaceBetween size="l">
    {error && <Alert type="error" action={<Button onClick={() => void load()}>{t("admin.freeTrial.retry")}</Button>}>{t("admin.freeTrial.error")}</Alert>}
    <Table items={items} trackBy="id" loading={loading} loadingText={t("admin.freeTrial.loading")}
      header={<Header counter={`(${items.length})`} actions={<Button variant="primary" onClick={() => {
        setForm(emptyForm); setShowErrors(false); setError(false); setEditor({});
      }}>{t("admin.freeTrial.create")}</Button>}>{t("admin.freeTrial.title")}</Header>}
      empty={<Box textAlign="center">{t("admin.freeTrial.empty")}</Box>}
      columnDefinitions={[
        { id: "code", header: t("admin.freeTrial.code"), cell: (p) => p.code },
        { id: "plan", header: t("admin.freeTrial.plan"), cell: (p) => plans.find((plan) => plan.id === p.plan_id)?.name ?? "-" },
        { id: "duration", header: t("admin.freeTrial.durationDays"), cell: (p) => p.duration_days },
        { id: "trial", header: t("admin.freeTrial.trialDays"), cell: (p) => p.trial_days },
        { id: "card", header: t("admin.freeTrial.requiresCard"), cell: (p) => p.requires_card ? t("admin.freeTrial.yes") : t("admin.freeTrial.no") },
        { id: "cap", header: t("admin.freeTrial.maxRedemptions"), cell: (p) => p.max_redemptions ?? t("admin.freeTrial.unlimited") },
        { id: "active", header: t("admin.freeTrial.active"), cell: (p) => <StatusIndicator type={p.is_active ? "success" : "stopped"}>{p.is_active ? t("admin.freeTrial.yes") : t("admin.freeTrial.no")}</StatusIndicator> },
        { id: "actions", header: t("admin.freeTrial.actions"), cell: (p) => <SpaceBetween direction="horizontal" size="xs">
          <Button variant="inline-link" onClick={() => {
            setForm({ ...p, plan_id: p.plan_id ?? "", duration_days: String(p.duration_days), trial_days: String(p.trial_days),
              max_redemptions: p.max_redemptions === null ? "" : String(p.max_redemptions), max_redemptions_per_user: String(p.max_redemptions_per_user) });
            setShowErrors(false); setError(false); setEditor({ id: p.id });
          }}>{t("admin.freeTrial.edit")}</Button>
          <Button variant="inline-link" onClick={() => setDeleting(p)}>{t("admin.freeTrial.delete")}</Button>
        </SpaceBetween> },
      ]} />
    <Modal visible={editor !== null} onDismiss={() => { if (!saving) setEditor(null); }}
      header={editor?.id ? t("admin.freeTrial.edit") : t("admin.freeTrial.create")}
      footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
        <Button disabled={saving} onClick={() => setEditor(null)}>{t("admin.freeTrial.cancel")}</Button>
        <Button variant="primary" loading={saving} onClick={() => void save()}>{t("admin.freeTrial.save")}</Button>
      </SpaceBetween></Box>}>
      <SpaceBetween size="m">
        {error && <Alert type="error">{t("admin.freeTrial.error")}</Alert>}
        <FormField label={t("admin.freeTrial.code")} errorText={fieldError(invalid.code)}><Input value={form.code} onChange={({ detail }) => setForm({ ...form, code: detail.value })} /></FormField>
        <FormField label={t("admin.freeTrial.plan")} errorText={fieldError(invalid.plan_id)}>
          <Select selectedOption={plans.find((p) => p.id === form.plan_id) ? { value: form.plan_id, label: plans.find((p) => p.id === form.plan_id)?.name } : null}
            placeholder={t("admin.freeTrial.selectPlan")} options={plans.map((p) => ({ value: p.id, label: p.name }))}
            onChange={({ detail }) => setForm({ ...form, plan_id: detail.selectedOption.value ?? "" })} />
        </FormField>
        <FormField label={t("admin.freeTrial.durationDays")} errorText={fieldError(invalid.duration_days)}><Input type="number" value={form.duration_days} onChange={({ detail }) => setForm({ ...form, duration_days: detail.value })} /></FormField>
        <FormField label={t("admin.freeTrial.trialDays")} errorText={fieldError(invalid.trial_days)}><Input type="number" value={form.trial_days} onChange={({ detail }) => setForm({ ...form, trial_days: detail.value })} /></FormField>
        <FormField label={t("admin.freeTrial.maxRedemptions")} description={t("admin.freeTrial.unlimitedHint")} errorText={fieldError(invalid.max_redemptions)}><Input type="number" value={form.max_redemptions} onChange={({ detail }) => setForm({ ...form, max_redemptions: detail.value })} /></FormField>
        <FormField label={t("admin.freeTrial.perUser")} errorText={fieldError(invalid.max_redemptions_per_user)}><Input type="number" value={form.max_redemptions_per_user} onChange={({ detail }) => setForm({ ...form, max_redemptions_per_user: detail.value })} /></FormField>
        <Toggle checked={form.requires_card} onChange={({ detail }) => setForm({ ...form, requires_card: detail.checked })}>{t("admin.freeTrial.requiresCard")}</Toggle>
        <Toggle checked={form.is_active} onChange={({ detail }) => setForm({ ...form, is_active: detail.checked })}>{t("admin.freeTrial.active")}</Toggle>
      </SpaceBetween>
    </Modal>
    <Modal visible={deleting !== null} onDismiss={() => { if (!saving) setDeleting(null); }} header={t("admin.freeTrial.delete")}
      footer={<SpaceBetween direction="horizontal" size="xs"><Button disabled={saving} onClick={() => setDeleting(null)}>{t("admin.freeTrial.cancel")}</Button><Button variant="primary" loading={saving} onClick={() => void remove()}>{t("admin.freeTrial.delete")}</Button></SpaceBetween>}>
      <SpaceBetween size="m">{error && <Alert type="error">{t("admin.freeTrial.error")}</Alert>}<Box>{t("admin.freeTrial.deleteConfirm", { code: deleting?.code })}</Box></SpaceBetween>
    </Modal>
  </SpaceBetween>;
}
