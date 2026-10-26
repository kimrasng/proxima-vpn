import { useCallback, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  Container,
  ContentLayout,
  FormField,
  Header,
  SpaceBetween,
  Spinner,
  Textarea,
} from "@cloudscape-design/components";
import * as userApi from "../../api/user";
import { ApiError } from "../../api/client";
import { useUserResource } from "../../hooks/useUserResource";
import { pickLocalized } from "../../utils/localizedText";
import { formatAbsoluteTime } from "../../utils/relativeTime";
import SupportThread from "../../components/support/SupportThread";
import SupportStatus from "../../components/support/SupportStatus";

const BODY_MAX = 5000;

export default function TicketDetail() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const { ticketId = "" } = useParams();
  const language = i18n.resolvedLanguage || i18n.language;
  const load = useCallback(() => userApi.getSupportTicket(ticketId), [ticketId]);
  const thread = useUserResource(load);
  const [body, setBody] = useState("");
  const [sending, setSending] = useState(false);
  const [closing, setClosing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const back = () => navigate("/portal/tickets");
  const errorMessage = (err: unknown, fallback: string) =>
    (err instanceof ApiError ? (err.body as { error?: string } | undefined)?.error : undefined) || fallback;

  const send = async () => {
    if (!body.trim()) return;
    setSending(true);
    setError(null);
    try {
      await userApi.postSupportMessage(ticketId, body.trim());
      setBody("");
      thread.refresh();
    } catch (err) {
      setError(errorMessage(err, t("support.thread.sendError")));
    } finally {
      setSending(false);
    }
  };

  const close = async () => {
    setClosing(true);
    setError(null);
    try {
      await userApi.closeSupportTicket(ticketId);
      thread.refresh();
    } catch (err) {
      setError(errorMessage(err, t("support.thread.closeError")));
    } finally {
      setClosing(false);
    }
  };

  if (thread.loading && !thread.data) {
    return <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>;
  }
  if (!thread.data) {
    return (
      <ContentLayout header={<Header variant="h1" actions={<Button onClick={back}>{t("support.thread.backToList")}</Button>}>{t("support.thread.notFound")}</Header>}>
        <Alert type="error">{t("support.thread.loadError")}</Alert>
      </ContentLayout>
    );
  }

  const { ticket, messages } = thread.data;
  const closed = ticket.status === "closed";

  return (
    <ContentLayout
      header={
        <SpaceBetween size="xxs">
          <Button variant="link" iconName="angle-left" onClick={back}>{t("support.thread.backToList")}</Button>
          <Header
            variant="h1"
            description={
              <span className="support-ticket-meta">
                <span>{pickLocalized(ticket.category_names, language)}</span>
                <span aria-hidden="true">·</span>
                <span>{t("support.thread.createdAt", { time: formatAbsoluteTime(ticket.created_at) })}</span>
              </span>
            }
            actions={!closed && <Button iconName="close" loading={closing} onClick={() => void close()}>{t("support.thread.close")}</Button>}
          >
            <span className="support-ticket-title">{ticket.subject} <SupportStatus status={ticket.status} pill /></span>
          </Header>
        </SpaceBetween>
      }
    >
      <SpaceBetween size="l">
        {error && <Alert type="error" dismissible onDismiss={() => setError(null)}>{error}</Alert>}
        <Container>
          <SupportThread messages={messages} viewer="user" />
        </Container>
        {closed ? (
          <Alert type="info">{t("support.thread.closedNotice")}</Alert>
        ) : (
          <Container header={<Header variant="h3">{t("support.thread.reply")}</Header>}>
            <SpaceBetween size="s">
              <FormField stretch constraintText={t("support.create.bodyLimit", { count: body.length, max: BODY_MAX })}>
                <Textarea
                  value={body}
                  rows={4}
                  onChange={({ detail }) => setBody(detail.value.slice(0, BODY_MAX))}
                  placeholder={t("support.thread.replyPlaceholder")}
                  ariaLabel={t("support.thread.reply")}
                />
              </FormField>
              <Box float="right">
                <Button variant="primary" loading={sending} disabled={!body.trim()} onClick={() => void send()}>{t("support.thread.send")}</Button>
              </Box>
            </SpaceBetween>
          </Container>
        )}
      </SpaceBetween>
    </ContentLayout>
  );
}
