import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Box, Button, Container, Header, Modal, SpaceBetween, Spinner, type FlashbarProps } from "@cloudscape-design/components";
import { listOAuthGrants, revokeOAuthGrant, scopeKey, type OAuthGrant } from "../api/oauth";

type Props = { onMessage: (message: FlashbarProps.MessageDefinition) => void };

export default function ConnectedApps({ onMessage }: Props) {
  const { t, i18n } = useTranslation();
  const [grants, setGrants] = useState<OAuthGrant[] | null>(null);
  const [revoking, setRevoking] = useState<OAuthGrant | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setGrants(await listOAuthGrants());
    } catch {
      setGrants([]);
      onMessage({ type: "error", content: t("user.account.apps.loadError") });
    }
  }, [onMessage, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const fmt = (iso: string) =>
    new Date(iso).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language, { year: "numeric", month: "short", day: "numeric" }).replace(/ /g, "\u00a0");

  const revoke = async () => {
    if (!revoking) return;
    setBusy(true);
    try {
      await revokeOAuthGrant(revoking.client_id);
      onMessage({ type: "success", content: t("user.account.apps.revoked", { app: revoking.name }) });
      setRevoking(null);
      await load();
    } catch {
      onMessage({ type: "error", content: t("user.account.apps.revokeError") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Container header={<Header variant="h2" description={t("user.account.apps.hint")}>{t("user.account.apps.title")}</Header>}>
      {grants === null ? (
        <Box textAlign="center" padding="l"><Spinner /></Box>
      ) : grants.length === 0 ? (
        <Box textAlign="center" padding="l" color="text-body-secondary">
          <SpaceBetween size="xxs">
            <Box variant="strong" color="text-body-secondary">{t("user.account.apps.emptyTitle")}</Box>
            <Box color="text-body-secondary">{t("user.account.apps.emptyBody")}</Box>
          </SpaceBetween>
        </Box>
      ) : (
        <ul className="connected-apps">
          {grants.map((g) => (
            <li key={g.client_id} className="connected-apps__item">
              <div className="connected-apps__main">
                <Box variant="h4">{g.name}</Box>
                {g.description && <Box color="text-body-secondary" fontSize="body-s">{g.description}</Box>}
                <div className="connected-apps__scopes">
                  {g.scopes.map((s) => (
                    <span key={s} className="connected-apps__chip">{t(`auth.oauth.scope.${scopeKey(s)}.title`)}</span>
                  ))}
                </div>
                <Box color="text-body-secondary" fontSize="body-s">
                  {t("user.account.apps.grantedAt", { date: fmt(g.granted_at) })}
                  {" · "}
                  {g.last_used_at ? t("user.account.apps.lastUsed", { date: fmt(g.last_used_at) }) : t("user.account.apps.neverUsed")}
                </Box>
              </div>
              <Button onClick={() => setRevoking(g)}>{t("user.account.apps.revoke")}</Button>
            </li>
          ))}
        </ul>
      )}

      <Modal
        visible={revoking !== null}
        onDismiss={() => setRevoking(null)}
        header={t("user.account.apps.revokeTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setRevoking(null)}>{t("common.cancel")}</Button>
              <Button variant="primary" loading={busy} onClick={() => void revoke()}>{t("user.account.apps.revoke")}</Button>
            </SpaceBetween>
          </Box>
        }
      >
        <div className="connect-copy">{revoking && t("user.account.apps.revokeBody", { app: revoking.name })}</div>
      </Modal>
    </Container>
  );
}
