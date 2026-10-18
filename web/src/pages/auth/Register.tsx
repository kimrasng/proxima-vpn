import { useEffect, useState } from "react";
import { useNavigate, Link } from "react-router-dom";
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
  StatusIndicator,
} from "@cloudscape-design/components";
import type { FlashbarProps } from "@cloudscape-design/components";
import { getSignupPolicy, userRegister } from "../../api/auth";
import type { SignupPolicy } from "../../api/auth";
import { ApiError } from "../../api/client";

export default function Register() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [passwordConfirm, setPasswordConfirm] = useState("");
  const [loading, setLoading] = useState(false);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [showErrors, setShowErrors] = useState(false);
  const [policy, setPolicy] = useState<SignupPolicy | null>(null);

  // Show the allowed email domains up front. If the lookup fails the form still
  // works: the server enforces the policy either way and explains a refusal.
  useEffect(() => {
    let cancelled = false;
    getSignupPolicy()
      .then((p) => {
        if (!cancelled) setPolicy(p);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  const allowedDomains = policy?.restricted ? policy.allowed_domains : [];
  const emailDomain = email.includes("@") ? email.slice(email.lastIndexOf("@") + 1).trim().toLowerCase() : "";
  const domainChecked = allowedDomains.length > 0 && emailDomain.includes(".");
  const domainOk = domainChecked && allowedDomains.includes(emailDomain);
  const required = t("auth.validation.required", { defaultValue: "This field is required." });
  const errors = {
    name: name.trim() ? undefined : required,
    email: email.trim() ? undefined : required,
    password: password.trim() ? undefined : required,
    passwordConfirm: !passwordConfirm.trim()
      ? required
      : password !== passwordConfirm
        ? t("auth.register.passwordMismatch")
        : undefined,
  };

  const handleSubmit = async () => {
    setShowErrors(true);
    setFlash([]);
    if (Object.values(errors).some(Boolean)) return;
    if (domainChecked && !domainOk) {
      setFlash([
        {
          type: "error",
          content: t("auth.register.domainNotAllowed", { domains: allowedDomains.join(", ") }),
          dismissible: true,
          onDismiss: () => setFlash([]),
        },
      ]);
      return;
    }
    setLoading(true);

    try {
      await userRegister({ name, email, password });
      setFlash([
        {
          type: "success",
          content: t("auth.register.success"),
          dismissible: true,
          onDismiss: () => setFlash([]),
        },
      ]);
      setTimeout(() => navigate("/login", { replace: true }), 1500);
    } catch (err) {
      if (err instanceof ApiError) {
        const body = err.body as { error?: string; allowed_domains?: string[] } | undefined;
        if (err.status === 403 && body?.allowed_domains) {
          // Admin restricted sign-up to certain email domains: say which ones, in the UI language.
          setFlash([
            {
              type: "error",
              content: t("auth.register.domainNotAllowed", { domains: body.allowed_domains.join(", ") }),
              dismissible: true,
              onDismiss: () => setFlash([]),
            },
          ]);
          return;
        }
        setFlash([
          {
            type: "error",
            content: body?.error || t("auth.register.error"),
            dismissible: true,
            onDismiss: () => setFlash([]),
          },
        ]);
      } else {
        setFlash([
          {
            type: "error",
            content: t("auth.register.error"),
            dismissible: true,
            onDismiss: () => setFlash([]),
          },
        ]);
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <SpaceBetween size="l">
      <Flashbar items={flash} />
      <Container header={<Header variant="h1">{t("auth.register.title")}</Header>}>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void handleSubmit();
          }}
        >
          <Form
            actions={
              <Button variant="primary" loading={loading} formAction="submit">
                {t("auth.register.submit")}
              </Button>
            }
          >
            <SpaceBetween size="m">
              {allowedDomains.length > 0 && (
                <Alert type="info" header={t("auth.register.allowedDomainsTitle")}>
                  <SpaceBetween size="xs">
                    <span>{t("auth.register.allowedDomainsBody")}</span>
                    <Box variant="code" data-testid="allowed-domains">
                      {allowedDomains.join("  ·  ")}
                    </Box>
                  </SpaceBetween>
                </Alert>
              )}
              <FormField label={t("auth.register.name")} errorText={showErrors ? errors.name : undefined}>
                <Input
                  value={name}
                  onChange={({ detail }) => setName(detail.value)}
                  placeholder={t("auth.register.namePlaceholder")}
                  autoFocus
                />
              </FormField>
              <FormField
                label={t("auth.register.email")}
                errorText={showErrors ? errors.email : undefined}
                constraintText={
                  domainChecked ? (
                    domainOk ? (
                      <StatusIndicator type="success">{emailDomain}</StatusIndicator>
                    ) : (
                      <StatusIndicator type="error">
                        {t("auth.register.domainNotAllowed", { domains: allowedDomains.join(", ") })}
                      </StatusIndicator>
                    )
                  ) : undefined
                }
              >
                <Input
                  value={email}
                  onChange={({ detail }) => setEmail(detail.value)}
                  type="email"
                  placeholder="user@example.com"
                />
              </FormField>
              <FormField label={t("auth.register.password")} errorText={showErrors ? errors.password : undefined}>
                <Input
                  value={password}
                  onChange={({ detail }) => setPassword(detail.value)}
                  type="password"
                />
              </FormField>
              <FormField label={t("auth.register.passwordConfirm")} errorText={showErrors ? errors.passwordConfirm : undefined}>
                <Input
                  value={passwordConfirm}
                  onChange={({ detail }) => setPasswordConfirm(detail.value)}
                  type="password"
                />
              </FormField>
            </SpaceBetween>
          </Form>
        </form>
        <div style={{ marginTop: "1rem", textAlign: "center" }}>
          <Link to="/login">{t("auth.register.loginLink")}</Link>
        </div>
      </Container>
    </SpaceBetween>
  );
}
