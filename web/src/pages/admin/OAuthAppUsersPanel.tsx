import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  Header,
  Input,
  Modal,
  SpaceBetween,
  StatusIndicator,
  Table,
  TextFilter,
} from "@cloudscape-design/components";
import { ApiError } from "../../api/client";
import {
  listOAuthAppUsers,
  revokeAllOAuthAppUsers,
  revokeOAuthAppUser,
  scopeKey,
  type OAuthApp,
  type OAuthAppUser,
} from "../../api/oauth";
import { formatRelativeTime } from "../../utils/relativeTime";

type Props = {
  app: OAuthApp;
  onChanged: () => void;
  onNotify: (type: "success" | "error", text: string) => void;
};

type Confirm = { kind: "one"; user: OAuthAppUser } | { kind: "all" } | null;

function errorText(err: unknown, fallback: string): string {
  const body = err instanceof ApiError ? (err.body as { error?: string } | undefined) : undefined;
  return body?.error || fallback;
}

export default function OAuthAppUsersPanel({ app, onChanged, onNotify }: Props) {
  const { t, i18n } = useTranslation();
  const [users, setUsers] = useState<OAuthAppUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState("");
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setUsers(await listOAuthAppUsers(app.id));
    } catch (err) {
      onNotify("error", errorText(err, t("admin.oauthApps.users.loadError")));
    } finally {
      setLoading(false);
    }
  }, [app.id, onNotify, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const needle = filter.trim().toLowerCase();
  const shown = needle
    ? users.filter((u) => u.email.toLowerCase().includes(needle) || u.name.toLowerCase().includes(needle))
    : users;

  const close = () => {
    if (busy) return;
    setConfirm(null);
    setTyped("");
  };

  const run = async () => {
    if (!confirm) return;
    setBusy(true);
    try {
      const res = confirm.kind === "all"
        ? await revokeAllOAuthAppUsers(app.id)
        : await revokeOAuthAppUser(app.id, confirm.user.user_id);
      onNotify("success", confirm.kind === "all"
        ? t("admin.oauthApps.users.revokedAll", { count: res.revoked, app: app.name })
        : t("admin.oauthApps.users.revokedOne", { email: confirm.user.email }));
      setConfirm(null);
      setTyped("");
      await load();
      onChanged();
    } catch (err) {
      onNotify("error", errorText(err, t("admin.oauthApps.users.revokeError")));
    } finally {
      setBusy(false);
    }
  };

  const fmt = (iso: string) => new Date(iso).toLocaleString(i18n.resolvedLanguage ?? i18n.language);
  const allConfirmed = confirm?.kind !== "all" || typed.trim() === app.name;

  return (
    <>
      <Table
        loading={loading}
        loadingText={t("common.loading")}
        items={shown}
        trackBy="user_id"
        wrapLines
        variant="container"
        header={
          <Header
            counter={`(${users.length})`}
            description={t("admin.oauthApps.users.description", { app: app.name })}
            actions={
              <SpaceBetween direction="horizontal" size="xs">
                <Button iconName="refresh" ariaLabel={t("admin.oauthApps.users.refresh")} onClick={() => void load()} />
                <Button disabled={users.length === 0} onClick={() => setConfirm({ kind: "all" })}>
                  {t("admin.oauthApps.users.revokeAll")}
                </Button>
              </SpaceBetween>
            }
          >
            {t("admin.oauthApps.users.title", { app: app.name })}
          </Header>
        }
        filter={
          <TextFilter
            filteringText={filter}
            filteringPlaceholder={t("admin.oauthApps.users.search")}
            filteringAriaLabel={t("admin.oauthApps.users.search")}
            countText={needle ? t("admin.oauthApps.users.matches", { count: shown.length }) : undefined}
            onChange={({ detail }) => setFilter(detail.filteringText)}
          />
        }
        empty={
          <Box textAlign="center" color="text-body-secondary" padding="l">
            {needle ? t("admin.oauthApps.users.noMatch") : t("admin.oauthApps.users.empty")}
          </Box>
        }
        columnDefinitions={[
          {
            id: "user",
            header: t("admin.oauthApps.users.col.user"),
            cell: (u) => (
              <div className="oauth-apps__name">
                <Box variant="strong">{u.email}</Box>
                {u.name && <Box color="text-body-secondary" fontSize="body-s">{u.name}</Box>}
              </div>
            ),
          },
          {
            id: "scopes",
            header: t("admin.oauthApps.users.col.scopes"),
            cell: (u) => (
              <div className="oauth-apps__chips">
                {u.scopes.map((s) => <span key={s} className="oauth-apps__chip">{t(`auth.oauth.scope.${scopeKey(s)}.title`)}</span>)}
              </div>
            ),
          },
          { id: "granted", header: t("admin.oauthApps.users.col.granted"), cell: (u) => fmt(u.granted_at) },
          {
            id: "used",
            header: t("admin.oauthApps.users.col.lastUsed"),
            cell: (u) => (u.last_used_at ? formatRelativeTime(t, u.last_used_at) : t("admin.oauthApps.users.neverUsed")),
          },
          {
            id: "session",
            header: t("admin.oauthApps.users.col.session"),
            cell: (u) => (
              <StatusIndicator type={u.active_tokens > 0 ? "success" : "stopped"}>
                {u.active_tokens > 0 ? t("admin.oauthApps.users.sessionLive") : t("admin.oauthApps.users.sessionNone")}
              </StatusIndicator>
            ),
          },
          {
            id: "actions",
            header: t("admin.oauthApps.users.col.actions"),
            cell: (u) => (
              <Button variant="inline-link" onClick={() => setConfirm({ kind: "one", user: u })}>
                {t("admin.oauthApps.users.revoke")}
              </Button>
            ),
          },
        ]}
      />

      <Modal
        visible={confirm !== null}
        onDismiss={close}
        header={confirm?.kind === "all" ? t("admin.oauthApps.users.revokeAllTitle") : t("admin.oauthApps.users.revokeTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" disabled={busy} onClick={close}>{t("common.cancel")}</Button>
              <Button variant="primary" loading={busy} disabled={!allConfirmed} onClick={() => void run()}>
                {confirm?.kind === "all" ? t("admin.oauthApps.users.revokeAllConfirm", { count: users.length }) : t("admin.oauthApps.users.revoke")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        {confirm?.kind === "one" && (
          <div className="oauth-apps__form">
            {t("admin.oauthApps.users.revokeBody", { email: confirm.user.email, app: app.name })}
          </div>
        )}
        {confirm?.kind === "all" && (
          <SpaceBetween size="m">
            <div className="oauth-apps__form">{t("admin.oauthApps.users.revokeAllBody", { count: users.length, app: app.name })}</div>
            <Box color="text-body-secondary" fontSize="body-s">{t("admin.oauthApps.users.revokeAllKeep")}</Box>
            <div>
              <Box variant="awsui-key-label">{t("admin.oauthApps.users.typeName", { app: app.name })}</Box>
              <Input
                value={typed}
                placeholder={app.name}
                ariaLabel={t("admin.oauthApps.users.typeName", { app: app.name })}
                onChange={({ detail }) => setTyped(detail.value)}
              />
            </div>
          </SpaceBetween>
        )}
      </Modal>
    </>
  );
}
