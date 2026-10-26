import { useEffect, useState } from "react";
import PortalEmpty from "../../components/PortalEmpty";
import GuideContent from "../../components/GuideContent";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Cards,
  Box,
  Spinner,
  Flashbar,
  type FlashbarProps,
  SpaceBetween,
} from "@cloudscape-design/components";
import type { Announcement } from "../../api/types";
import * as userApi from "../../api/user";

export default function Announcements() {
  const { t, i18n } = useTranslation();
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const data = await userApi.listAnnouncements(i18n.resolvedLanguage ?? i18n.language);
        if (!cancelled) setAnnouncements(data.filter((a) => a.is_active));
      } catch {
        if (!cancelled) setFlash([{ type: "error", content: t("user.announcements.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    void load();
    return () => { cancelled = true; };
  }, [i18n.resolvedLanguage, i18n.language, t]);

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1" description={t("user.announcements.description")}>{t("user.announcements.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={<Header variant="h1" description={t("user.announcements.description")}>{t("user.announcements.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />
        {announcements.length === 0 ? (
          <PortalEmpty title={t("user.announcements.empty")} description={t("user.announcements.emptyHint")} />
        ) : (
        <Cards
          cardsPerRow={[{ cards: 1 }]}
          cardDefinition={{
            header: (item) => item.title,
            sections: [
              {
                id: "content",
                content: (item) => item.content_html
                  ? <GuideContent html={item.content_html} />
                  : <Box variant="p">{item.content}</Box>,
              },
              {
                id: "date",
                content: (item) => (
                  <Box color="text-status-inactive" fontSize="body-s">
                    {new Date(item.created_at).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language)}
                  </Box>
                ),
              },
            ],
          }}
          items={[...announcements].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())}
        />
        )}
      </SpaceBetween>
    </ContentLayout>
  );
}
