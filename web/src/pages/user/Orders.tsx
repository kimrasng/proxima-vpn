import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Spinner,
  Alert,
  Badge,
  Box,
  Button,
  ContentLayout,
  Flashbar,
  type FlashbarProps,
  Header,
  Modal,
  SpaceBetween,
  StatusIndicator,
  type StatusIndicatorProps,
  Table,
} from "@cloudscape-design/components";
import type { OrderReceipt, OrderResponse, OrderStatus } from "../../api/types";
import OrderReceiptDetails from "../../components/OrderReceiptDetails";
import * as userApi from "../../api/user";
import PortalEmpty from "../../components/PortalEmpty";
import { formatPriceCents } from "../../utils/planPricing";

// "expired" borrows the warning severity the user pages already use for a lapsed
// plan: it needs attention but nothing failed.
function orderStatusIndicatorType(status: OrderStatus): StatusIndicatorProps.Type {
  switch (status) {
    case "paid":
      return "success";
    case "cancelled":
      return "stopped";
    case "expired":
      return "warning";
    default:
      return "pending";
  }
}

export default function Orders() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [orders, setOrders] = useState<OrderResponse[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [receipt, setReceipt] = useState<OrderReceipt | null>(null);
  const [receiptLoading, setReceiptLoading] = useState<string | null>(null);
  const [cancelModal, setCancelModal] = useState<OrderResponse | null>(null);
  const locale = i18n.resolvedLanguage ?? i18n.language;

  const loadData = async () => {
    try {
      setLoading(true);
      setLoadError(false);
      setOrders(await userApi.listMyOrders());
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadData();
  }, []);

  const handleCancelOrder = async () => {
    if (!cancelModal) return;
    try {
      setSubmitting(true);
      await userApi.cancelOrder(cancelModal.id);
      setFlash([{ type: "success", content: t("user.plan.order.cancelSuccess"), dismissible: true, onDismiss: () => setFlash([]) }]);
      setCancelModal(null);
      await loadData();
    } catch {
      setFlash([{ type: "error", content: t("user.plan.order.cancelError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setSubmitting(false);
    }
  };

  const handleViewReceipt = async (orderId: string) => {
    setReceiptLoading(orderId);
    setReceipt(null);
    try {
      setReceipt(await userApi.getOrderReceipt(orderId));
    } catch {
      setFlash([{ type: "error", content: t("user.plan.order.receipt.error"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally {
      setReceiptLoading(null);
    }
  };


  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={t("user.orders.description")}
          actions={<Button iconName="refresh" loading={loading} onClick={() => void loadData()}>{t("user.dashboard.refresh")}</Button>}
        >
          {t("user.orders.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        <Flashbar items={flash} />
        {loadError ? (
          <Alert type="error" action={<Button onClick={() => void loadData()}>{t("user.dashboard.retry")}</Button>}>
            {t("user.orders.loadError")}
          </Alert>
        ) : !loading && orders.length === 0 ? (
          <PortalEmpty
            title={t("user.plan.order.noOrders")}
            description={t("user.orders.emptyHint")}
            action={<Button variant="primary" onClick={() => navigate("/portal/plan")}>{t("user.plan.checkoutModal.viewPlans")}</Button>}
          />
        ) : (
          <Table
            loading={loading}
            loadingText={t("user.orders.title")}
            items={orders}
            trackBy="id"
            header={<Header variant="h2" counter={`(${orders.length})`}>{t("user.plan.order.myOrders")}</Header>}
            columnDefinitions={[
              {
                id: "plan",
                header: t("user.plan.order.col.plan"),
                cell: (item) => item.provider === "grant" ? (
                  <SpaceBetween size="xxxs">
                    <Box>{item.plan_name}</Box>
                    <Box variant="small" color="text-status-info">{t("user.orders.grantedBadge")}</Box>
                  </SpaceBetween>
                ) : item.plan_name,
              },
              {
                id: "duration",
                header: t("user.plan.order.col.duration"),
                cell: (item) => `${item.duration_days} ${t("user.plan.durationDays")}`,
              },
              {
                id: "price",
                header: t("user.plan.order.col.price"),
                cell: (item) =>
                  (item.discount_cents ?? 0) > 0 ? (
                    <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
                      <Box color="text-status-inactive">
                        <s>{formatPriceCents(item.price_cents + (item.discount_cents ?? 0))}</s>
                      </Box>
                      <Box>{formatPriceCents(item.price_cents)}</Box>
                      {item.promotion_code && (
                        <Badge color="green">{item.promotion_code}</Badge>
                      )}
                    </SpaceBetween>
                  ) : (
                    formatPriceCents(item.price_cents)
                  ),
              },
              {
                id: "status",
                header: t("user.plan.order.col.status"),
                cell: (item) => (
                  <StatusIndicator type={orderStatusIndicatorType(item.status)}>
                    {t(`user.plan.order.status.${item.status}`)}
                  </StatusIndicator>
                ),
              },
              {
                id: "createdAt",
                header: t("user.plan.order.col.createdAt"),
                cell: (item) => new Date(item.created_at).toLocaleString(locale),
              },
              {
                id: "actions",
                header: t("user.plan.order.col.actions"),
                cell: (item) =>
                  item.status === "pending" ? (
                    <SpaceBetween direction="horizontal" size="xs">
                      <Button variant="inline-link" onClick={() => navigate("/portal/plan")}>
                        {t("user.plan.order.checkout.payNow")}
                      </Button>
                      <Button variant="inline-link" onClick={() => setCancelModal(item)}>
                        {t("user.plan.order.cancel")}
                      </Button>
                    </SpaceBetween>
                  ) : item.status === "paid" ? (
                    <Button
                      variant="inline-link"
                      disabled={receiptLoading !== null}
                      onClick={() => void handleViewReceipt(item.id)}
                    >
                      {t("user.plan.order.receipt.view")}
                    </Button>
                  ) : (
                    <Box color="text-status-inactive">—</Box>
                  ),
              },
            ]}
            empty={<Box textAlign="center">{t("user.plan.order.noOrders")}</Box>}
          />
        )}

        <Modal
          visible={receipt !== null || receiptLoading !== null}
          onDismiss={() => { setReceipt(null); setReceiptLoading(null); }}
          closeAriaLabel={t("user.announcements.close")}
          header={t("user.plan.order.receipt.title")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setReceipt(null); setReceiptLoading(null); }}>{t("user.plan.order.dismiss")}</Button>
                {receipt?.receipt_url && (
                  <Button
                    variant="primary"
                    href={receipt.receipt_url}
                    target="_blank"
                    iconName="external"
                    iconAlign="right"
                  >
                    {t(receipt.receipt_url.includes("invoice.stripe.com") ? "user.plan.order.receipt.openInvoice" : "user.plan.order.receipt.openStripe")}
                  </Button>
                )}
              </SpaceBetween>
            </Box>
          }
        >
          {receipt ? <OrderReceiptDetails receipt={receipt} /> : (
            <Box textAlign="center" padding="l"><Spinner size="large" /></Box>
          )}
        </Modal>

        <Modal
          visible={cancelModal !== null}
          onDismiss={() => setCancelModal(null)}
          header={t("user.plan.order.cancelTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setCancelModal(null)}>{t("user.plan.order.dismiss")}</Button>
                <Button variant="primary" loading={submitting} onClick={() => void handleCancelOrder()}>
                  {t("user.plan.order.confirmCancel")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("user.plan.order.cancelMessage", {
            plan: cancelModal?.plan_name,
            days: cancelModal?.duration_days,
          })}
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
