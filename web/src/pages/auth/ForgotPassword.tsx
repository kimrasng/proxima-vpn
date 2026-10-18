import { useState } from "react";
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
import { forgotPassword } from "../../api/auth";
import { ApiError } from "../../api/client";
import AuthLink from "../../components/AuthLink";
import { resolveLanguage } from "../../utils/language";

// Mirrors the server's own check; anything stricter would just reject real addresses.
const EMAIL_PATTERN = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;

export default function ForgotPassword() {
  const { t, i18n } = useTranslation();

  const [email, setEmail] = useState("");
  const [loading, setLoading] = useState(false);
  const [sent, setSent] = useState(false);
  const [showErrors, setShowErrors] = useState(false);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  const required = t("auth.validation.required", { defaultValue: "This field is required." });
  const emailError = !email.trim() ? required : EMAIL_PATTERN.test(email.trim()) ? undefined : t("auth.forgot.invalidEmail");

  const showError = (content: string) =>
    setFlash([{ type: "error", content, dismissible: true, onDismiss: () => setFlash([]) }]);

  const handleSubmit = async () => {
    setShowErrors(true);
    setFlash([]);
    if (emailError) return;
    setLoading(true);
    try {
      await forgotPassword({
        email: email.trim(),
        language: resolveLanguage(i18n.resolvedLanguage ?? i18n.language),
      });
      setSent(true);
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        showError(t("auth.forgot.unavailable"));
      } else if (err instanceof ApiError && err.status === 429) {
        showError(t("auth.forgot.tooMany"));
      } else if (err instanceof ApiError && err.status === 400) {
        showError(t("auth.forgot.invalidEmail"));
      } else {
        showError(t("auth.forgot.error"));
      }
    } finally {
      setLoading(false);
    }
  };

  if (sent) {
    return (
      <div className="auth-layout__card">
      <Container header={<Header variant="h1">{t("auth.forgot.sentTitle")}</Header>}>
        <SpaceBetween size="m">
          <Alert type="success" statusIconAriaLabel={t("auth.forgot.sentTitle")}>
            {t("auth.forgot.sentBody")}
          </Alert>
          <Box color="text-body-secondary">{t("auth.forgot.sentHint")}</Box>
          <Button
            fullWidth
            onClick={() => {
              setSent(false);
              setShowErrors(false);
            }}
          >
            {t("auth.forgot.retry")}
          </Button>
          <Box textAlign="center">
            <AuthLink to="/login">{t("auth.forgot.backToLogin")}</AuthLink>
          </Box>
        </SpaceBetween>
      </Container>
      </div>
    );
  }

  return (
    <SpaceBetween size="l">
      {flash.length > 0 && <Flashbar items={flash} />}
      <div className="auth-layout__card">
      <Container header={<Header variant="h1" description={t("auth.forgot.description")}>{t("auth.forgot.title")}</Header>}>
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            void handleSubmit();
          }}
        >
          <Form>
            <SpaceBetween size="m">
              <FormField label={t("auth.forgot.email")} errorText={showErrors ? emailError : undefined}>
                <Input
                  value={email}
                  onChange={({ detail }) => setEmail(detail.value)}
                  type="email"
                  placeholder="name@example.com"
                  autoComplete="email"
                  autoFocus
                />
              </FormField>
              <Button variant="primary" loading={loading} formAction="submit" fullWidth>
                {t("auth.forgot.submit")}
              </Button>
            </SpaceBetween>
          </Form>
        </form>
        <Box textAlign="center" margin={{ top: "l" }}>
          <AuthLink to="/login">{t("auth.forgot.backToLogin")}</AuthLink>
        </Box>
      </Container>
      </div>
    </SpaceBetween>
  );
}
