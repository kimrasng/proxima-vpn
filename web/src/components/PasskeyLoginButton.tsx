import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Box, Button } from "@cloudscape-design/components";
import { browserSupportsWebAuthn, startAuthentication, WebAuthnError } from "@simplewebauthn/browser";
import { ApiError } from "../api/client";
import { beginPasskeyLogin, finishPasskeyLogin } from "../api/passkey";
import { setToken } from "../api/auth";
import { consumePostLogin } from "../utils/postLogin";
import type { FlashbarProps } from "@cloudscape-design/components";

type Props = { onMessage: (message: FlashbarProps.MessageDefinition) => void; onLoadingChange: (loading: boolean) => void; disabled?: boolean };

export default function PasskeyLoginButton({ onMessage, onLoadingChange, disabled = false }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  if (!browserSupportsWebAuthn()) return null;

  const login = async () => {
    setLoading(true);
    onLoadingChange(true);
    try {
      const begin = await beginPasskeyLogin();
      const credential = await startAuthentication({ optionsJSON: begin.options });
      const result = await finishPasskeyLogin(begin.session_id, credential);
      setToken("user", result.token);
      navigate(consumePostLogin("/portal/dashboard"), { replace: true });
    } catch (error) {
      const cancelled = error instanceof WebAuthnError && ["ERROR_CEREMONY_ABORTED", "ERROR_NOT_ALLOWED"].includes(error.code as string);
      if (cancelled || (error instanceof Error && error.name === "NotAllowedError")) {
        onMessage({ type: "info", content: t("auth.passkey.cancelled"), dismissible: true, dismissLabel: t("common.close") });
      } else {
        const body = error instanceof ApiError ? error.body as { error?: string } | undefined : undefined;
        onMessage({ type: "error", content: body?.error || t("auth.passkey.loginError"), dismissible: true, dismissLabel: t("common.close") });
      }
    } finally { setLoading(false); onLoadingChange(false); }
  };

  return <div className="passkey-login">
    <Box textAlign="center" color="text-body-secondary">{t("auth.passkey.or")}</Box>
    <Button fullWidth formAction="none" variant="normal" iconName="lock-private" loading={loading} disabled={disabled} onClick={() => void login()}>
      {t("auth.passkey.loginButton")}
    </Button>
  </div>;
}
