import { useTranslation } from "react-i18next";
import { Box, ColumnLayout, SpaceBetween } from "@cloudscape-design/components";
import type { OrderReceipt } from "../api/types";
import { formatPriceCents } from "../utils/planPricing";

export default function OrderReceiptDetails({ receipt }: { receipt: OrderReceipt }) {
  const { t, i18n } = useTranslation();
  return (
<ColumnLayout columns={2} variant="text-grid">
      <SpaceBetween size="xxs">
        <Box variant="awsui-key-label">{t("user.plan.order.receipt.orderId")}</Box>
        <Box variant="code">{receipt.order_id}</Box>
      </SpaceBetween>
      <SpaceBetween size="xxs">
        <Box variant="awsui-key-label">{t("user.plan.order.receipt.paidAt")}</Box>
        <Box>{receipt.paid_at ? new Date(receipt.paid_at).toLocaleString(i18n.resolvedLanguage ?? i18n.language) : "-"}</Box>
      </SpaceBetween>
      <SpaceBetween size="xxs">
        <Box variant="awsui-key-label">{t("user.plan.planName")}</Box>
        <Box>{receipt.plan_name}</Box>
      </SpaceBetween>
      <SpaceBetween size="xxs">
        <Box variant="awsui-key-label">{t("user.plan.duration")}</Box>
        <Box>{receipt.duration_days} {t("user.plan.durationDays")}</Box>
      </SpaceBetween>
      {receipt.discount_cents > 0 && (
        <SpaceBetween size="xxs">
          <Box variant="awsui-key-label">{t("user.plan.promotion.discountApplied")}</Box>
          <Box>-{formatPriceCents(receipt.discount_cents)}</Box>
        </SpaceBetween>
      )}
      <SpaceBetween size="xxs">
        <Box variant="awsui-key-label">{t("user.plan.order.receipt.total")}</Box>
        <Box fontWeight="bold">{formatPriceCents(receipt.price_cents)}</Box>
      </SpaceBetween>
      <SpaceBetween size="xxs">
        <Box variant="awsui-key-label">{t("user.plan.order.receipt.email")}</Box>
        <Box>{receipt.email}</Box>
      </SpaceBetween>
      <SpaceBetween size="xxs">
        <Box variant="awsui-key-label">{t("user.plan.order.receipt.method")}</Box>
        <Box>{receipt.provider === "stripe" ? "Stripe"
          : receipt.provider === "grant" ? t("user.orders.receipt.methodGrant")
          : t("user.plan.order.receipt.methodPanel")}</Box>
      </SpaceBetween>
      {receipt.note && (
        <SpaceBetween size="xxs">
          <Box variant="awsui-key-label">{t("user.orders.receipt.note")}</Box>
          <Box>{receipt.note}</Box>
        </SpaceBetween>
      )}
      {receipt.plan_expires_after && (
        <SpaceBetween size="xxs">
          <Box variant="awsui-key-label">{t("user.orders.receipt.term")}</Box>
          <Box>{receipt.plan_expires_before
            ? t("user.orders.receipt.termChange", {
                from: new Date(receipt.plan_expires_before).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language),
                to: new Date(receipt.plan_expires_after).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language),
              })
            : t("user.orders.receipt.termUntil", {
                to: new Date(receipt.plan_expires_after).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language),
              })}</Box>
        </SpaceBetween>
      )}
    </ColumnLayout>
  );
}
