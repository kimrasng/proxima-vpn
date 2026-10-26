import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Badge,
  Box,
  Button,
  ContentLayout,
  Header,
  Link,
  SpaceBetween,
  Table,
} from "@cloudscape-design/components";
import type { SupportTicket } from "../../api/types";
import * as userApi from "../../api/user";
import { useUserResource } from "../../hooks/useUserResource";
import { formatAbsoluteTime, formatRelativeTime } from "../../utils/relativeTime";
import { pickLocalized } from "../../utils/localizedText";
import CreateTicketModal from "../../components/support/CreateTicketModal";
import SupportStatus from "../../components/support/SupportStatus";
import PortalEmpty from "../../components/PortalEmpty";

export default function Tickets() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const language = i18n.resolvedLanguage || i18n.language;
  const [creating, setCreating] = useState(false);
  const loadTickets = useCallback(() => userApi.listSupportTickets(), []);
  const loadHelp = useCallback(() => userApi.getSupportHelp(), []);
  const tickets = useUserResource(loadTickets);
  const help = useUserResource(loadHelp);

  const open = () => setCreating(true);

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={t("support.list.description")}
          actions={<Button variant="primary" iconName="add-plus" onClick={open}>{t("support.list.create")}</Button>}
        >
          {t("support.list.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        <Table<SupportTicket>
          variant="container"
          loading={tickets.loading}
          loadingText={t("support.list.loading")}
          items={tickets.data ?? []}
          trackBy="id"
          header={<Header counter={tickets.data ? `(${tickets.data.length})` : undefined}>{t("support.list.tableTitle")}</Header>}
          empty={
            tickets.error
              ? <Box textAlign="center" color="text-status-error">{t("support.list.loadError")}</Box>
              : <PortalEmpty title={t("support.list.empty")} description={t("support.list.emptyHint")} />
          }
          columnDefinitions={[
            {
              id: "subject",
              header: t("support.list.subject"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs" alignItems="center">
                  <Link href={`/portal/tickets/${item.id}`} onFollow={(event) => { event.preventDefault(); navigate(`/portal/tickets/${item.id}`); }}>
                    {item.subject}
                  </Link>
                  {item.unread && <Badge color="red">{t("support.list.newReply")}</Badge>}
                </SpaceBetween>
              ),
              isRowHeader: true,
            },
            { id: "category", header: t("support.list.category"), cell: (item) => pickLocalized(item.category_names, language) || "-" },
            { id: "status", header: t("support.list.status"), cell: (item) => <SupportStatus status={item.status} /> },
            { id: "created", header: t("support.list.created"), cell: (item) => formatAbsoluteTime(item.created_at) },
            { id: "reply", header: t("support.list.lastReply"), cell: (item) => (item.last_reply_at ? formatRelativeTime(t, item.last_reply_at) : "-") },
          ]}
        />
      </SpaceBetween>
      <CreateTicketModal
        visible={creating}
        help={help.data}
        onDismiss={() => setCreating(false)}
        onCreated={(id) => { setCreating(false); navigate(`/portal/tickets/${id}`); }}
      />
    </ContentLayout>
  );
}
