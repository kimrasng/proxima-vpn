import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
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
import { adminLogin, setToken } from "../../api/auth";
import { ApiError } from "../../api/client";

export default function AdminLogin() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [showTotp, setShowTotp] = useState(false);
  const [loading, setLoading] = useState(false);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  const handleSubmit = async () => {
    setLoading(true);
    setFlash([]);

    try {
      const req = { email, password, ...(showTotp ? { totp_code: totpCode } : {}) };
      const res = await adminLogin(req);
      setToken("admin", res.token);
      navigate("/admin/dashboard", { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        const body = err.body as { error?: string } | undefined;
        const errorMsg = body?.error || err.statusText;

        if (errorMsg.includes("totp") || errorMsg.includes("2fa") || errorMsg.includes("TOTP")) {
          setShowTotp(true);
          setFlash([
            {
              type: "info",
              content: t("auth.adminLogin.totpRequired"),
              dismissible: true,
              onDismiss: () => setFlash([]),
            },
          ]);
        } else {
          setFlash([
            {
              type: "error",
              content: errorMsg || t("auth.adminLogin.error"),
              dismissible: true,
              onDismiss: () => setFlash([]),
            },
          ]);
        }
      } else {
        setFlash([
          {
            type: "error",
            content: t("auth.adminLogin.error"),
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
      <Container header={<Header variant="h1">{t("auth.adminLogin.title")}</Header>}>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void handleSubmit();
          }}
        >
          <Form
            actions={
              <Button variant="primary" loading={loading} formAction="submit">
                {t("auth.adminLogin.submit")}
              </Button>
            }
          >
            <SpaceBetween size="m">
              <FormField label={t("auth.adminLogin.email")}>
                <Input
                  value={email}
                  onChange={({ detail }) => setEmail(detail.value)}
                  type="email"
                  placeholder="admin@example.com"
                  autoFocus
                />
              </FormField>
              <FormField label={t("auth.adminLogin.password")}>
                <Input
                  value={password}
                  onChange={({ detail }) => setPassword(detail.value)}
                  type="password"
                />
              </FormField>
              {showTotp && (
                <FormField
                  label={t("auth.adminLogin.totpCode")}
                  description={t("auth.adminLogin.totpDescription")}
                >
                  <Input
                    value={totpCode}
                    onChange={({ detail }) => setTotpCode(detail.value)}
                    placeholder="000000"
                    autoFocus
                  />
                </FormField>
              )}
            </SpaceBetween>
          </Form>
        </form>
      </Container>
    </SpaceBetween>
  );
}
