import { useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  ColumnLayout,
  Container,
  ContentLayout,
  FormField,
  Header,
  Modal,
  Select,
  SpaceBetween,
  Spinner,
  Textarea,
} from "@cloudscape-design/components";
import * as adminApi from "../../api/admin";
import { ApiError } from "../../api/client";
import type { AdminDirectoryEntry, AdminSupportTicket, SupportMessage, SupportPriority, SupportStatus as Status } from "../../api/types";
import { usePublishBreadcrumbLeaf } from "../../hooks/useBreadcrumbLeaf";
import { pickLocalized } from "../../utils/localizedText";
import SupportThread from "../../components/support/SupportThread";
import SupportStatus from "../../components/support/SupportStatus";

const BODY_MAX = 5000;

export default function SupportTicketDetail() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const { ticketId = "" } = useParams();
  const language = i18n.resolvedLanguage || i18n.language;
  const [ticket, setTicket] = useState<AdminSupportTicket | null>(null);
  const [messages, setMessages] = useState<SupportMessage[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [body, setBody] = useState("");
  const [notifyUser, setNotifyUser] = useState(false);
  const [directory, setDirectory] = useState<{ admins: AdminDirectoryEntry[]; me: string }>({ admins: [], me: "" });

  useEffect(() => {
    adminApi.listAdminDirectory().then(setDirectory).catch(() => setDirectory({ admins: [], me: "" }));
  }, []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  usePublishBreadcrumbLeaf(ticket?.subject);

  const load = useCallback(async () => {
    try {
      const data = await adminApi.getSupportTicket(ticketId);
      setTicket(data.ticket);
      setMessages(data.messages);
      setFailed(false);
    } catch {
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }, [ticketId]);

  useEffect(() => { void load(); }, [load]);

  const describe = (err: unknown, fallback: string) =>
    (err instanceof ApiError ? (err.body as { error?: string } | undefined)?.error : undefined) || fallback;

  const run = async (action: () => Promise<unknown>, fallback: string) => {
    setBusy(true);
    setError(null);
    try {
      await action();
      await load();
    } catch (err) {
      setError(describe(err, fallback));
    } finally {
      setBusy(false);
    }
  };

  const reply = async () => {
    if (!body.trim()) return;
    await run(async () => { await adminApi.replySupportTicket(ticketId, body.trim(), notifyUser); setBody(""); }, t("support.admin.replyError"));
  };

  const remove = async () => {
    setBusy(true);
    try {
      await adminApi.deleteSupportTicket(ticketId);
      navigate("/admin/support");
    } catch (err) {
      setError(describe(err, t("support.admin.deleteError")));
      setConfirmDelete(false);
      setBusy(false);
    }
  };

  if (loading) return <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>;
  if (failed || !ticket) {
    return (
      <ContentLayout header={<Header variant="h1" actions={<Button onClick={() => navigate("/admin/support")}>{t("support.thread.backToList")}</Button>}>{t("support.thread.notFound")}</Header>}>
        <Alert type="error">{t("support.thread.loadError")}</Alert>
      </ContentLayout>
    );
  }

  const closed = ticket.status === "closed";
  const statusOptions: { value: Status; label: string }[] = [
    { value: "open", label: t("support.status.open") },
    { value: "answered", label: t("support.status.answered") },
    { value: "closed", label: t("support.status.closed") },
  ];
  const assigneeName = (a: AdminDirectoryEntry) => a.name.trim() || a.email;
  const assigneeOptions = [
    { value: "", label: t("support.admin.unassigned") },
    ...directory.admins.map((a) => ({
      value: a.id,
      label: a.id === directory.me ? `${assigneeName(a)} (${t("support.admin.me")})` : assigneeName(a),
      description: a.name.trim() ? a.email : undefined,
    })),
  ];
  if (ticket.assigned_admin_id && !directory.admins.some((a) => a.id === ticket.assigned_admin_id)) {
    assigneeOptions.push({ value: ticket.assigned_admin_id, label: ticket.assigned_admin_name?.trim() || ticket.assigned_admin_email || "-", description: undefined });
  }
  const assignTo = (id: string) => void run(() => adminApi.updateSupportTicket(ticketId, { assigned_admin_id: id }), t("support.admin.assignError"));
  const priorityOptions: { value: SupportPriority; label: string }[] = [
    { value: "low", label: t("support.priority.low") },
    { value: "normal", label: t("support.priority.normal") },
    { value: "high", label: t("support.priority.high") },
  ];

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={pickLocalized(ticket.category_names, language)}
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button onClick={() => navigate("/admin/support")}>{t("support.thread.backToList")}</Button>
              <Button disabled={busy} onClick={() => setConfirmDelete(true)}>{t("support.admin.delete")}</Button>
            </SpaceBetween>
          }
        >
          <span className="support-ticket-title">{ticket.subject} <SupportStatus status={ticket.status} pill /></span>
        </Header>
      }
    >
      <SpaceBetween size="l">
        {error && <Alert type="error" dismissible onDismiss={() => setError(null)}>{error}</Alert>}
        <Container header={<Header variant="h3">{t("support.admin.details")}</Header>}>
          <ColumnLayout columns={4} variant="text-grid" minColumnWidth={170}>
            <div>
              <Box variant="awsui-key-label">{t("support.admin.user")}</Box>
              <Box>{ticket.user_name}</Box>
              <Box color="text-body-secondary">{ticket.user_email}</Box>
            </div>
            <FormField
              label={t("support.admin.assigneeCol")}
              description={
                directory.me && ticket.assigned_admin_id !== directory.me ? (
                  <Button variant="inline-link" disabled={busy} onClick={() => assignTo(directory.me)}>{t("support.admin.assignMe")}</Button>
                ) : undefined
              }
            >
              <Select
                selectedOption={assigneeOptions.find((option) => option.value === (ticket.assigned_admin_id ?? "")) ?? null}
                options={assigneeOptions}
                filteringType={assigneeOptions.length > 6 ? "auto" : "none"}
                disabled={busy}
                ariaLabel={t("support.admin.assigneeCol")}
                onChange={({ detail }) => assignTo(detail.selectedOption.value ?? "")}
              />
            </FormField>
            <FormField label={t("support.list.status")}>
              <Select
                selectedOption={statusOptions.find((option) => option.value === ticket.status) ?? null}
                options={statusOptions}
                disabled={busy}
                onChange={({ detail }) => void run(() => adminApi.updateSupportTicket(ticketId, { status: detail.selectedOption.value as Status }), t("support.admin.updateError"))}
              />
            </FormField>
            <FormField label={t("support.admin.priority")}>
              <Select
                selectedOption={priorityOptions.find((option) => option.value === ticket.priority) ?? null}
                options={priorityOptions}
                disabled={busy}
                onChange={({ detail }) => void run(() => adminApi.updateSupportTicket(ticketId, { priority: detail.selectedOption.value as SupportPriority }), t("support.admin.updateError"))}
              />
            </FormField>
          </ColumnLayout>
        </Container>
        <Container>
          <SupportThread messages={messages} viewer="admin" />
        </Container>
        {closed ? (
          <Alert type="info">{t("support.admin.closedNotice")}</Alert>
        ) : (
          <Container header={<Header variant="h3">{t("support.thread.reply")}</Header>}>
            <SpaceBetween size="s">
              <FormField stretch constraintText={t("support.create.bodyLimit", { count: body.length, max: BODY_MAX })}>
                <Textarea
                  value={body}
                  rows={5}
                  onChange={({ detail }) => setBody(detail.value.slice(0, BODY_MAX))}
                  placeholder={t("support.admin.replyPlaceholder")}
                  ariaLabel={t("support.thread.reply")}
                />
              </FormField>
              <Checkbox
                checked={notifyUser}
                onChange={({ detail }) => setNotifyUser(detail.checked)}
                description={t("support.admin.notifyUserHint", { email: ticket.user_email })}
              >
                {t("support.admin.notifyUser")}
              </Checkbox>
              <Box float="right">
                <Button variant="primary" loading={busy} disabled={!body.trim()} onClick={() => void reply()}>{t("support.admin.sendReply")}</Button>
              </Box>
            </SpaceBetween>
          </Container>
        )}
      </SpaceBetween>
      <Modal
        visible={confirmDelete}
        onDismiss={() => setConfirmDelete(false)}
        header={t("support.admin.deleteTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setConfirmDelete(false)}>{t("support.admin.cancel")}</Button>
              <Button variant="primary" loading={busy} onClick={() => void remove()}>{t("support.admin.delete")}</Button>
            </SpaceBetween>
          </Box>
        }
      >
        {t("support.admin.deleteBody")}
      </Modal>
    </ContentLayout>
  );
}
