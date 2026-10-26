import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Box,
  Button,
  Container,
  ContentLayout,
  Header,
  SpaceBetween,
} from "@cloudscape-design/components";
import { OAUTH_ENDPOINT_GROUPS, breakAtSlashes } from "./oauthEndpoints";
import { CodeBlock } from "./OAuthCodeBlock";
import { OAuthResponses } from "./OAuthResponses";
import { OAuthUrlBuilder } from "./OAuthUrlBuilder";
import "./oauthApps.css";

const AUTHORIZE_PARAMS: { name: string; required: boolean }[] = [
  { name: "response_type", required: true },
  { name: "client_id", required: true },
  { name: "redirect_uri", required: true },
  { name: "scope", required: false },
  { name: "state", required: true },
  { name: "code_challenge", required: false },
  { name: "code_challenge_method", required: false },
];

const ERROR_CODES = [
  "invalid_request",
  "invalid_client",
  "invalid_grant",
  "invalid_scope",
  "unsupported_grant_type",
  "access_denied",
  "invalid_token",
  "insufficient_scope",
  "slow_down",
] as const;

const SCOPES = [
  "profile",
  "subscription:read",
  "nodes:read",
  "plan:read",
  "reset_tickets:read",
  "reset_tickets:redeem",
] as const;

export default function OAuthDocs() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const origin = window.location.origin;
  const copy = { copyLabel: t("admin.oauthDocs.copy"), copied: t("admin.oauthApps.copied"), copyFailed: t("admin.oauthApps.copyFailed") };

  const authorizeUrl = `${origin}/oauth/authorize
  ?response_type=code
  &client_id=pxc_xxxxxxxx
  &redirect_uri=proxima%3A%2F%2Foauth%2Fcallback
  &scope=profile%20subscription%3Aread%20plan%3Aread
  &state=RANDOM_STATE
  &code_challenge=BASE64URL_SHA256_OF_VERIFIER
  &code_challenge_method=S256`;
  const pkce = `// code_verifier: 43-128 random URL-safe characters
const verifier = base64url(randomBytes(32));
// code_challenge = BASE64URL(SHA256(verifier)), no padding
const challenge = base64url(sha256(verifier));`;
  const tokenPublic = `curl -X POST ${origin}/api/v1/oauth/token \\
  -d grant_type=authorization_code \\
  -d code=AUTHORIZATION_CODE \\
  -d redirect_uri=proxima://oauth/callback \\
  -d client_id=pxc_xxxxxxxx \\
  -d code_verifier=ORIGINAL_VERIFIER`;
  const tokenConfidential = `curl -X POST ${origin}/api/v1/oauth/token \\
  -u "pxc_xxxxxxxx:pxs_xxxxxxxx" \\
  -d grant_type=authorization_code \\
  -d code=AUTHORIZATION_CODE \\
  -d redirect_uri=https://app.example.com/callback \\
  -d code_verifier=ORIGINAL_VERIFIER`;
  const tokenResponse = `{
  "access_token": "pxat_...",
  "token_type": "Bearer",
  "expires_in": 3600,
  "refresh_token": "pxrt_...",
  "scope": "profile subscription:read plan:read"
}`;
  const refresh = `curl -X POST ${origin}/api/v1/oauth/token \\
  -d grant_type=refresh_token \\
  -d refresh_token=pxrt_... \\
  -d client_id=pxc_xxxxxxxx`;
  const revoke = `curl -X POST ${origin}/api/v1/oauth/revoke \\
  -d token=pxrt_... \\
  -d client_id=pxc_xxxxxxxx`;
  const apiCall = `curl ${origin}/api/v1/client/subscription \\
  -H "Authorization: Bearer pxat_..."`;
  const apiResponse = `{
  "available": true,
  "url": "https://sub.example.com/sub/XXXXXXXX",
  "domains": [
    { "domain": "sub.example.com", "is_default": true, "url": "https://sub.example.com/sub/XXXXXXXX" }
  ]
}`;
  const errorResponse = `HTTP/1.1 403 Forbidden
WWW-Authenticate: Bearer error="insufficient_scope", scope="nodes:read"

{ "error": "insufficient_scope", "error_description": "this token lacks the nodes:read scope", "scope": "nodes:read" }`;

  const sections = ["overview", "register", "authorize", "token", "refresh", "api", "responses", "errors", "security"] as const;

  return (
    <ContentLayout
      header={
        <Header variant="h1" description={t("admin.oauthDocs.description")}
          actions={<Button iconName="arrow-left" onClick={() => navigate("/admin/oauth-apps")}>{t("admin.oauthDocs.back")}</Button>}>
          {t("admin.oauthDocs.title")}
        </Header>
      }
    >
      <div className="oauth-docs">
        <SpaceBetween size="l">
          <Container>
            <nav className="oauth-docs__toc" aria-label={t("admin.oauthDocs.toc")}>
              {sections.map((s, i) => (
                <a key={s} href={`#oauth-${s}`}>{i + 1}. {t(`admin.oauthDocs.${s}.title`)}</a>
              ))}
            </nav>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-overview">1. {t("admin.oauthDocs.overview.title")}</span></Header>}>
            <SpaceBetween size="m">
              <p>{t("admin.oauthDocs.overview.body")}</p>
              <ol className="oauth-docs__flow">
                {[1, 2, 3, 4, 5].map((n) => (
                  <li key={n}>
                    <strong>{t(`admin.oauthDocs.overview.step${n}Title`)}</strong>
                    <span>{t(`admin.oauthDocs.overview.step${n}Body`)}</span>
                  </li>
                ))}
              </ol>
              <div className="oauth-docs__facts">
                {["code", "access", "refresh"].map((k) => (
                  <div key={k} className="oauth-docs__fact">
                    <Box variant="awsui-key-label">{t(`admin.oauthDocs.overview.fact.${k}Label`)}</Box>
                    <div>{t(`admin.oauthDocs.overview.fact.${k}Value`)}</div>
                  </div>
                ))}
              </div>
            </SpaceBetween>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-register">2. {t("admin.oauthDocs.register.title")}</span></Header>}>
            <SpaceBetween size="m">
              <p>{t("admin.oauthDocs.register.body")}</p>
              <div className="oauth-docs__compare">
                {["confidential", "public"].map((k) => (
                  <div key={k} className="oauth-docs__card">
                    <Box variant="h4">{t(`admin.oauthApps.type.${k}`)}</Box>
                    <p>{t(`admin.oauthDocs.register.${k}`)}</p>
                  </div>
                ))}
              </div>
              <Box variant="h4">{t("admin.oauthDocs.register.redirectTitle")}</Box>
              <ul className="oauth-docs__list">
                {[1, 2, 3, 4].map((n) => <li key={n}>{t(`admin.oauthDocs.register.redirect${n}`)}</li>)}
              </ul>
              <Box variant="h4">{t("admin.oauthDocs.register.scopesTitle")}</Box>
              <div className="oauth-docs__table-wrap">
                <table className="oauth-docs__table">
                  <thead><tr><th>{t("admin.oauthDocs.col.scope")}</th><th>{t("admin.oauthDocs.col.desc")}</th></tr></thead>
                  <tbody>
                    {SCOPES.map((s) => (
                      <tr key={s}>
                        <td><code>{s}</code></td>
                        <td>
                          <strong>{t(`auth.oauth.scope.${s.replace(":", "_")}.title`)}</strong>
                          <div className="oauth-docs__muted">{t(`auth.oauth.scope.${s.replace(":", "_")}.desc`)}</div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </SpaceBetween>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-authorize">3. {t("admin.oauthDocs.authorize.title")}</span></Header>}>
            <SpaceBetween size="m">
              <p>{t("admin.oauthDocs.authorize.body")}</p>
              <CodeBlock code={authorizeUrl} {...copy} />
              <Box variant="h4">{t("admin.oauthDocs.builder.title")}</Box>
              <OAuthUrlBuilder origin={origin} copy={copy} />
              <div className="oauth-docs__table-wrap">
                <table className="oauth-docs__table">
                  <thead><tr><th>{t("admin.oauthDocs.col.param")}</th><th>{t("admin.oauthDocs.col.required")}</th><th>{t("admin.oauthDocs.col.desc")}</th></tr></thead>
                  <tbody>
                    {AUTHORIZE_PARAMS.map((p) => (
                      <tr key={p.name}>
                        <td><code>{p.name}</code></td>
                        <td className="oauth-docs__nowrap">{p.required ? t("admin.oauthDocs.required.yes") : t("admin.oauthDocs.required.no")}</td>
                        <td>{t(`admin.oauthDocs.param.${p.name}`)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <Box variant="h4">{t("admin.oauthDocs.authorize.pkceTitle")}</Box>
              <p>{t("admin.oauthDocs.authorize.pkceBody")}</p>
              <CodeBlock code={pkce} {...copy} />
              <Box variant="h4">{t("admin.oauthDocs.authorize.callbackTitle")}</Box>
              <p>{t("admin.oauthDocs.authorize.callbackBody")}</p>
              <CodeBlock code={`proxima://oauth/callback?code=AUTHORIZATION_CODE&state=RANDOM_STATE\nproxima://oauth/callback?error=access_denied&state=RANDOM_STATE`} {...copy} />
            </SpaceBetween>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-token">4. {t("admin.oauthDocs.token.title")}</span></Header>}>
            <SpaceBetween size="m">
              <p>{t("admin.oauthDocs.token.body")}</p>
              <Box variant="h4">{t("admin.oauthDocs.token.publicTitle")}</Box>
              <CodeBlock code={tokenPublic} {...copy} />
              <Box variant="h4">{t("admin.oauthDocs.token.confidentialTitle")}</Box>
              <CodeBlock code={tokenConfidential} {...copy} />
              <Box variant="h4">{t("admin.oauthDocs.token.responseTitle")}</Box>
              <CodeBlock code={tokenResponse} {...copy} />
              <p className="oauth-docs__note">{t("admin.oauthDocs.token.note")}</p>
            </SpaceBetween>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-refresh">5. {t("admin.oauthDocs.refresh.title")}</span></Header>}>
            <SpaceBetween size="m">
              <p>{t("admin.oauthDocs.refresh.body")}</p>
              <CodeBlock code={refresh} {...copy} />
              <p className="oauth-docs__note">{t("admin.oauthDocs.refresh.note")}</p>
              <Box variant="h4">{t("admin.oauthDocs.refresh.revokeTitle")}</Box>
              <p>{t("admin.oauthDocs.refresh.revokeBody")}</p>
              <CodeBlock code={revoke} {...copy} />
            </SpaceBetween>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-api">6. {t("admin.oauthDocs.api.title")}</span></Header>}>
            <SpaceBetween size="m">
              <p>{t("admin.oauthDocs.api.body")}</p>
              <div className="oauth-apps__ep-list">
                {OAUTH_ENDPOINT_GROUPS.find((g) => g.key === "client")!.items.map((ep) => (
                  <div key={ep.path} className="oauth-apps__ep">
                    <div className="oauth-apps__ep-head">
                      <span className="oauth-apps__ep-label">{t(`admin.oauthApps.endpoint.${ep.key}`)}</span>
                      {ep.scope && <span className="oauth-apps__chip">{ep.scope}</span>}
                    </div>
                    <div className="oauth-apps__ep-url">
                      <span className={`oauth-apps__method oauth-apps__method--${ep.method.toLowerCase()}`}>{ep.method}</span>
                      <code className="oauth-apps__ep-code">{breakAtSlashes(`${origin}${ep.path}`)}</code>
                    </div>
                    <div className="oauth-docs__muted">{t(`admin.oauthDocs.api.ep.${ep.key}`)}</div>
                  </div>
                ))}
              </div>
              <Box variant="h4">{t("admin.oauthDocs.api.exampleTitle")}</Box>
              <CodeBlock code={apiCall} {...copy} />
              <CodeBlock code={apiResponse} {...copy} />
            </SpaceBetween>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-responses">7. {t("admin.oauthDocs.responses.title")}</span></Header>}>
            <SpaceBetween size="m">
              <p>{t("admin.oauthDocs.responses.body")}</p>
              <OAuthResponses copy={copy} />
            </SpaceBetween>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-errors">8. {t("admin.oauthDocs.errors.title")}</span></Header>}>
            <SpaceBetween size="m">
              <p>{t("admin.oauthDocs.errors.body")}</p>
              <div className="oauth-docs__table-wrap">
                <table className="oauth-docs__table">
                  <thead><tr><th>{t("admin.oauthDocs.col.code")}</th><th>{t("admin.oauthDocs.col.meaning")}</th></tr></thead>
                  <tbody>
                    {ERROR_CODES.map((c) => (
                      <tr key={c}><td><code>{c}</code></td><td>{t(`admin.oauthDocs.error.${c}`)}</td></tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <CodeBlock code={errorResponse} {...copy} />
            </SpaceBetween>
          </Container>

          <Container header={<Header variant="h2"><span id="oauth-security">9. {t("admin.oauthDocs.security.title")}</span></Header>}>
            <ul className="oauth-docs__list">
              {[1, 2, 3, 4, 5, 6].map((n) => <li key={n}>{t(`admin.oauthDocs.security.item${n}`)}</li>)}
            </ul>
          </Container>
        </SpaceBetween>
      </div>
    </ContentLayout>
  );
}
