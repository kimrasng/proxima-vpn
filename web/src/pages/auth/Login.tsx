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
import { userLogin, setToken } from "../../api/auth";
import { ApiError } from "../../api/client";

export default function Login() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  const handleSubmit = async () => {
    setLoading(true);
    setFlash([]);

    try {
      const res = await userLogin({ email, password });
      setToken("user", res.token);
      navigate("/portal/devices", { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        const body = err.body as { error?: string } | undefined;
        setFlash([
          {
            type: "error",
            content: body?.error || t("auth.login.error"),
            dismissible: true,
            onDismiss: () => setFlash([]),
          },
        ]);
      } else {
        setFlash([
          {
            type: "error",
            content: t("auth.login.error"),
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
      <Container header={<Header variant="h1">{t("auth.login.title")}</Header>}>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void handleSubmit();
          }}
        >
          <Form
            actions={
              <SpaceBetween direction="horizontal" size="xs">
                <Button variant="primary" loading={loading} formAction="submit">
                  {t("auth.login.submit")}
                </Button>
              </SpaceBetween>
            }
          >
            <SpaceBetween size="m">
              <FormField label={t("auth.login.email")}>
                <Input
                  value={email}
                  onChange={({ detail }) => setEmail(detail.value)}
                  type="email"
                  placeholder="user@example.com"
                  autoFocus
                />
              </FormField>
              <FormField label={t("auth.login.password")}>
                <Input
                  value={password}
                  onChange={({ detail }) => setPassword(detail.value)}
                  type="password"
                />
              </FormField>
            </SpaceBetween>
          </Form>
        </form>
        <div style={{ marginTop: "1rem", textAlign: "center" }}>
          <Link to="/register">{t("auth.login.registerLink")}</Link>
        </div>
      </Container>
    </SpaceBetween>
  );
}
