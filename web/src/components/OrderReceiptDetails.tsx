import { useTranslation } from "react-i18next";
import { Badge, Box, SpaceBetween, StatusIndicator } from "@cloudscape-design/components";
import { borderRadiusItem, colorBackgroundCellShaded, colorBorderDividerDefault } from "@cloudscape-design/design-tokens";
import type { OrderReceipt } from "../api/types";
import "./OrderReceiptDetails.css";
import { formatPriceCents } from "../utils/planPricing";

export default function OrderReceiptDetails({ receipt }: { receipt: OrderReceipt }) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? i18n.language;
  const date = (value?: string) => (value ? new Date(value).toLocaleDateString(locale) : "-");
  const dateTime = (value?: string) => (value ? new Date(value).toLocaleString(locale, { year: "numeric", month: "long", day: "numeric", hour: "numeric", minute: "2-digit" }) : "-");
  const Row = ({ label, children }: { label: string; children: React.ReactNode }) => (
    <div className="order-receipt__row">
      <Box color="text-body-secondary">{label}</Box>
      <Box textAlign="right">{children}</Box>
    </div>
  );
  const divider = <div style={{ borderBlockStart: `1px solid ${colorBorderDividerDefault}` }} />;
  const subtotal = receipt.price_cents + receipt.discount_cents;

  return (
    <SpaceBetween size="m">
      <Box textAlign="center">
        <SpaceBetween size="xxs" alignItems="center">
          <StatusIndicator type="success">{t("user.orders.receipt.statusPaid")}</StatusIndicator>
          <Box fontSize="heading-xl" fontWeight="bold" padding="n">{formatPriceCents(receipt.price_cents)}</Box>
          <Box color="text-body-secondary">{receipt.plan_name} · {receipt.duration_days} {t("user.plan.durationDays")}</Box>
        </SpaceBetween>
      </Box>

      <div className="order-receipt__panel" style={{ background: colorBackgroundCellShaded, borderRadius: borderRadiusItem }}>
        <Row label={t("user.plan.order.receipt.orderId")}><Box variant="code">{receipt.order_id}</Box></Row>
        <Row label={t("user.orders.receipt.orderDate")}>{date(receipt.created_at)}</Row>
        <Row label={t("user.plan.order.receipt.paidAt")}>{dateTime(receipt.paid_at)}</Row>
        <Row label={t("user.plan.order.receipt.method")}>{receipt.provider === "stripe" || receipt.provider === "stripe_subscription" ? "Stripe"
          : receipt.provider === "free_trial" ? t("trialSubscription.title")
          : receipt.provider === "grant" ? t("user.orders.receipt.methodGrant")
          : t("user.plan.order.receipt.methodPanel")}</Row>
        <Row label={t("user.plan.order.receipt.email")}>{receipt.email}</Row>
      </div>

      <div className="order-receipt__panel" style={{ background: colorBackgroundCellShaded, borderRadius: borderRadiusItem }}>
        {receipt.discount_cents === 0 && (receipt.promotion_name || receipt.promotion_code) && (
          <Row label={t("user.promo.benefitLabel")}>{receipt.promotion_name || receipt.promotion_code}</Row>
        )}
        {receipt.discount_cents > 0 && (
          <>
            <Row label={t("user.orders.receipt.subtotal")}>{formatPriceCents(subtotal)}</Row>
            <Row label={t("user.orders.receipt.discount")}>
              <SpaceBetween size="xxxs">
                {(receipt.promotion_name || receipt.promotion_code) && <Box>{receipt.promotion_name || receipt.promotion_code}</Box>}
                <Box color="text-status-success">-{formatPriceCents(receipt.discount_cents)}</Box>
              </SpaceBetween>
            </Row>
            {divider}
          </>
        )}
        <Row label={t("user.plan.order.receipt.total")}>
          <Box fontSize="heading-s" fontWeight="bold">{formatPriceCents(receipt.price_cents)}</Box>
        </Row>
      </div>

      {(receipt.plan_expires_after || receipt.note) && (
        <div className="order-receipt__panel" style={{ background: colorBackgroundCellShaded, borderRadius: borderRadiusItem }}>
          {receipt.plan_expires_after && (
            <Row label={t("user.orders.receipt.term")}>
              {receipt.plan_expires_before
                ? t("user.orders.receipt.termChange", { from: date(receipt.plan_expires_before), to: date(receipt.plan_expires_after) })
                : t("user.orders.receipt.termUntil", { to: date(receipt.plan_expires_after) })}
            </Row>
          )}
          {receipt.note && <Row label={t("user.orders.receipt.note")}><Badge color="blue">{receipt.note}</Badge></Row>}
        </div>
      )}
    </SpaceBetween>
  );
}
