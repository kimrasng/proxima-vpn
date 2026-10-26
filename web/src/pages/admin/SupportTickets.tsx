import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Badge,
  Box,
  Button,
  ColumnLayout,
  ContentLayout,
  Flashbar,
  Header,
  Input,
  Link,
  Pagination,
  SegmentedControl,
  SpaceBetween,
  Table,
} from "@cloudscape-design/components";
import * as adminApi from "../../api/admin";
import type { AdminSupportSummary, AdminSupportTicket, SupportStatus as Status } from "../../api/types";
import { formatRelativeTime } from "../../utils/relativeTime";
import { pickLocalized } from "../../utils/localizedText";
import SupportStatus from "../../components/support/SupportStatus";
import "./supportAssignee.css";

const PAGE_SIZE = 20;
type Filter = Status | "";

export default function SupportTickets() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const language = i18n.resolvedLanguage || i18n.language;
  const [filter, setFilter] = useState<Filter>("open");
  const [assignee, setAssignee] = useState<adminApi.SupportAssigneeFilter>("all");
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [tickets, setTickets] = useState<AdminSupportTicket[]>([]);
  const [total, setTotal] = useState(0);
  const [summary, setSummary] = useState<AdminSupportSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const requestId = useRef(0);

  const load = useCallback(async () => {
    const id = ++requestId.current;
    setLoading(true);
    try {
      const [list, counts] = await Promise.all([
        adminApi.listSupportTickets({ status: filter, assignee, q: query, page, limit: PAGE_SIZE }),
        adminApi.getSupportSummary(),
      ]);
      if (id !== requestId.current) return;
      setTickets(list.tickets);
      setTotal(list.total);
      setSummary(counts);
      setFailed(false);
    } catch {
      if (id === requestId.current) setFailed(true);
    } finally {
      if (id === requestId.current) setLoading(false);
    }
  }, [filter, assignee, query, page]);

  useEffect(() => { void load(); }, [load]);

  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const openTicket = (id: string) => navigate(`/admin/support/${id}`);

  return (
    <ContentLayout
      header={
        <Header variant="h1" description={t("support.admin.description")} actions={<Button iconName="refresh" onClick={() => void load()}>{t("support.admin.refresh")}</Button>}>
          {t("support.admin.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {failed && <Flashbar items={[{ type: "error", content: t("support.admin.loadError"), dismissible: false }]} />}
        {summary && (
          <ColumnLayout columns={4} variant="text-grid" minColumnWidth={140}>
            <div><Box variant="awsui-key-label">{t("support.status.open")}</Box><Box variant="awsui-value-large">{summary.open}</Box></div>
            <div><Box variant="awsui-key-label">{t("support.status.answered")}</Box><Box variant="awsui-value-large">{summary.answered}</Box></div>
            <div><Box variant="awsui-key-label">{t("support.status.closed")}</Box><Box variant="awsui-value-large">{summary.closed}</Box></div>
            <div><Box variant="awsui-key-label">{t("support.admin.unread")}</Box><Box variant="awsui-value-large">{summary.unread}</Box></div>
          </ColumnLayout>
        )}
        <div className="support-assignee-bar" role="group" aria-label={t("support.admin.assigneeFilter")}>
          {(["all", "mine", "unassigned"] as const).map((id) => (
            <button
              key={id}
              type="button"
              aria-pressed={assignee === id}
              className={assignee === id ? "support-assignee-chip support-assignee-chip--on" : "support-assignee-chip"}
              onClick={() => { setPage(1); setAssignee(id); }}
            >
              <span>{t(`support.admin.assignee.${id}`)}</span>
              {summary && id !== "all" && <strong>{id === "mine" ? summary.mine : summary.unassigned}</strong>}
            </button>
          ))}
        </div>
        <Table<AdminSupportTicket>
          variant="container"
          loading={loading}
          loadingText={t("support.list.loading")}
          items={tickets}
          trackBy="id"
          header={
            <Header
              counter={`(${total})`}
              actions={
                <SpaceBetween direction="horizontal" size="s">
                  <SegmentedControl
                    selectedId={filter || "all"}
                    onChange={({ detail }) => { setPage(1); setFilter(detail.selectedId === "all" ? "" : (detail.selectedId as Status)); }}
                    label={t("support.admin.filter")}
                    options={[
                      { id: "open", text: t("support.status.open") },
                      { id: "answered", text: t("support.status.answered") },
                      { id: "closed", text: t("support.status.closed") },
                      { id: "all", text: t("support.admin.all") },
                    ]}
                  />
                </SpaceBetween>
              }
            >
              {t("support.admin.tableTitle")}
            </Header>
          }
          filter={
            <Input
              type="search"
              value={search}
              onChange={({ detail }) => setSearch(detail.value)}
              onKeyDown={({ detail }) => { if (detail.key === "Enter") { setPage(1); setQuery(search); } }}
              placeholder={t("support.admin.searchPlaceholder")}
              ariaLabel={t("support.admin.searchPlaceholder")}
            />
          }
          pagination={<Pagination currentPageIndex={page} pagesCount={pages} onChange={({ detail }) => setPage(detail.currentPageIndex)} />}
          empty={<Box textAlign="center" color="text-body-secondary">{t("support.admin.empty")}</Box>}
          columnDefinitions={[
            {
              id: "subject",
              header: t("support.list.subject"),
              isRowHeader: true,
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs" alignItems="center">
                  <Link href={`/admin/support/${item.id}`} onFollow={(event) => { event.preventDefault(); openTicket(item.id); }}>{item.subject}</Link>
                  {item.unread && <Badge color="red">{t("support.admin.new")}</Badge>}
                </SpaceBetween>
              ),
            },
            { id: "user", header: t("support.admin.user"), cell: (item) => item.user_email },
            { id: "category", header: t("support.list.category"), cell: (item) => pickLocalized(item.category_names, language) || "-" },
            { id: "status", header: t("support.list.status"), cell: (item) => <SupportStatus status={item.status} /> },
            {
              id: "assignee",
              header: t("support.admin.assigneeCol"),
              cell: (item) => item.assigned_admin_id
                ? (item.assigned_admin_name?.trim() || item.assigned_admin_email)
                : <Box color="text-status-inactive">{t("support.admin.unassigned")}</Box>,
            },
            { id: "updated", header: t("support.admin.updated"), cell: (item) => formatRelativeTime(t, item.updated_at) },
          ]}
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
