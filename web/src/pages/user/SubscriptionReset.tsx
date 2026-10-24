import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Container,
  ContentLayout,
  CopyToClipboard,
  Header,
  Icon,
  Input,
  SpaceBetween,
  Spinner,
} from "@cloudscape-design/components";
import * as userApi from "../../api/user";
import { getAccountSubscriptionUrl } from "../../utils/subscriptionUrl";
import "./accountSettings.css";

type Step = "explain" | "done";

export default function SubscriptionReset() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [hasLink, setHasLink] = useState<boolean | null>(null);
  const [understood, setUnderstood] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [step, setStep] = useState<Step>("explain");
  const [newUrl, setNewUrl] = useState("");

  useEffect(() => {
    let active = true;
    userApi.getProfile()
      .then((profile) => { if (active) setHasLink(!!profile.sub_token); })
      .catch(() => { if (active) setHasLink(false); });
    return () => { active = false; };
  }, []);

  const reset = async () => {
    setBusy(true);
    setError(false);
    try {
      const result = await userApi.regenerateSubToken();
      setNewUrl(getAccountSubscriptionUrl(result.sub_token));
      setStep("done");
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  };

  const header = (
    <Header variant="h1" description={t("user.subReset.description")}>
      {t("user.subReset.title")}
    </Header>
  );

  if (hasLink === null) {
    return <ContentLayout header={header}><Box textAlign="center" padding="xl"><Spinner size="large" /></Box></ContentLayout>;
  }

  if (step === "done") {
    return (
      <ContentLayout header={header}>
        <SpaceBetween size="l">
          <Alert type="success" header={t("user.subReset.doneTitle")}>{t("user.subReset.doneBody")}</Alert>
          <Container header={<Header variant="h2">{t("user.subReset.newLink")}</Header>}>
            <SpaceBetween size="m">
              <Input value={newUrl} readOnly ariaLabel={t("user.subReset.newLink")} />
              <CopyToClipboard textToCopy={newUrl} copyButtonText={t("user.dashboard.copyUrl")}
                copySuccessText={t("common.copied")} copyErrorText={t("user.dashboard.copyError")} />
              <Box variant="h4">{t("user.subReset.nextTitle")}</Box>
              <ol className="account-settings__steps">
                <li>{t("user.subReset.next1")}</li>
                <li>{t("user.subReset.next2")}</li>
                <li>{t("user.subReset.next3")}</li>
              </ol>
              <SpaceBetween direction="horizontal" size="xs">
                <Button variant="primary" onClick={() => navigate("/portal/devices")}>{t("user.subReset.goDevices")}</Button>
                <Button onClick={() => navigate("/portal/dashboard")}>{t("user.subReset.goDashboard")}</Button>
              </SpaceBetween>
            </SpaceBetween>
          </Container>
        </SpaceBetween>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={header}>
      <SpaceBetween size="l">
        <Container header={<Header variant="h2">{t("user.subReset.whatTitle")}</Header>}>
          <div className="account-settings__explain">
            <Icon name="key" size="medium" variant="subtle" />
            <Box>{t("user.subReset.whatBody")}</Box>
          </div>
        </Container>

        <Container header={<Header variant="h2">{t("user.subReset.whenTitle")}</Header>}>
          <ul className="account-settings__tips">
            <li>{t("user.subReset.when1")}</li>
            <li>{t("user.subReset.when2")}</li>
            <li>{t("user.subReset.when3")}</li>
          </ul>
        </Container>

        <Container header={<Header variant="h2">{t("user.subReset.effectTitle")}</Header>}>
          <SpaceBetween size="m">
            <div className="account-settings__compare">
              <div>
                <Box variant="h4"><Icon name="close" variant="error" /> {t("user.subReset.effectStops")}</Box>
                <Box color="text-body-secondary">{t("user.subReset.effectStopsBody")}</Box>
              </div>
              <div>
                <Box variant="h4"><Icon name="check" variant="success" /> {t("user.subReset.effectKeeps")}</Box>
                <Box color="text-body-secondary">{t("user.subReset.effectKeepsBody")}</Box>
              </div>
            </div>
            {!hasLink && <Alert type="info">{t("user.account.noSubscriptionYet")}</Alert>}
            {error && <Alert type="error">{t("user.subReset.error")}</Alert>}
            <Checkbox checked={understood} disabled={!hasLink} onChange={({ detail }) => setUnderstood(detail.checked)}>
              {t("user.subReset.confirmCheck")}
            </Checkbox>
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="primary" disabled={!understood || !hasLink} loading={busy} onClick={() => void reset()}>
                {t("user.subReset.action")}
              </Button>
              <Button variant="link" onClick={() => navigate(-1)}>{t("user.subReset.back")}</Button>
            </SpaceBetween>
          </SpaceBetween>
        </Container>
      </SpaceBetween>
    </ContentLayout>
  );
}
