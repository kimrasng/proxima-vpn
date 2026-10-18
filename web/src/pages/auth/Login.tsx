import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  Container,
  Flashbar,
  Form,
  FormField,
  Header,
  Icon,
  Input,
  SpaceBetween,
} from "@cloudscape-design/components";
import type { FlashbarProps } from "@cloudscape-design/components";
import { userLogin, setToken } from "../../api/auth";
import { ApiError } from "../../api/client";
import PortalHighlights from "../../components/PortalHighlights";
import AuthLink from "../../components/AuthLink";
import PasskeyLoginButton from "../../components/PasskeyLoginButton";

export default function Login() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [passkeyLoading, setPasskeyLoading] = useState(false);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [showErrors, setShowErrors] = useState(false);
  const required = t("auth.validation.required", { defaultValue: "This field is required." });
  const errors = {
    email: email.trim() ? undefined : required,
    password: password.trim() ? undefined : required,
  };

  const handleSubmit = async () => {
    setShowErrors(true);
    setFlash([]);
    if (errors.email || errors.password) return;
    setLoading(true);

    try {
      const res = await userLogin({ email, password });
      setToken("user", res.token);
      navigate("/portal/dashboard", { replace: true });
    } catch (err) {
      const body = err instanceof ApiError ? (err.body as { error?: string } | undefined) : undefined;
      setFlash([
        {
          type: "error",
          content: body?.error || t("auth.login.error"),
          dismissible: true,
          onDismiss: () => setFlash([]),
        },
      ]);
    } finally {
      setLoading(false);
    }
  };

  return (
    <SpaceBetween size="l">
      <Box textAlign="center">
        <SpaceBetween size="xxs" alignItems="center">
          <span className="auth-layout__brand">
            <Icon name="lock-private" size="medium" />
            <Box variant="div" fontSize="heading-xl" fontWeight="bold" padding="n">
              {t("auth.portal.name")}
            </Box>
          </span>
          <Box color="text-body-secondary">{t("auth.portal.tagline")}</Box>
        </SpaceBetween>
      </Box>

      <SpaceBetween size="l">
        {flash.length > 0 && <Flashbar items={flash} i18nStrings={{ ariaLabel: t("admin.notifications.title") }} />}
        <div className="auth-layout__card">
        <Container header={<Header variant="h1">{t("auth.login.title")}</Header>}>
          <Box padding={{ vertical: "l" }}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void handleSubmit();
            }}
          >
            <Form>
              <SpaceBetween size="xl">
                <FormField label={t("auth.login.email")} errorText={showErrors ? errors.email : undefined}>
                  <Input
                    value={email}
                    onChange={({ detail }) => setEmail(detail.value)}
                    type="email"
                    placeholder="name@example.com"
                    autoComplete="email"
                    autoFocus
                  />
                </FormField>
                <div className="auth-layout__field-with-link">
                  <FormField label={t("auth.login.password")} errorText={showErrors ? errors.password : undefined}>
                    <Input
                      value={password}
                      onChange={({ detail }) => setPassword(detail.value)}
                      type="password"
                      autoComplete="current-password"
                    />
                  </FormField>
                  {/* Outside the <label> so it does not become part of the input's accessible name. */}
                  <div className="auth-layout__field-link">
                    <AuthLink to="/forgot-password">{t("auth.login.forgotPassword")}</AuthLink>
                  </div>
                </div>
                <Button variant="primary" loading={loading} disabled={passkeyLoading} formAction="submit" fullWidth>
                  {t("auth.login.submit")}
                </Button>
                <PasskeyLoginButton disabled={passkeyLoading || loading} onMessage={(message) => setFlash([{ ...message, id: "passkey-message", onDismiss: () => setFlash([]) }])} onLoadingChange={setPasskeyLoading} />
              </SpaceBetween>
            </Form>
          </form>
          </Box>
          <Box textAlign="center" margin={{ top: "l" }}>
            <AuthLink to="/register">{t("auth.login.registerLink")}</AuthLink>
          </Box>
        </Container>
        </div>
      </SpaceBetween>

      <PortalHighlights />
    </SpaceBetween>
  );
}
