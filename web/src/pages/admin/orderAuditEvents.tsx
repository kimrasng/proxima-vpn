import { useTranslation } from "react-i18next";
import {
  Box,
  Container,
  Header,
  SpaceBetween,
  StatusIndicator,
  Table,
} from "@cloudscape-design/components";
import type { AdminOrderAuditEvent } from "../../api/types";
import { formatAbsoluteTime } from "../../utils/relativeTime";
import { formatPriceCents } from "../../utils/planPricing";
import { paymentOutcomeIndicatorType } from "./orderStatus";
import { textOrEmpty, timeOrEmpty } from "./orderAuditCells";

export function PaymentEventsSection({ events }: { events: AdminOrderAuditEvent[] }) {
  const { t } = useTranslation();

  return (
    <Container
      header={
        <Header
          variant="h2"
          counter={`(${events.length})`}
          description={t("admin.orderDetail.eventsHint")}
        >
          {t("admin.orderDetail.events")}
        </Header>
      }
    >
      {/* No wrapLines: ten columns squeezed into the container break CJK
          headers one character per line and split labels mid-word. Explicit
          widths let the table scroll horizontally instead.

          tableLabel names that scroll region. The header lives on the Container,
          not the Table, so Cloudscape has no heading to derive aria-labelledby
          from - without this the wrapper is a focusable region with no
          accessible name.

          stickyColumns pins the received-at column, which is what identifies an
          attempt. The row is ~1650px wide, so scrolling to the request id at
          375px otherwise carries that column off screen and leaves every visible
          cell belonging to an attempt the reader can no longer place in time. */}
      <Table
        variant="embedded"
        ariaLabels={{ tableLabel: t("admin.orderDetail.events") }}
        stickyColumns={{ first: 1 }}
        items={events}
        trackBy="id"
        columnDefinitions={[
          {
            id: "receivedAt",
            header: t("admin.orderDetail.eventCol.receivedAt"),
            minWidth: 170,
            cell: (item) => formatAbsoluteTime(item.received_at),
          },
          {
            id: "provider",
            header: t("admin.orderDetail.eventCol.provider"),
            minWidth: 120,
            cell: (item) => textOrEmpty(item.provider),
          },
          {
            id: "outcome",
            header: t("admin.orderDetail.eventCol.outcome"),
            minWidth: 160,
            cell: (item) => (
              <SpaceBetween size="xxxs">
                <StatusIndicator type={paymentOutcomeIndicatorType(item.outcome)}>
                  {t(`admin.orderDetail.outcome.${item.outcome}`, { defaultValue: item.outcome })}
                </StatusIndicator>
                {item.reason && (
                  <Box variant="small" color="text-body-secondary">
                    {item.reason}
                  </Box>
                )}
              </SpaceBetween>
            ),
          },
          {
            id: "amount",
            header: t("admin.orderDetail.eventCol.amount"),
            minWidth: 110,
            cell: (item) =>
              `${formatPriceCents(item.amount_cents)}${item.currency ? ` ${item.currency}` : ""}`,
          },
          {
            id: "externalId",
            header: t("admin.orderDetail.eventCol.externalId"),
            minWidth: 200,
            cell: (item) => textOrEmpty(item.external_id),
          },
          {
            id: "sessionId",
            header: t("admin.orderDetail.eventCol.sessionId"),
            minWidth: 140,
            cell: (item) => textOrEmpty(item.session_id),
          },
          {
            id: "actor",
            header: t("admin.orderDetail.eventCol.actor"),
            minWidth: 200,
            cell: (item) =>
              item.actor_type
                ? `${item.actor_type}${item.actor_id ? `: ${item.actor_id}` : ""}`
                : textOrEmpty(item.actor_id),
          },
          {
            id: "requestIp",
            header: t("admin.orderDetail.eventCol.requestIp"),
            minWidth: 140,
            cell: (item) => textOrEmpty(item.request_ip),
          },
          {
            id: "requestId",
            header: t("admin.orderDetail.eventCol.requestId"),
            minWidth: 200,
            cell: (item) => textOrEmpty(item.request_id),
          },
          {
            id: "processedAt",
            header: t("admin.orderDetail.eventCol.processedAt"),
            minWidth: 170,
            cell: (item) => timeOrEmpty(item.processed_at),
          },
        ]}
        empty={<Box textAlign="center">{t("admin.orderDetail.eventsEmpty")}</Box>}
      />
    </Container>
  );
}
