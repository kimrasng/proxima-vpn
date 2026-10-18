import { useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  Container,
  Flashbar,
  Form,
  FormField,
  Header,
  Input,
  SpaceBetween,
} from "@cloudscape-design/components";
import type { FlashbarProps } from "@cloudscape-design/components";
import { resetPassword } from "../../api/auth";
import { ApiError } from "../../api/client";
import AuthLink from "../../components/AuthLink";

export default function ResetPassword() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  // Read once: the token is removed from the address bar below so it does not
  // linger in history, screenshots or a Referer header.
  const tokenRef = useRef(searchParams.get("token") ?? "");
  const token = tokenRef.current;

  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [loading, setLoading] = useState(false);
  const [done, setDone] = useState(false);
  const [linkInvalid, setLinkInvalid] = useState(token === "");
  const [showErrors, setShowErrors] = useState(false);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const redirectTimer = useRef<number | undefined>(undefined);

  useEffect(() => {
    if (token) window.history.replaceState(null, "", window.location.pathname);
    return () => window.clearTimeout(redirectTimer.current);
  }, [token]);

  const required = t("auth.validation.required", { defaultValue: "This field is required." });
  // Mirrors the server: at least 8 characters, and bcrypt's 72-byte ceiling.
  const passwordError = !password
    ? required
    : [...password].length < 8
      ? t("auth.reset.tooShort")
      : new TextEncoder().encode(password).length > 72
        ? t("auth.reset.tooLong")
        : undefined;
  const errors = {
    password: passwordError,
    confirm: !confirm ? required : confirm !== password ? t("auth.reset.mismatch") : undefined,
  };

  const showError = (content: string) =>
    setFlash([{ type: "error", content, dismissible: true, onDismiss: () => setFlash([]) }]);

  const handleSubmit = async () => {
    setShowErrors(true);
    setFlash([]);
    if (errors.password || errors.confirm) return;
    setLoading(true);
    try {
      await resetPassword({ token, password });
      setDone(true);
      redirectTimer.current = window.setTimeout(() => navigate("/login", { replace: true }), 2000);
    } catch (err) {
      const body = err instanceof ApiError ? (err.body as { error?: string } | undefined) : undefined;
      if (err instanceof ApiError && err.status === 400 && /token/i.test(body?.error ?? "")) {
        setLinkInvalid(true);
      } else if (err instanceof ApiError && err.status === 400) {
        // Password policy errors come from the server so the rule lives in one place.
        showError(body?.error || t("auth.reset.error"));
      } else if (err instanceof ApiError && err.status === 429) {
        showError(t("auth.reset.tooMany"));
      } else {
        showError(t("auth.reset.error"));
      }
    } finally {
      setLoading(false);
    }
  };

  if (linkInvalid) {
    return (
      <Container header={<Header variant="h1">{t("auth.reset.invalidTitle")}</Header>}>
        <SpaceBetween size="m">
          <Alert type="error" statusIconAriaLabel={t("auth.reset.invalidTitle")}>
            {t("auth.reset.invalidBody")}
          </Alert>
          <Button variant="primary" fullWidth onClick={() => navigate("/forgot-password")}>
            {t("auth.reset.requestNew")}
          </Button>
          <Box textAlign="center">
            <AuthLink to="/login">{t("auth.forgot.backToLogin")}</AuthLink>
          </Box>
        </SpaceBetween>
      </Container>
    );
  }

  return (
    <SpaceBetween size="l">
      {(done || flash.length > 0) && (
        <Flashbar
          items={
            done
              ? [{ type: "success", content: t("auth.reset.success"), statusIconAriaLabel: "success" }]
              : flash
          }
        />
      )}
      <Container header={<Header variant="h1">{t("auth.reset.title")}</Header>}>
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            void handleSubmit();
          }}
        >
          <Form>
            <SpaceBetween size="m">
              <FormField label={t("auth.reset.newPassword")} errorText={showErrors ? errors.password : undefined}>
                <Input
                  value={password}
                  onChange={({ detail }) => setPassword(detail.value)}
                  type="password"
                  autoComplete="new-password"
                  autoFocus
                />
              </FormField>
              <FormField label={t("auth.reset.confirmPassword")} errorText={showErrors ? errors.confirm : undefined}>
                <Input
                  value={confirm}
                  onChange={({ detail }) => setConfirm(detail.value)}
                  type="password"
                  autoComplete="new-password"
                />
              </FormField>
              <Button variant="primary" loading={loading} disabled={done} formAction="submit" fullWidth>
                {t("auth.reset.submit")}
              </Button>
            </SpaceBetween>
          </Form>
        </form>
        <Box textAlign="center" margin={{ top: "l" }}>
          <AuthLink to="/login">{t("auth.forgot.backToLogin")}</AuthLink>
        </Box>
      </Container>
    </SpaceBetween>
  );
}
