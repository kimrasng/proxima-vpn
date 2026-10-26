import { useTranslation } from "react-i18next";
import { Box } from "@cloudscape-design/components";
import { CodeBlock, type CopyLabels } from "./OAuthCodeBlock";
import { RESPONSE_GROUPS, STATUS_TEXT, type ResponseCase } from "./oauthExamples";
import "./oauthDocsExtras.css";

function render(c: ResponseCase): string {
  if (c.raw) return c.raw;
  const lines = [`HTTP/1.1 ${c.status} ${STATUS_TEXT[c.status ?? 200] ?? ""}`.trim(), ...(c.headers ?? [])];
  return c.body === undefined ? lines.join("\n") : `${lines.join("\n")}\n\n${c.body}`;
}

export function OAuthResponses({ copy }: { copy: CopyLabels }) {
  const { t } = useTranslation();
  return (
    <div style={{ display: "grid", gap: 16 }}>
      {RESPONSE_GROUPS.map((group) => (
        <section key={group.key} className="oauth-resp" aria-labelledby={`oauth-resp-${group.key}`}>
          <div className="oauth-resp__head">
            <Box variant="h3"><span id={`oauth-resp-${group.key}`}>{t(group.titleKey)}</span></Box>
            <div className="oauth-resp__meta">
              <span className={`oauth-apps__method oauth-apps__method--${group.method.toLowerCase()}`}>{group.method}</span>
              <code className="oauth-apps__ep-code">{group.path}</code>
              {group.scope && <span className="oauth-apps__chip">{group.scope}</span>}
            </div>
          </div>
          {group.noteKey && <p className="oauth-docs__note">{t(group.noteKey)}</p>}
          {group.cases.map((c) => {
            const summary = c.raw ? t("admin.oauthDocs.responses.redirect") : String(c.status);
            return (
              <div key={c.id} className="oauth-resp__case">
                <div className="oauth-resp__label">
                  <span className={`oauth-resp__badge oauth-resp__badge--${c.ok ? "ok" : "err"}`}>
                    {c.ok ? t("admin.oauthDocs.responses.success") : t("admin.oauthDocs.responses.failure")} · {summary}{c.code ? ` · ${c.code}` : ""}
                  </span>
                  <span>{t(`admin.oauthDocs.responses.when.${c.when ?? c.id}`)}</span>
                </div>
                <CodeBlock code={render(c)} {...copy} />
              </div>
            );
          })}
        </section>
      ))}
    </div>
  );
}
