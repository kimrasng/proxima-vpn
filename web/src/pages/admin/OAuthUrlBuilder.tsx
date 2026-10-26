import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Checkbox, FormField, Input, SpaceBetween, Toggle } from "@cloudscape-design/components";
import { OAUTH_SCOPES, scopeKey } from "../../api/oauth";
import { CodeBlock, type CopyLabels } from "./OAuthCodeBlock";
import "./oauthDocsExtras.css";

const DEFAULT_SCOPES: readonly string[] = ["profile", "subscription:read", "plan:read"];

function base64url(bytes: Uint8Array): string {
  let binary = "";
  bytes.forEach((b) => { binary += String.fromCharCode(b); });
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function OAuthUrlBuilder({ origin, copy }: { origin: string; copy: CopyLabels }) {
  const { t } = useTranslation();
  const [clientId, setClientId] = useState("pxc_xxxxxxxx");
  const [redirectUri, setRedirectUri] = useState("proxima://oauth/callback");
  const [scopes, setScopes] = useState<readonly string[]>(DEFAULT_SCOPES);
  const [state, setState] = useState("RANDOM_STATE");
  const [usePkce, setUsePkce] = useState(true);
  const [challenge, setChallenge] = useState("BASE64URL_SHA256_OF_VERIFIER");
  const [verifier, setVerifier] = useState("");
  const canHash = typeof crypto !== "undefined" && !!crypto.subtle;

  const generate = async () => {
    const v = base64url(crypto.getRandomValues(new Uint8Array(32)));
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(v));
    setVerifier(v);
    setChallenge(base64url(new Uint8Array(digest)));
    setState(base64url(crypto.getRandomValues(new Uint8Array(16))));
  };

  const params: [string, string][] = [["response_type", "code"]];
  if (clientId) params.push(["client_id", clientId]);
  if (redirectUri) params.push(["redirect_uri", redirectUri]);
  const chosen = OAUTH_SCOPES.filter((s) => scopes.includes(s));
  if (chosen.length > 0) params.push(["scope", chosen.join(" ")]);
  if (state) params.push(["state", state]);
  if (usePkce && challenge) params.push(["code_challenge", challenge], ["code_challenge_method", "S256"]);

  const pairs = params.map(([k, v]) => `${k}=${encodeURIComponent(v)}`);
  const base = `${origin}/oauth/authorize`;
  const shown = `${base}\n` + pairs.map((p, i) => `  ${i === 0 ? "?" : "&"}${p}`).join("\n");
  const oneLine = `${base}?${pairs.join("&")}`;

  return (
    <div className="oauth-builder">
      <p className="oauth-docs__muted" style={{ margin: 0 }}>{t("admin.oauthDocs.builder.body")}</p>
      <div className="oauth-builder__fields">
        <FormField label="client_id" errorText={clientId ? undefined : t("admin.oauthDocs.builder.required")}>
          <Input value={clientId} onChange={({ detail }) => setClientId(detail.value)} />
        </FormField>
        <FormField label="redirect_uri" description={t("admin.oauthDocs.builder.redirectHint")}
          errorText={redirectUri ? undefined : t("admin.oauthDocs.builder.required")}>
          <Input value={redirectUri} onChange={({ detail }) => setRedirectUri(detail.value)} />
        </FormField>
      </div>
      <FormField label="scope" description={chosen.length === 0 ? t("admin.oauthDocs.builder.scopeEmpty") : t("admin.oauthDocs.builder.scopeHint")}>
        <div className="oauth-builder__scopes">
          {OAUTH_SCOPES.map((s) => (
            <Checkbox key={s} checked={scopes.includes(s)}
              description={t(`auth.oauth.scope.${scopeKey(s)}.title`)}
              onChange={({ detail }) => setScopes((prev) => (detail.checked ? [...prev, s] : prev.filter((x) => x !== s)))}>
              <code>{s}</code>
            </Checkbox>
          ))}
        </div>
      </FormField>
      <FormField label="state" description={t("admin.oauthDocs.builder.stateHint")}>
        <Input value={state} onChange={({ detail }) => setState(detail.value)} />
      </FormField>
      <SpaceBetween size="s">
        <Toggle checked={usePkce} onChange={({ detail }) => setUsePkce(detail.checked)}>{t("admin.oauthDocs.builder.pkce")}</Toggle>
        {usePkce && (
          <FormField label="code_challenge" description={t("admin.oauthDocs.builder.challengeHint")}>
            <Input value={challenge} onChange={({ detail }) => setChallenge(detail.value)} />
          </FormField>
        )}
        <div className="oauth-builder__actions">
          <Button iconName="refresh" disabled={!canHash} onClick={() => void generate()}>{t("admin.oauthDocs.builder.generate")}</Button>
        </div>
        {!canHash && <p className="oauth-docs__note" style={{ margin: 0 }}>{t("admin.oauthDocs.builder.insecure")}</p>}
        {verifier && (
          <FormField label="code_verifier" description={t("admin.oauthDocs.builder.verifierHint")}>
            <CodeBlock code={verifier} {...copy} />
          </FormField>
        )}
      </SpaceBetween>
      <CodeBlock code={shown} copyText={oneLine} {...copy} />
      <div className="oauth-builder__actions">
        <Button iconName="external" iconAlign="right" href={oneLine} target="_blank" disabled={!clientId || !redirectUri}>
          {t("admin.oauthDocs.builder.open")}
        </Button>
      </div>
    </div>
  );
}
