import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useCollection } from "@cloudscape-design/collection-hooks";
import {
  Box,
  Button,
  ContentLayout,
  Flashbar,
  Header,
  Link,
  Modal,
  Pagination,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  TextFilter,
} from "@cloudscape-design/components";
import { adminListOrders, adminMarkOrderPaid, adminCancelOrder } from "../../api/admin";
import type { AdminOrderItem } from "../../api/types";
import { formatPriceCents } from "../../utils/planPricing";
import { formatAbsoluteTime } from "../../utils/relativeTime";
import { orderStatusIndicatorType } from "./orderStatus";

const PAGE_SIZE = 25;

function capturedOrDash(value: string | undefined) {
  return value ? value : <Box color="text-status-inactive">—</Box>;
}

export default function Orders() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [orders, setOrders] = useState<AdminOrderItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<string>("pending");
  const [actionLoading, setActionLoading] = useState<string | null>(null);
  const [payModal, setPayModal] = useState<AdminOrderItem | null>(null);
  const [cancelModal, setCancelModal] = useState<AdminOrderItem | null>(null);

  const fetchOrders = async () => {
    setLoading(true);
    try {
      const data = await adminListOrders(statusFilter || undefined);
      setOrders(data);
      setError(null);
    } catch {
      setError(t("admin.orders.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchOrders();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [statusFilter]);

  const statusOptions = useMemo(
    () => [
      { label: t("admin.orders.filter.all"), value: "" },
      { label: t("admin.orders.filter.pending"), value: "pending" },
      { label: t("admin.orders.filter.paid"), value: "paid" },
      { label: t("admin.orders.filter.cancelled"), value: "cancelled" },
      { label: t("admin.orders.filter.expired"), value: "expired" },
    ],
    [t],
  );

  const { items, collectionProps, filterProps, filteredItemsCount, paginationProps } = useCollection(
    orders,
    {
      filtering: {
        empty: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            {t("admin.orders.empty")}
          </Box>
        ),
        noMatch: (
          <Box textAlign="center" padding={{ vertical: "l" }} color="inherit">
            {t("admin.orders.noMatch")}
          </Box>
        ),
        filteringFunction: (item, filteringText) => {
          const text = filteringText.trim().toLowerCase();
          if (!text) return true;
          return [item.user_email, item.user_name, item.plan_name].some((field) =>
            (field ?? "").toLowerCase().includes(text),
          );
        },
      },
      sorting: {},
      pagination: { pageSize: PAGE_SIZE },
    },
  );

  const handleMarkPaid = async () => {
    if (!payModal) return;
    setActionLoading(payModal.id);
    try {
      await adminMarkOrderPaid(payModal.id);
      setNotice(t("admin.orders.paidSuccess", { email: payModal.user_email }));
      setPayModal(null);
      await fetchOrders();
    } catch {
      setError(t("admin.orders.paidError"));
    } finally {
      setActionLoading(null);
    }
  };

  const handleCancel = async () => {
    if (!cancelModal) return;
    setActionLoading(cancelModal.id);
    try {
      await adminCancelOrder(cancelModal.id);
      setNotice(t("admin.orders.cancelSuccess"));
      setCancelModal(null);
      await fetchOrders();
    } catch {
      setError(t("admin.orders.cancelError"));
    } finally {
      setActionLoading(null);
    }
  };

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={t("admin.orders.subtitle")}
          actions={
            <Button
              iconName="refresh"
              ariaLabel={t("admin.orders.refresh")}
              loading={loading}
              onClick={() => void fetchOrders()}
            />
          }
        >
          {t("admin.orders.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {error && (
          <Flashbar
            items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]}
          />
        )}
        {notice && (
          <Flashbar
            items={[
              { type: "success", content: notice, dismissible: true, onDismiss: () => setNotice(null) },
            ]}
          />
        )}

        {/* Eleven columns run wider than the content column at every viewport,
            so Cloudscape makes the wrapper a focusable scroll region. tableLabel
            gives that region its accessible name, and stickyColumns pins the
            user column so a row stays identifiable at the far-right scroll
            extreme. Hiding columns is not the alternative - they are the audit
            record. */}
        <Table
          {...collectionProps}
          ariaLabels={{ tableLabel: t("admin.orders.title") }}
          stickyColumns={{ first: 1 }}
          loading={loading}
          loadingText={t("admin.orders.loading")}
          items={items}
          trackBy="id"
          header={
            <Header
              counter={
                filteredItemsCount !== undefined && filteredItemsCount !== orders.length
                  ? `(${filteredItemsCount}/${orders.length})`
                  : `(${orders.length})`
              }
            >
              {t("admin.orders.title")}
            </Header>
          }
          filter={
            <SpaceBetween direction="horizontal" size="xs" alignItems="center">
              <TextFilter
                {...filterProps}
                filteringPlaceholder={t("admin.orders.searchPlaceholder")}
                filteringAriaLabel={t("admin.orders.searchPlaceholder")}
              />
              <Select
                selectedOption={statusOptions.find((o) => o.value === statusFilter) ?? statusOptions[0] ?? null}
                options={statusOptions}
                onChange={({ detail }) => setStatusFilter(detail.selectedOption.value ?? "")}
                ariaLabel={t("admin.orders.filter.all")}
              />
            </SpaceBetween>
          }
          pagination={<Pagination {...paginationProps} />}
          columnDefinitions={[
            {
              id: "user",
              header: t("admin.orders.col.user"),
              sortingField: "user_email",
              minWidth: 200,
              cell: (item) => (
                <SpaceBetween size="xxxs">
                  <Box>{item.user_email}</Box>
                  {item.user_name && (
                    <Box variant="small" color="text-body-secondary">
                      {item.user_name}
                    </Box>
                  )}
                </SpaceBetween>
              ),
            },
            {
              id: "plan",
              header: t("admin.orders.col.plan"),
              sortingField: "plan_name",
              cell: (item) => (
                <Link
                  href={`/admin/orders/${item.id}`}
                  onFollow={(event) => {
                    event.preventDefault();
                    void navigate(`/admin/orders/${item.id}`);
                  }}
                  ariaLabel={t("admin.orders.openAudit", { plan: item.plan_name })}
                >
                  {item.plan_name}
                </Link>
              ),
            },
            {
              id: "duration",
              header: t("admin.orders.col.duration"),
              sortingField: "duration_days",
              cell: (item) => t("admin.orders.durationValue", { days: item.duration_days }),
            },
            {
              id: "price",
              header: t("admin.orders.col.price"),
              sortingField: "price_cents",
              cell: (item) => formatPriceCents(item.price_cents),
            },
            {
              id: "status",
              header: t("admin.orders.col.status"),
              sortingField: "status",
              cell: (item) => (
                <StatusIndicator type={orderStatusIndicatorType(item.status)}>
                  {t(`admin.orders.status.${item.status}`)}
                </StatusIndicator>
              ),
            },
            {
              id: "provider",
              header: t("admin.orders.col.provider"),
              sortingField: "provider",
              cell: (item) => capturedOrDash(item.provider),
            },
            // Context summary. A dash here means "never captured" - absent for a
            // pre-capture order or one whose tracking columns retention purged.
            {
              id: "clientIp",
              header: t("admin.orders.col.clientIp"),
              sortingField: "client_ip",
              cell: (item) => capturedOrDash(item.client_ip),
            },
            {
              id: "browser",
              header: t("admin.orders.col.browser"),
              sortingField: "browser_family",
              cell: (item) => capturedOrDash(item.browser_family),
            },
            {
              id: "os",
              header: t("admin.orders.col.os"),
              sortingField: "os_family",
              cell: (item) => capturedOrDash(item.os_family),
            },
            {
              id: "createdAt",
              header: t("admin.orders.col.createdAt"),
              sortingField: "created_at",
              // Not a bare toLocaleString(): that follows the browser locale,
              // so a zh panel showed "9/21/2026, 7:08:44 PM" in this column
              // while the audit detail beside it read "2026/9/21 19:08:44".
              cell: (item) => formatAbsoluteTime(item.created_at),
            },
            {
              id: "actions",
              header: t("admin.orders.col.actions"),
              minWidth: 200,
              cell: (item) =>
                item.status === "pending" ? (
                  <SpaceBetween direction="horizontal" size="xs">
                    <Button
                      variant="primary"
                      loading={actionLoading === item.id}
                      onClick={() => setPayModal(item)}
                    >
                      {t("admin.orders.markPaid")}
                    </Button>
                    <Button
                      loading={actionLoading === item.id}
                      onClick={() => setCancelModal(item)}
                    >
                      {t("admin.orders.cancel")}
                    </Button>
                  </SpaceBetween>
                ) : (
                  <Box color="text-status-inactive">—</Box>
                ),
            },
          ]}
          empty={
            <Box textAlign="center" padding={{ vertical: "l" }}>
              {loading ? <Spinner /> : t("admin.orders.empty")}
            </Box>
          }
        />

        <Modal
          visible={payModal !== null}
          onDismiss={() => setPayModal(null)}
          header={t("admin.orders.payTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setPayModal(null)}>{t("admin.orders.dismiss")}</Button>
                <Button
                  variant="primary"
                  loading={actionLoading === payModal?.id}
                  onClick={() => void handleMarkPaid()}
                >
                  {t("admin.orders.confirmPaid")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.orders.payMessage", {
            email: payModal?.user_email,
            plan: payModal?.plan_name,
            days: payModal?.duration_days,
            price: payModal ? formatPriceCents(payModal.price_cents) : "",
          })}
        </Modal>

        <Modal
          visible={cancelModal !== null}
          onDismiss={() => setCancelModal(null)}
          header={t("admin.orders.cancelTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setCancelModal(null)}>{t("admin.orders.dismiss")}</Button>
                <Button
                  variant="primary"
                  loading={actionLoading === cancelModal?.id}
                  onClick={() => void handleCancel()}
                >
                  {t("admin.orders.confirmCancel")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.orders.cancelMessage", {
            email: cancelModal?.user_email,
            plan: cancelModal?.plan_name,
          })}
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
