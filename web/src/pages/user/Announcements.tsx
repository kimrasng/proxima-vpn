import { useEffect, useState } from "react";
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
  const { t } = useTranslation();
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  useEffect(() => {
    const load = async () => {
      try {
        const data = await userApi.listAnnouncements();
        setAnnouncements(data.filter((a) => a.is_active));
      } catch {
        setFlash([{ type: "error", content: t("user.announcements.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
      } finally {
        setLoading(false);
      }
    };
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.announcements.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={<Header variant="h1">{t("user.announcements.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />
        <Cards
          cardDefinition={{
            header: (item) => item.title,
            sections: [
              {
                id: "content",
                content: (item) => <Box variant="p">{item.content}</Box>,
              },
              {
                id: "date",
                content: (item) => (
                  <Box color="text-status-inactive" fontSize="body-s">
                    {new Date(item.created_at).toLocaleDateString()}
                  </Box>
                ),
              },
            ],
          }}
          items={announcements}
          empty={
            <Box textAlign="center" padding="l">
              {t("user.announcements.empty")}
            </Box>
          }
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
