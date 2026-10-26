import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "react-router-dom";
import { Alert, Box, Button, Container, ContentLayout, Header, SpaceBetween, Spinner } from "@cloudscape-design/components";
import GuideContent from "../../components/GuideContent";
import { getPublishedGuide, type GuideDetail as GuideDetailData } from "../../api/guides";

export default function GuideDetail() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const { guideId = "" } = useParams();
  const [guide, setGuide] = useState<GuideDetailData | null>(null);
  const [missing, setMissing] = useState(false);

  useEffect(() => {
    let active = true;
    setGuide(null);
    setMissing(false);
    getPublishedGuide(guideId)
      .then((res) => { if (active) setGuide(res); })
      .catch(() => { if (active) setMissing(true); });
    return () => { active = false; };
  }, [guideId]);

  const back = (
    <Button variant="link" iconName="arrow-left" onClick={() => navigate("/portal/guides")}>{t("user.guides.back")}</Button>
  );

  if (missing) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.guides.title")}</Header>}>
        <SpaceBetween size="m">
          <Alert type="warning" header={t("user.guides.notFound")}>{t("user.guides.notFoundHint")}</Alert>
          <Box>{back}</Box>
        </SpaceBetween>
      </ContentLayout>
    );
  }

  if (!guide) {
    return <ContentLayout header={<Header variant="h1">{t("user.guides.title")}</Header>}><Box textAlign="center" padding="xl"><Spinner size="large" /></Box></ContentLayout>;
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={[guide.category_name, t("user.guides.updated", {
            date: new Date(guide.updated_at).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language),
          })].filter(Boolean).join(" · ")}
          actions={back}
        >
          {guide.title}
        </Header>
      }
    >
      <Container>
        <div style={{ maxWidth: 820 }}>
          {guide.summary && <Box color="text-body-secondary" padding={{ bottom: "m" }}>{guide.summary}</Box>}
          <GuideContent html={guide.rendered_html} />
        </div>
      </Container>
    </ContentLayout>
  );
}
