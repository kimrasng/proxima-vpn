import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert, Box, Button, Container, Header, Modal, SpaceBetween, Spinner,
} from "@cloudscape-design/components";
import {
  borderRadiusItem, colorBorderDividerDefault, colorBackgroundCellShaded, colorTextStatusInfo,
} from "@cloudscape-design/design-tokens";
import { ApiError } from "../api/client";
import type { ResetTicket } from "../api/types";
import * as userApi from "../api/user";
import { useUserResource } from "../hooks/useUserResource";
import { formatBytes } from "../utils/format";
import "./resetTickets.css";

type Props = {
  // Whether the plan has a data cap that a reset would lift. Without one the
  // tickets are still listed, but spending them would reset nothing.
  canReset: boolean;
  onRedeemed: () => void;
};

// Tickets shown like a wallet of passes: each card carries its name, the day it
// was granted and the day it lapses, with the action on the card itself.
export default function ResetTickets({ canReset, onRedeemed }: Props) {
  const { t, i18n } = useTranslation();
  const tickets = useUserResource(userApi.listResetTickets);
  const history = useUserResource(userApi.listResetTicketHistory);
  const [confirm, setConfirm] = useState<ResetTicket | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const dateFormat = (value: string) => new Date(value).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language);
  const daysLeft = (value: string) => Math.max(0, Math.ceil((new Date(value).getTime() - Date.now()) / 86400000));

  const redeem = async () => {
    if (!confirm) return;
    setBusy(true);
    try {
      const result = await userApi.redeemResetTicket(confirm.id);
      setFlash({ type: "success", text: t("user.resetTickets.redeemed", { name: result.name, bytes: formatBytes(result.reset_bytes) }) });
      onRedeemed();
    } catch (err) {
      const code = err instanceof ApiError ? (err.body as { code?: string } | undefined)?.code : undefined;
      const text = code === "used" ? t("user.resetTickets.errorUsed")
        : code === "expired" ? t("user.resetTickets.errorExpired")
        : code === "no_plan" ? t("user.resetTickets.errorNoPlan")
        : t("user.resetTickets.errorGeneric");
      setFlash({ type: "error", text });
    } finally {
      setBusy(false);
      setConfirm(null);
      tickets.refresh();
      history.refresh();
    }
  };

  const items = tickets.data ?? [];
  // Accounts without a capped plan only see the section once they hold a
  // ticket; an empty "no tickets" card would mean nothing to them.
  const used = history.data ?? [];
  if (!canReset && !tickets.loading && !tickets.error && items.length === 0 && used.length === 0 && !flash) return null;
  return (
    <Container header={<Header variant="h2" counter={items.length ? `(${items.length})` : undefined}
      description={t("user.resetTickets.description")}>{t("user.resetTickets.title")}</Header>}>
      <SpaceBetween size="m">
        {flash && <Alert type={flash.type} dismissible onDismiss={() => setFlash(null)}>{flash.text}</Alert>}
        {tickets.loading ? <Spinner /> : tickets.error ? (
          <Alert type="error" action={<Button onClick={tickets.refresh}>{t("user.dashboard.retry")}</Button>}>
            {t("user.resetTickets.loadError")}
          </Alert>
        ) : items.length === 0 ? (
          <Box color="text-body-secondary">{t("user.resetTickets.empty")}</Box>
        ) : (
          <div className="reset-tickets__grid">
            {items.map((ticket) => {
              const days = daysLeft(ticket.expires_at);
              return (
                <div key={ticket.id} className="reset-tickets__card"
                  style={{ background: colorBackgroundCellShaded, borderRadius: borderRadiusItem, borderInlineStart: `4px solid ${colorTextStatusInfo}` }}>
                  <SpaceBetween size="xs">
                    <Box fontWeight="bold" fontSize="heading-s">{ticket.name}</Box>
                    <div className="reset-tickets__dates" style={{ borderBlockStart: `1px solid ${colorBorderDividerDefault}` }}>
                      <div>
                        <Box variant="small" color="text-body-secondary">{t("user.resetTickets.grantedAt")}</Box>
                        <Box>{dateFormat(ticket.granted_at)}</Box>
                      </div>
                      <div>
                        <Box variant="small" color="text-body-secondary">{t("user.resetTickets.expiresAt")}</Box>
                        <Box>{dateFormat(ticket.expires_at)}</Box>
                      </div>
                    </div>
                    <Box variant="small" color={days <= 3 ? "text-status-warning" : "text-body-secondary"}>
                      {t("user.resetTickets.daysLeft", { days })}
                    </Box>
                    <Button fullWidth disabled={!canReset} onClick={() => setConfirm(ticket)}
                      ariaLabel={`${t("user.resetTickets.use")} ${ticket.name}`}>{t("user.resetTickets.use")}</Button>
                  </SpaceBetween>
                </div>
              );
            })}
          </div>
        )}
        {items.length > 0 && !canReset && <Box variant="small" color="text-body-secondary">{t("user.resetTickets.needsPlan")}</Box>}
        {used.length > 0 && (
          <div className="reset-tickets__history" style={{ borderBlockStart: `1px solid ${colorBorderDividerDefault}` }}>
            <Box variant="h4" padding={{ top: "s", bottom: "xs" }}>{t("user.resetTickets.historyTitle")}</Box>
            <ul className="reset-tickets__history-list">
              {used.slice(0, 5).map((ticket) => (
                <li key={ticket.id}>
                  <Box>{ticket.name}</Box>
                  <Box variant="small" color="text-body-secondary">
                    {t("user.resetTickets.historyItem", {
                      at: ticket.used_at ? new Date(ticket.used_at).toLocaleString(i18n.resolvedLanguage ?? i18n.language, { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }) : "-",
                      bytes: formatBytes(ticket.reset_bytes ?? 0),
                    })}
                  </Box>
                </li>
              ))}
            </ul>
          </div>
        )}
      </SpaceBetween>
      <Modal visible={confirm !== null} onDismiss={() => !busy && setConfirm(null)} header={t("user.resetTickets.confirmTitle")}
        closeAriaLabel={t("user.announcements.close")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
          <Button variant="link" disabled={busy} onClick={() => setConfirm(null)}>{t("user.resetTickets.cancel")}</Button>
          <Button variant="primary" loading={busy} onClick={() => void redeem()}>{t("user.resetTickets.confirm")}</Button>
        </SpaceBetween></Box>}>
        {confirm && <SpaceBetween size="s">
          <Box>{t("user.resetTickets.confirmBody", { name: confirm.name })}</Box>
          <Box variant="small" color="text-body-secondary">{t("user.resetTickets.confirmNote")}</Box>
        </SpaceBetween>}
      </Modal>
    </Container>
  );
}
