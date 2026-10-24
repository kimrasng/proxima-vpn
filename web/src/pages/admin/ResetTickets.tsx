import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert, Autosuggest, Box, Button, ContentLayout, DatePicker, Flashbar, FormField, Header, Input, Modal,
  RadioGroup, Select, SpaceBetween, Spinner, Table,
} from "@cloudscape-design/components";
import { ApiError } from "../../api/client";
import { grantResetTickets, listPlans, listResetTicketGrants, listUsers } from "../../api/admin";
import type { Plan, ResetTicketGrant, ResetTicketTarget, User } from "../../api/types";

type GrantForm = {
  name: string;
  target: ResetTicketTarget;
  userQuery: string;
  user: User | null;
  planId: string;
  expiresOn: string;
};

function defaultExpiry(): string {
  const date = new Date(Date.now() + 30 * 86400000);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

const emptyForm = (): GrantForm => ({ name: "", target: "user", userQuery: "", user: null, planId: "", expiresOn: defaultExpiry() });

export default function ResetTickets() {
  const { t, i18n } = useTranslation();
  const tRef = useRef(t);
  tRef.current = t;
  const [grants, setGrants] = useState<ResetTicketGrant[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<{ type: "success" | "error"; content: string } | null>(null);
  const [modal, setModal] = useState(false);
  const [confirmAll, setConfirmAll] = useState(false);
  const [form, setForm] = useState<GrantForm>(emptyForm);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [userOptions, setUserOptions] = useState<User[]>([]);
  const [userLoading, setUserLoading] = useState(false);
  const dateTime = (value: string) => new Date(value).toLocaleString(i18n.resolvedLanguage ?? i18n.language);
  const date = (value: string) => new Date(value).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language);

  const fetchData = useCallback(async () => {
    try {
      const [grantData, planData] = await Promise.all([listResetTicketGrants(), listPlans()]);
      setGrants(grantData);
      setPlans(planData);
    } catch {
      setFlash({ type: "error", content: tRef.current("admin.resetTickets.fetchError") });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void fetchData(); }, [fetchData]);

  useEffect(() => {
    if (!modal || form.target !== "user" || form.userQuery.trim().length < 2) {
      setUserOptions([]);
      return;
    }
    let active = true;
    setUserLoading(true);
    const timer = window.setTimeout(() => {
      listUsers({ search: form.userQuery.trim(), limit: 10 })
        .then((res) => {
          if (!active) return;
          setUserOptions(res.users);
          // Typing or pasting a full address picks that account without a click.
          const exact = res.users.find((u) => u.email.toLowerCase() === form.userQuery.trim().toLowerCase());
          if (exact) setForm((f) => (f.user ? f : { ...f, user: exact }));
        })
        .catch(() => { if (active) setUserOptions([]); })
        .finally(() => { if (active) setUserLoading(false); });
    }, 250);
    return () => { active = false; window.clearTimeout(timer); };
  }, [modal, form.target, form.userQuery]);

  // The picked day is inclusive: a ticket valid "until the 30th" lasts through
  // the end of that day in the admin's time zone.
  const expiresAt = useMemo(() => {
    const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(form.expiresOn);
    if (!match) return null;
    return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]), 23, 59, 59);
  }, [form.expiresOn]);

  const errors = {
    name: form.name.trim() === "" ? t("admin.resetTickets.errors.name") : form.name.trim().length > 80 ? t("admin.resetTickets.errors.nameLength") : "",
    user: form.target === "user" && !form.user ? t("admin.resetTickets.errors.user") : "",
    plan: form.target === "plan" && !form.planId ? t("admin.resetTickets.errors.plan") : "",
    expires: !expiresAt || expiresAt.getTime() <= Date.now() ? t("admin.resetTickets.errors.expires") : "",
  };
  const invalid = Object.values(errors).some(Boolean);

  const submit = async () => {
    setShowErrors(true);
    if (invalid || !expiresAt) return;
    if (form.target === "all" && !confirmAll) {
      setConfirmAll(true);
      return;
    }
    setSaving(true);
    try {
      const grant = await grantResetTickets({
        name: form.name.trim(),
        target_type: form.target,
        user_id: form.target === "user" ? form.user?.id : undefined,
        plan_id: form.target === "plan" ? form.planId : undefined,
        expires_at: expiresAt.toISOString(),
      });
      setModal(false);
      setConfirmAll(false);
      setFlash({ type: "success", content: t("admin.resetTickets.granted", { name: grant.name, count: grant.granted_count }) });
      await fetchData();
    } catch (err) {
      const code = err instanceof ApiError ? (err.body as { code?: string } | undefined)?.code : undefined;
      setConfirmAll(false);
      setFlash({ type: "error", content: code === "no_recipients" ? t("admin.resetTickets.noRecipients") : t("admin.resetTickets.grantError") });
    } finally {
      setSaving(false);
    }
  };

  const targetLabel = (g: ResetTicketGrant) => g.target_type === "all" ? t("admin.resetTickets.target.all")
    : g.target_type === "plan" ? t("admin.resetTickets.targetPlan", { plan: g.target_plan_name ?? "-" })
    : g.target_email ?? "-";
  const activePlans = plans.filter((plan) => plan.is_active);

  if (loading) {
    return <ContentLayout header={<Header variant="h1">{t("admin.resetTickets.title")}</Header>}>
      <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
    </ContentLayout>;
  }

  return (
    <ContentLayout header={<Header variant="h1" description={t("admin.resetTickets.subtitle")}>{t("admin.resetTickets.title")}</Header>}>
      <SpaceBetween size="l">
        {flash && <Flashbar items={[{ type: flash.type, content: flash.content, dismissible: true, onDismiss: () => setFlash(null) }]} />}
        <Table
          items={grants}
          trackBy="id"
          wrapLines
          header={<Header counter={`(${grants.length})`} actions={
            <Button variant="primary" onClick={() => { setForm(emptyForm()); setShowErrors(false); setConfirmAll(false); setModal(true); }}>
              {t("admin.resetTickets.grant")}
            </Button>
          }>{t("admin.resetTickets.history")}</Header>}
          columnDefinitions={[
            { id: "name", header: t("admin.resetTickets.col.name"), cell: (g) => g.name },
            { id: "target", header: t("admin.resetTickets.col.target"), cell: targetLabel },
            { id: "count", header: t("admin.resetTickets.col.usage"), cell: (g) => t("admin.resetTickets.usage", { used: g.used_count, total: g.granted_count }) },
            { id: "expires", header: t("admin.resetTickets.col.expires"), cell: (g) => date(g.expires_at) },
            { id: "created", header: t("admin.resetTickets.col.created"), cell: (g) => dateTime(g.created_at) },
            { id: "by", header: t("admin.resetTickets.col.by"), cell: (g) => g.granted_by || "-" },
          ]}
          empty={<Box textAlign="center" color="text-body-secondary">{t("admin.resetTickets.empty")}</Box>}
        />
      </SpaceBetween>

      <Modal visible={modal} size="medium" onDismiss={() => !saving && setModal(false)} header={t("admin.resetTickets.grantTitle")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
          <Button variant="link" disabled={saving} onClick={() => { setModal(false); setConfirmAll(false); }}>{t("admin.resetTickets.cancel")}</Button>
          <Button variant="primary" loading={saving} onClick={() => void submit()}>
            {confirmAll ? t("admin.resetTickets.confirmAll") : t("admin.resetTickets.submit")}
          </Button>
        </SpaceBetween></Box>}>
        <SpaceBetween size="m">
          <FormField label={t("admin.resetTickets.field.name")} description={t("admin.resetTickets.field.nameHint")}
            errorText={showErrors ? errors.name : undefined}>
            <Input value={form.name} placeholder={t("admin.resetTickets.field.namePlaceholder")}
              onChange={({ detail }) => setForm((f) => ({ ...f, name: detail.value }))} />
          </FormField>
          <FormField label={t("admin.resetTickets.field.target")}>
            <RadioGroup value={form.target} onChange={({ detail }) => { setConfirmAll(false); setForm((f) => ({ ...f, target: detail.value as ResetTicketTarget })); }}
              items={[
                { value: "user", label: t("admin.resetTickets.target.user") },
                { value: "plan", label: t("admin.resetTickets.target.plan"), description: t("admin.resetTickets.target.planHint") },
                { value: "all", label: t("admin.resetTickets.target.all"), description: t("admin.resetTickets.target.allHint") },
              ]} />
          </FormField>
          {form.target === "user" && <FormField label={t("admin.resetTickets.field.user")} errorText={showErrors ? errors.user : undefined}
            description={form.user ? t("admin.resetTickets.field.userPicked", { email: form.user.email }) : t("admin.resetTickets.field.userHint")}>
            <Autosuggest value={form.userQuery} placeholder={t("admin.resetTickets.field.userPlaceholder")}
              statusType={userLoading ? "loading" : "finished"} loadingText={t("admin.resetTickets.field.userLoading")}
              empty={form.userQuery.trim().length >= 2 ? t("admin.resetTickets.field.userEmpty") : undefined}
              enteredTextLabel={(value) => value}
              hideEnteredTextOption
              filteringType="manual"
              options={userOptions.map((u) => ({ value: u.id, label: u.email, description: [u.name, u.plan_name].filter(Boolean).join(" · ") }))}
              onChange={({ detail }) => setForm((f) => ({ ...f, userQuery: detail.value, user: f.user?.email === detail.value ? f.user : null }))}
              onSelect={({ detail }) => {
                const picked = userOptions.find((u) => u.id === detail.value) ?? null;
                setForm((f) => ({ ...f, user: picked, userQuery: picked?.email ?? f.userQuery }));
              }} />
          </FormField>}
          {form.target === "plan" && <FormField label={t("admin.resetTickets.field.plan")} errorText={showErrors ? errors.plan : undefined}>
            <Select selectedOption={activePlans.filter((p) => p.id === form.planId).map((p) => ({ value: p.id, label: p.name }))[0] ?? null}
              placeholder={t("admin.resetTickets.field.planPlaceholder")}
              options={activePlans.map((p) => ({ value: p.id, label: p.name }))}
              onChange={({ detail }) => setForm((f) => ({ ...f, planId: detail.selectedOption.value ?? "" }))} />
          </FormField>}
          <FormField label={t("admin.resetTickets.field.expires")} description={t("admin.resetTickets.field.expiresHint")}
            errorText={showErrors ? errors.expires : undefined}>
            <DatePicker value={form.expiresOn} placeholder="YYYY/MM/DD" openCalendarAriaLabel={(selected) => selected ?? t("admin.resetTickets.field.expires")}
              isDateEnabled={(day) => day.getTime() >= new Date().setHours(0, 0, 0, 0)}
              onChange={({ detail }) => setForm((f) => ({ ...f, expiresOn: detail.value }))} />
          </FormField>
          {confirmAll && <Alert type="warning">{t("admin.resetTickets.allWarning")}</Alert>}
        </SpaceBetween>
      </Modal>
    </ContentLayout>
  );
}
