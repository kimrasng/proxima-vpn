import { useState } from "react";
import { useNavigate, Link } from "react-router-dom";
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
import { userRegister } from "../../api/auth";
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

  const handleSubmit = async () => {
    setFlash([]);

    if (password !== passwordConfirm) {
      setFlash([
        {
          type: "error",
          content: t("auth.register.passwordMismatch"),
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
        const body = err.body as { error?: string } | undefined;
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
              <FormField label={t("auth.register.name")}>
                <Input
                  value={name}
                  onChange={({ detail }) => setName(detail.value)}
                  placeholder={t("auth.register.namePlaceholder")}
                  autoFocus
                />
              </FormField>
              <FormField label={t("auth.register.email")}>
                <Input
                  value={email}
                  onChange={({ detail }) => setEmail(detail.value)}
                  type="email"
                  placeholder="user@example.com"
                />
              </FormField>
              <FormField label={t("auth.register.password")}>
                <Input
                  value={password}
                  onChange={({ detail }) => setPassword(detail.value)}
                  type="password"
                />
              </FormField>
              <FormField label={t("auth.register.passwordConfirm")}>
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
