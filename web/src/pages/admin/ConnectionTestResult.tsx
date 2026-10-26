import { useTranslation } from "react-i18next";
import { Box, StatusIndicator } from "@cloudscape-design/components";
import type { ConnectionResult } from "../../api/admin";
import "./connectionTest.css";

type Props = { kind: "s3" | "telegram"; result: ConnectionResult | null; error: string | null };

export default function ConnectionTestResult({ kind, result, error }: Props) {
  const { t } = useTranslation();
  if (!result && !error) return null;
  if (error) {
    return (
      <div className="conn-test conn-test--fail" role="status">
        <StatusIndicator type="error">{error}</StatusIndicator>
      </div>
    );
  }
  if (!result) return null;
  return (
    <div className={`conn-test conn-test--${result.ok ? "ok" : "fail"}`} role="status">
      <div className="conn-test__head">
        <StatusIndicator type={result.ok ? "success" : "error"}>
          {result.ok ? t(`admin.settings.test.${kind}.ok`) : t(`admin.settings.test.${kind}.fail`)}
        </StatusIndicator>
        <Box color="text-body-secondary" fontSize="body-s">{t("admin.settings.test.elapsed", { ms: result.elapsed_ms })}</Box>
      </div>
      <ol className="conn-test__steps">
        {result.checks.map((c) => (
          <li key={c.step} className={c.ok ? "conn-test__step--ok" : "conn-test__step--fail"}>
            <span className="conn-test__mark" aria-hidden="true">{c.ok ? "✓" : "✕"}</span>
            <span className="conn-test__label">{t(`admin.settings.test.step.${c.step}`)}</span>
            {c.detail && <code className="conn-test__detail">{c.detail}</code>}
          </li>
        ))}
      </ol>
    </div>
  );
}
