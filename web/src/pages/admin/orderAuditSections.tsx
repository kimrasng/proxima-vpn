import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Container,
  Header,
  KeyValuePairs,
  Link,
  SpaceBetween,
  StatusIndicator,
} from "@cloudscape-design/components";
import type {
  AdminOrderAuditContext,
  AdminOrderAuditGrant,
  AdminOrderAuditOrder,
} from "../../api/types";
import { formatAbsoluteTime } from "../../utils/relativeTime";
import { formatPriceCents } from "../../utils/planPricing";
import { orderStatusIndicatorType } from "./orderStatus";
import { textOrEmpty, timeOrEmpty } from "./orderAuditCells";

export function OrderSummarySection({ order }: { order: AdminOrderAuditOrder }) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  return (
    <Container header={<Header variant="h2">{t("admin.orderDetail.summary")}</Header>}>
      <KeyValuePairs
        columns={3}
        items={[
          {
            label: t("admin.orders.col.status"),
            value: (
              <StatusIndicator type={orderStatusIndicatorType(order.status)}>
                {t(`admin.orders.status.${order.status}`, { defaultValue: order.status })}
              </StatusIndicator>
            ),
          },
          {
            label: t("admin.orders.col.user"),
            value: (
              <Link
                href={`/admin/users/${order.user_id}`}
                onFollow={(event) => {
                  event.preventDefault();
                  void navigate(`/admin/users/${order.user_id}`);
                }}
              >
                {order.user_email}
              </Link>
            ),
          },
          { label: t("admin.orderDetail.userName"), value: textOrEmpty(order.user_name) },
          { label: t("admin.orders.col.plan"), value: order.plan_name },
          {
            label: t("admin.orders.col.duration"),
            value: t("admin.orders.durationValue", { days: order.duration_days }),
          },
          { label: t("admin.orders.col.price"), value: formatPriceCents(order.price_cents) },
          {
            label: t("admin.orderDetail.discount"),
            value:
              order.discount_cents > 0
                ? formatPriceCents(order.discount_cents)
                : textOrEmpty(""),
          },
          {
            label: t("admin.orders.col.provider"),
            value: textOrEmpty(order.provider),
          },
          {
            label: t("admin.orderDetail.providerSessionId"),
            value: textOrEmpty(order.provider_session_id),
          },
          { label: t("admin.orders.col.createdAt"), value: formatAbsoluteTime(order.created_at) },
          { label: t("admin.orderDetail.paidAt"), value: timeOrEmpty(order.paid_at) },
          { label: t("admin.orderDetail.paidBy"), value: textOrEmpty(order.paid_by) },
          { label: t("admin.orderDetail.expiresAt"), value: timeOrEmpty(order.expires_at) },
          { label: t("admin.orderDetail.expiredAt"), value: timeOrEmpty(order.expired_at) },
          { label: t("admin.orderDetail.cancelledAt"), value: timeOrEmpty(order.cancelled_at) },
          { label: t("admin.orderDetail.orderId"), value: order.id },
        ]}
      />
    </Container>
  );
}

// A null context is reported as its own state rather than a row of dashes: an
// order placed before capture shipped, or one whose tracking columns retention
// has purged, must not be mistaken for a client that sent no headers.
export function RequestContextSection({ context }: { context: AdminOrderAuditContext | null }) {
  const { t } = useTranslation();

  return (
    <Container
      fitHeight
      header={
        <Header variant="h2" description={t("admin.orderDetail.contextHint")}>
          {t("admin.orderDetail.context")}
        </Header>
      }
    >
      {context === null ? (
        <Alert type="info">{t("admin.orderDetail.contextUnavailable")}</Alert>
      ) : (
        <SpaceBetween size="m">
          <KeyValuePairs
            columns={2}
            items={[
              { label: t("admin.orderDetail.origin"), value: textOrEmpty(context.origin) },
              { label: t("admin.orderDetail.clientIp"), value: textOrEmpty(context.client_ip) },
              { label: t("admin.orderDetail.browser"), value: textOrEmpty(context.browser_family) },
              { label: t("admin.orderDetail.os"), value: textOrEmpty(context.os_family) },
              { label: t("admin.orderDetail.locale"), value: textOrEmpty(context.locale) },
              {
                label: t("admin.orderDetail.userAgent"),
                value: textOrEmpty(context.user_agent),
              },
              {
                label: t("admin.orderDetail.deviceFingerprint"),
                value: (
                  <SpaceBetween size="xxxs">
                    {textOrEmpty(context.device_fingerprint)}
                    <Box variant="small" color="text-body-secondary">
                      {t("admin.orderDetail.deviceFingerprintHint")}
                    </Box>
                  </SpaceBetween>
                ),
              },
            ]}
          />
        </SpaceBetween>
      )}
    </Container>
  );
}

export function GrantSection({ grant }: { grant: AdminOrderAuditGrant | null }) {
  const { t } = useTranslation();

  return (
    <Container
      fitHeight
      header={
        <Header variant="h2" description={t("admin.orderDetail.grantHint")}>
          {t("admin.orderDetail.grant")}
        </Header>
      }
    >
      {grant === null ? (
        <Alert type="info">{t("admin.orderDetail.grantUnavailable")}</Alert>
      ) : (
        <KeyValuePairs
          columns={2}
          items={[
            {
              label: t("admin.orderDetail.planExpiresBefore"),
              // A first purchase has no "before", which is not the same as a
              // missing record - it reads as "no plan window yet".
              value: grant.plan_expires_before
                ? formatAbsoluteTime(grant.plan_expires_before)
                : t("admin.orderDetail.noPriorExpiry"),
            },
            {
              label: t("admin.orderDetail.planExpiresAfter"),
              value: timeOrEmpty(grant.plan_expires_after),
            },
          ]}
        />
      )}
    </Container>
  );
}
