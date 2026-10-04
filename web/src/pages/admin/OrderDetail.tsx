import { useCallback, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  ColumnLayout,
  ContentLayout,
  Header,
  SpaceBetween,
  Spinner,
} from "@cloudscape-design/components";
import { getOrderAudit } from "../../api/admin";
import type { AdminOrderAudit } from "../../api/types";
import { useManualRefresh } from "../../hooks/useManualRefresh";
import { usePublishBreadcrumbLeaf } from "../../hooks/useBreadcrumbLeaf";
import { formatTime } from "../../utils/relativeTime";
import {
  GrantSection,
  OrderSummarySection,
  RequestContextSection,
} from "./orderAuditSections";
import { PaymentEventsSection } from "./orderAuditEvents";
import "./orderAudit.css";

const REFRESH_INTERVAL = 60000;

// Keyed on the route param for the same reason UserDetail is: navigating
// between two order ids without remounting would leave one order's audit
// record on screen under the other order's header.
//
// The line-breaking wrapper sits outside that, so the loading and not-found
// branches get the rule too - both render prose that wraps.
export default function OrderDetail() {
  const { orderId } = useParams<{ orderId: string }>();
  return (
    <div className="order-audit-view">
      <OrderDetailPage key={orderId ?? ""} />
    </div>
  );
}

function OrderDetailPage() {
  const { orderId } = useParams<{ orderId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();

  const [audit, setAudit] = useState<AdminOrderAudit | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  usePublishBreadcrumbLeaf(
    audit ? `${audit.order.plan_name} · ${audit.order.user_email}` : null,
  );

  const fetchAudit = useCallback(async () => {
    if (!orderId) return;
    try {
      setAudit(await getOrderAudit(orderId));
      setError(null);
    } catch {
      setError(t("admin.orderDetail.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [orderId, t]);

  const { refreshing, lastUpdated, refresh } = useManualRefresh(fetchAudit, REFRESH_INTERVAL);

  if (loading && !audit) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.orderDetail.title")}</Header>}>
        <Box textAlign="center" padding="xxl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  if (!audit) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.orderDetail.title")}</Header>}>
        <Alert type="error" header={error ?? t("admin.orderDetail.notFound")}>
          <Button onClick={() => void navigate("/admin/orders")}>
            {t("admin.orderDetail.backToList")}
          </Button>
        </Alert>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={
            lastUpdated
              ? `${t("admin.nodeDetail.lastRefreshed")}: ${formatTime(lastUpdated)}`
              : t("admin.orderDetail.subtitle")
          }
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                iconName="refresh"
                ariaLabel={t("admin.orderDetail.refresh")}
                loading={refreshing}
                onClick={refresh}
              />
              <Button onClick={() => void navigate("/admin/orders")}>
                {t("admin.orderDetail.backToList")}
              </Button>
            </SpaceBetween>
          }
        >
          {t("admin.orderDetail.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {/* A refresh that fails leaves the record already on screen in place: a
            transient error must not blank an audit an admin is reading. */}
        {error && (
          <Alert type="error" dismissible onDismiss={() => setError(null)}>
            {error}
          </Alert>
        )}

        <OrderSummarySection order={audit.order} />

        <ColumnLayout columns={2} minColumnWidth={380}>
          <RequestContextSection context={audit.request_context} />
          <GrantSection grant={audit.grant} />
        </ColumnLayout>

        <PaymentEventsSection events={audit.payment_events} />
      </SpaceBetween>
    </ContentLayout>
  );
}
