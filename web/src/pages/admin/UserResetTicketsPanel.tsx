import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert, Box, Button, Header, Modal, SpaceBetween, StatusIndicator, Table,
} from "@cloudscape-design/components";
import { ApiError } from "../../api/client";
import { listUserResetTickets, revokeUserResetTicket } from "../../api/admin";
import type { UserResetTicket } from "../../api/types";
import { formatBytes } from "../../utils/format";
import { formatAbsoluteTime } from "../../utils/relativeTime";

type Props = { userId: string };

// Every ticket the user was ever granted. Unused tickets can be revoked; used
// ones stay as the record of a reset that already happened.
export default function UserResetTicketsPanel({ userId }: Props) {
  const { t } = useTranslation();
  const [tickets, setTickets] = useState<UserResetTicket[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [confirm, setConfirm] = useState<UserResetTicket | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ type: "success" | "error"; text: string } | null>(null);

  const load = useCallback(async () => {
    try {
      setTickets(await listUserResetTickets(userId));
      setLoadError(false);
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  }, [userId]);

  useEffect(() => { void load(); }, [load]);

  const revoke = async () => {
    if (!confirm) return;
    setBusy(true);
    try {
      await revokeUserResetTicket(userId, confirm.id);
      setFlash({ type: "success", text: t("admin.userDetail.resetTickets.revoked", { name: confirm.name }) });
    } catch (err) {
      const used = err instanceof ApiError && err.status === 409;
      setFlash({ type: "error", text: used ? t("admin.userDetail.resetTickets.revokeUsed") : t("admin.userDetail.resetTickets.revokeError") });
    } finally {
      setBusy(false);
      setConfirm(null);
      await load();
    }
  };

  const state = (ticket: UserResetTicket) => ticket.used_at ? "used"
    : new Date(ticket.expires_at).getTime() <= Date.now() ? "expired" : "usable";
  const usable = tickets.filter((ticket) => state(ticket) === "usable").length;

  return (
    <SpaceBetween size="m">
      {flash && <Alert type={flash.type} dismissible onDismiss={() => setFlash(null)}>{flash.text}</Alert>}
      {loadError && <Alert type="error" action={<Button onClick={() => void load()}>{t("admin.userDetail.resetTickets.retry")}</Button>}>
        {t("admin.userDetail.resetTickets.loadError")}
      </Alert>}
      <Table
        loading={loading}
        loadingText={t("admin.userDetail.resetTickets.loading")}
        items={tickets}
        trackBy="id"
        wrapLines
        header={<Header counter={`(${usable}/${tickets.length})`} description={t("admin.userDetail.resetTickets.description")}>
          {t("admin.userDetail.resetTickets.title")}
        </Header>}
        columnDefinitions={[
          { id: "name", header: t("admin.userDetail.resetTickets.col.name"), cell: (ticket) => ticket.name },
          { id: "state", header: t("admin.userDetail.resetTickets.col.state"), cell: (ticket) => {
            const s = state(ticket);
            return <StatusIndicator type={s === "usable" ? "success" : s === "used" ? "info" : "stopped"}>
              {s === "usable" ? t("admin.userDetail.resetTickets.state.usable")
                : s === "used" ? t("admin.userDetail.resetTickets.state.used")
                : t("admin.userDetail.resetTickets.state.expired")}
            </StatusIndicator>;
          } },
          { id: "granted", header: t("admin.userDetail.resetTickets.col.granted"), cell: (ticket) => formatAbsoluteTime(ticket.granted_at) },
          { id: "expires", header: t("admin.userDetail.resetTickets.col.expires"), cell: (ticket) => formatAbsoluteTime(ticket.expires_at) },
          { id: "used", header: t("admin.userDetail.resetTickets.col.used"), cell: (ticket) => ticket.used_at
            ? t("admin.userDetail.resetTickets.usedDetail", { at: formatAbsoluteTime(ticket.used_at), bytes: formatBytes(ticket.reset_bytes ?? 0) })
            : "—" },
          { id: "actions", header: t("admin.userDetail.resetTickets.col.actions"), cell: (ticket) => ticket.used_at ? "—" : (
            <Button variant="inline-link" onClick={() => setConfirm(ticket)}
              ariaLabel={`${t("admin.userDetail.resetTickets.revoke")} ${ticket.name}`}>{t("admin.userDetail.resetTickets.revoke")}</Button>
          ) },
        ]}
        empty={<Box textAlign="center" color="text-body-secondary">{t("admin.userDetail.resetTickets.empty")}</Box>}
      />
      <Modal visible={confirm !== null} onDismiss={() => !busy && setConfirm(null)} header={t("admin.userDetail.resetTickets.confirmTitle")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
          <Button variant="link" disabled={busy} onClick={() => setConfirm(null)}>{t("admin.userDetail.resetTickets.cancel")}</Button>
          <Button variant="primary" loading={busy} onClick={() => void revoke()}>{t("admin.userDetail.resetTickets.confirm")}</Button>
        </SpaceBetween></Box>}>
        {confirm && t("admin.userDetail.resetTickets.confirmBody", { name: confirm.name })}
      </Modal>
    </SpaceBetween>
  );
}
