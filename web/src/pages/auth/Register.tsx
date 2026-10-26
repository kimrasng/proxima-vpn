import { useEffect, useState } from "react";
import { useLocation, useNavigate, Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Alert,
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
import { getSignupPolicy, requestSignupEmailCode, setToken, verifySignupEmailCode } from "../../api/auth";
import type { SignupPolicy } from "../../api/auth";
import { ApiError } from "../../api/client";
import { consumePostLogin } from "../../utils/postLogin";

export default function Register() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();

  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [passwordConfirm, setPasswordConfirm] = useState("");
  const [verificationCode, setVerificationCode] = useState("");
  const [verificationStep, setVerificationStep] = useState(false);
  const [resending, setResending] = useState(false);
  const codeQuery = new URLSearchParams(location.search).get("ref")?.trim() ?? "";
  const [referralCode, setReferralCode] = useState(() => {
    const query = codeQuery;
    if (query) {
      window.localStorage.setItem("proxima_referral_code", query);
      return query;
    }
    return window.localStorage.getItem("proxima_referral_code") ?? "";
  });
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

  const tokenKey = `proxima_signup_token:${email.trim().toLowerCase()}`;
  const requestCode = async () => {
    const issued = await requestSignupEmailCode({ name, email, password, referral_code: referralCode.trim() || undefined, language: i18n.resolvedLanguage || i18n.language, signup_token: window.sessionStorage.getItem(tokenKey) || undefined });
    window.sessionStorage.setItem(tokenKey, issued.signup_token);
  };

  const resendCode = async () => {
    setResending(true);
    setFlash([]);
    try {
      await requestCode();
      setVerificationCode("");
      setFlash([{ type: "success", content: t("auth.register.codeSent", { email }), dismissible: true, onDismiss: () => setFlash([]) }]);
    } catch (err) {
      const body = err instanceof ApiError ? err.body as { error?: string } | undefined : undefined;
      setFlash([{ type: "error", content: body?.error || t("auth.register.resendError"), dismissible: true, onDismiss: () => setFlash([]) }]);
    } finally { setResending(false); }
  };

  const handleSubmit = async () => {
    setShowErrors(true);
    setFlash([]);
    if (verificationStep) {
      if (!/^\d{6}$/.test(verificationCode)) {
        setFlash([{ type: "error", content: t("auth.register.codeInvalid"), dismissible: true, onDismiss: () => setFlash([]) }]);
        return;
      }
      setLoading(true);
      try {
        const created = await verifySignupEmailCode({ name, email, password, referral_code: referralCode.trim() || undefined, code: verificationCode, signup_token: window.sessionStorage.getItem(tokenKey) || "", language: i18n.resolvedLanguage || i18n.language });
        window.sessionStorage.removeItem(tokenKey);
        window.localStorage.removeItem("proxima_referral_code");
        if (created.token) {
          setToken("user", created.token);
          navigate(consumePostLogin("/portal/dashboard"), { replace: true });
        } else {
          navigate("/login", { replace: true });
        }
      } catch (err) {
        const body = err instanceof ApiError ? err.body as { error?: string } | undefined : undefined;
        setFlash([{ type: "error", content: body?.error || t("auth.register.codeVerifyError"), dismissible: true, onDismiss: () => setFlash([]) }]);
      } finally { setLoading(false); }
      return;
    }
    if (!verificationStep && Object.values(errors).some(Boolean)) return;
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
      await requestCode();
      setVerificationStep(true);
      setFlash([]);
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
              <Button variant="primary" loading={loading} disabled={resending} formAction="submit">
                {verificationStep ? t("auth.register.verifyCode") : t("auth.register.submit")}
              </Button>
            }
          >
            <SpaceBetween size="m">
              {verificationStep ? (
                <>
                  <Alert type="info" header={t("auth.register.codeTitle")}>
                    {t("auth.register.codeSent", { email })}
                  </Alert>
                  <FormField label={t("auth.register.codeLabel")} description={t("auth.register.codeHelp")}>
                    <Input
                      value={verificationCode}
                      onChange={({ detail }) => setVerificationCode(detail.value.replace(/\D/g, "").slice(0, 6))}
                      type="text"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      placeholder="000000"
                      autoFocus
                    />
                  </FormField>
                  <Button variant="link" formAction="none" disabled={loading} loading={resending} onClick={() => void resendCode()}>
                    {t("auth.register.resendCode")}
                  </Button>
                </>
              ) : (
                <>
              {allowedDomains.length > 0 && (
                <div className="register__domain-notice" role="note">
                  <div className="register__domain-notice-icon" aria-hidden="true">i</div>
                  <div className="register__domain-notice-content">
                    <div className="register__domain-notice-title">{t("auth.register.allowedDomainsTitle")}</div>
                    <div className="register__domain-notice-body">{t("auth.register.allowedDomainsBody")}</div>
                    <div className="register__domain-list" data-testid="allowed-domains">
                      {allowedDomains.map((domain) => <span className="register__domain-chip" key={domain}>{domain}</span>)}
                    </div>
                  </div>
                </div>
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
              <FormField label={t("auth.register.referralCode")}>
                <Input
                  value={referralCode}
                  onChange={({ detail }) => {
                    setReferralCode(detail.value);
                    if (detail.value.trim()) window.localStorage.setItem("proxima_referral_code", detail.value.trim());
                    else window.localStorage.removeItem("proxima_referral_code");
                  }}
                  placeholder={t("auth.register.referralCodePlaceholder")}
                  autoComplete="off"
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
                </>
              )}
            </SpaceBetween>
          </Form>
        </form>
        <div style={{ marginTop: "1rem", textAlign: "center" }}>
          {verificationStep ? (
            <Button variant="link" formAction="none" disabled={loading || resending} onClick={() => { setVerificationStep(false); setVerificationCode(""); setFlash([]); }}>
              {t("auth.register.backToDetails")}
            </Button>
          ) : <Link to="/login">{t("auth.register.loginLink")}</Link>}
        </div>
      </Container>
    </SpaceBetween>
  );
}
