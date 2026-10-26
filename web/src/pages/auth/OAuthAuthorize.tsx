import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Box, Button, Icon, Spinner } from "@cloudscape-design/components";
import { isAuthenticated, removeToken } from "../../api/auth";
import { ApiError } from "../../api/client";
import {
  decideOAuthAuthorize,
  getOAuthAuthorize,
  scopeKey,
  type OAuthAuthorizeError,
  type OAuthAuthorizeInfo,
} from "../../api/oauth";
import { getProfile } from "../../api/user";
import { loginUrl, rememberPostLogin } from "../../utils/postLogin";
import "./oauthAuthorize.css";

const PARAMS = ["response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method"] as const;
const KNOWN_ERRORS = ["invalid_client", "invalid_request", "invalid_scope", "unsupported_response_type", "server_error"];

type Failure = { code: string; description?: string; returnTo?: string };

type View =
  | { kind: "loading" }
  | { kind: "consent"; info: OAuthAuthorizeInfo }
  | { kind: "redirecting"; to: string; approved: boolean }
  | { kind: "error"; failure: Failure };

// Web callbacks show only the origin; app schemes (proxima://ready/promocode) have no
// meaningful host, so the whole URI is shown without its query.
function redirectLabel(uri: string): string {
  try {
    const u = new URL(uri);
    if (u.protocol === "https:" || u.protocol === "http:") return u.origin;
    return uri.split("?")[0] ?? uri;
  } catch {
    return uri;
  }
}

function initialOf(name: string): string {
  return (name.trim()[0] ?? "?").toUpperCase();
}

function failureFrom(err: unknown): Failure {
  const body = err instanceof ApiError ? (err.body as OAuthAuthorizeError | undefined) : undefined;
  return { code: body?.error ?? "server_error", description: body?.error_description, returnTo: body?.redirect_to };
}

export default function OAuthAuthorize() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [view, setView] = useState<View>({ kind: "loading" });
  const [busy, setBusy] = useState<"approve" | "deny" | null>(null);
  const [account, setAccount] = useState<{ name: string; email: string } | null>(null);
  const here = location.pathname + location.search;

  const params = useMemo(() => {
    const q = new URLSearchParams(location.search);
    const out: Record<string, string> = {};
    for (const key of PARAMS) {
      const value = q.get(key);
      if (value !== null) out[key] = value;
    }
    return out;
  }, [location.search]);

  useEffect(() => {
    // Kept as a fallback for sign-in paths that cannot carry ?continue=.
    rememberPostLogin(here);
    if (!isAuthenticated("user")) {
      navigate(loginUrl(here), { replace: true });
      return;
    }
    let active = true;
    getProfile()
      .then((profile) => { if (active) setAccount({ name: profile.name, email: profile.email }); })
      .catch(() => { /* The consent request below reports the real failure. */ });
    getOAuthAuthorize(location.search)
      .then((info) => { if (active) setView({ kind: "consent", info }); })
      .catch((err: unknown) => {
        // A 401 is already handled by the shared client, which goes to /login?continue=.
        if (!active || (err instanceof ApiError && err.status === 401)) return;
        setView({ kind: "error", failure: failureFrom(err) });
      });
    return () => {
      active = false;
    };
  }, [here, location.search, navigate]);

  const goBack = (to: string, approved: boolean) => {
    setView({ kind: "redirecting", to, approved });
    window.location.assign(to);
  };

  const decide = async (approve: boolean) => {
    setBusy(approve ? "approve" : "deny");
    try {
      const { redirect_to } = await decideOAuthAuthorize(params, approve);
      goBack(redirect_to, approve);
    } catch (err) {
      setView({ kind: "error", failure: failureFrom(err) });
    } finally {
      setBusy(null);
    }
  };

  const switchAccount = () => {
    removeToken("user");
    navigate(loginUrl(here), { replace: true });
  };

  return (
    <div className="oauth-consent connect-copy">
      <div className="auth-layout__card oauth-consent__card">
        {view.kind === "loading" && (
          <Box textAlign="center" padding="xl">
            <Spinner size="large" />
          </Box>
        )}

        {view.kind === "error" && (
          <div className="oauth-consent__stack">
            <div className="oauth-consent__badge oauth-consent__badge--error">
              <Icon name="status-warning" size="big" />
            </div>
            <h1 className="oauth-consent__title">{t("auth.oauth.errorTitle")}</h1>
            <p className="oauth-consent__lead">
              {t(`auth.oauth.error.${KNOWN_ERRORS.includes(view.failure.code) ? view.failure.code : "other"}`)}
            </p>
            <div className="oauth-consent__error-detail">
              <span className="oauth-consent__error-label">{t("auth.oauth.errorDetail")}</span>
              <code>{view.failure.code}</code>
              {view.failure.description && <span className="oauth-consent__error-desc">{view.failure.description}</span>}
            </div>
            <p className="oauth-consent__hint">
              {view.failure.returnTo ? t("auth.oauth.errorHintReturn") : t("auth.oauth.errorHint")}
            </p>
            <div className="oauth-consent__actions oauth-consent__actions--single">
              {view.failure.returnTo ? (
                <Button variant="primary" fullWidth onClick={() => goBack(view.failure.returnTo as string, false)}>{t("auth.oauth.backToApp")}</Button>
              ) : (
                <Button variant="primary" fullWidth onClick={() => navigate("/portal/dashboard")}>{t("auth.oauth.toDashboard")}</Button>
              )}
            </div>
          </div>
        )}

        {view.kind === "redirecting" && (
          <div className="oauth-consent__stack oauth-consent__handoff">
            <div className={`oauth-consent__badge oauth-consent__badge--lg${view.approved ? " oauth-consent__badge--ok" : ""}`}>
              {view.approved ? <Icon name="status-positive" size="large" /> : <Spinner size="large" />}
            </div>
            <h1 className="oauth-consent__title">
              {view.approved ? t("auth.oauth.approvedTitle") : t("auth.oauth.returning")}
            </h1>
            <p className="oauth-consent__lead">{t("auth.oauth.returningHint")}</p>
            <div className="oauth-consent__actions oauth-consent__actions--single">
              <Button variant="primary" fullWidth href={view.to}>{t("auth.oauth.openApp")}</Button>
            </div>
            <p className="oauth-consent__hint">{t("auth.oauth.closeHint")}</p>
          </div>
        )}

        {view.kind === "consent" && (
          <div className="oauth-consent__stack">
            <div className="oauth-consent__apps" aria-hidden="true">
              <span className="oauth-consent__app">{initialOf(view.info.client.name)}</span>
              <span className="oauth-consent__link"><Icon name="arrow-left" /><Icon name="arrow-right" /></span>
              <span className="oauth-consent__app oauth-consent__app--proxima"><Icon name="lock-private" /></span>
            </div>
            <h1 className="oauth-consent__title">{t("auth.oauth.title", { app: view.info.client.name })}</h1>
            {view.info.client.description && <p className="oauth-consent__lead">{view.info.client.description}</p>}

            {account && (
              <div className="oauth-consent__account">
                <span className="oauth-consent__avatar">{initialOf(account.name || account.email)}</span>
                <span className="oauth-consent__account-text">
                  <span className="oauth-consent__account-label">{t("auth.oauth.signedInAs")}</span>
                  <strong className="oauth-consent__email" title={account.email}>{account.email}</strong>
                </span>
                <Button variant="inline-link" onClick={switchAccount}>{t("auth.oauth.switchAccount")}</Button>
              </div>
            )}

            <div>
              <div className="oauth-consent__section-title">{t("auth.oauth.scopesTitle")}</div>
              <ul className="oauth-consent__scopes">
                {view.info.scopes.map((scope) => (
                  <li key={scope} className={scope === "reset_tickets:redeem" ? "oauth-consent__scope--write" : undefined}>
                    <Icon name={scope === "reset_tickets:redeem" ? "edit" : "check"} />
                    <div>
                      <div className="oauth-consent__scope-title">{t(`auth.oauth.scope.${scopeKey(scope)}.title`)}</div>
                      <div className="oauth-consent__scope-desc">{t(`auth.oauth.scope.${scopeKey(scope)}.desc`)}</div>
                    </div>
                  </li>
                ))}
              </ul>
            </div>

            <div className="oauth-consent__note">
              <Icon name="security" variant="subtle" />
              <span>{t("auth.oauth.never")}</span>
            </div>

            <div className="oauth-consent__redirect">
              <span>{t("auth.oauth.redirectLabel")}</span>
              <code title={view.info.redirect_uri}>{redirectLabel(view.info.redirect_uri)}</code>
            </div>

            <div className="oauth-consent__actions">
              <Button fullWidth onClick={() => void decide(false)} loading={busy === "deny"} disabled={busy !== null}>
                {t("auth.oauth.deny")}
              </Button>
              <Button variant="primary" fullWidth onClick={() => void decide(true)} loading={busy === "approve"} disabled={busy !== null}>
                {t("auth.oauth.approve")}
              </Button>
            </div>
            <p className="oauth-consent__hint">{t("auth.oauth.revokeHint")}</p>
          </div>
        )}
      </div>
    </div>
  );
}
