import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Table,
  Box,
  Spinner,
  Flashbar,
  type FlashbarProps,
  StatusIndicator,
  SpaceBetween,
} from "@cloudscape-design/components";
import type { AvailableNode } from "../../api/types";
import * as userApi from "../../api/user";

export default function Nodes() {
  const { t } = useTranslation();
  const [nodes, setNodes] = useState<AvailableNode[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);

  useEffect(() => {
    const load = async () => {
      try {
        setNodes(await userApi.listAvailableNodes());
      } catch {
        setFlash([
          {
            type: "error",
            content: t("user.nodes.loadError"),
            dismissible: true,
            onDismiss: () => setFlash([]),
          },
        ]);
      } finally {
        setLoading(false);
      }
    };
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.nodes.title")}</Header>}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header variant="h1" description={t("user.nodes.description")}>
          {t("user.nodes.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {flash.length > 0 && <Flashbar items={flash} />}
        <Table
          items={nodes}
          loadingText={t("user.nodes.title")}
          columnDefinitions={[
            {
              id: "name",
              header: t("user.nodes.name"),
              cell: (n: AvailableNode) => n.name,
            },
            {
              id: "location",
              header: t("user.nodes.location"),
              cell: (n: AvailableNode) => [n.country, n.region].filter(Boolean).join(" / ") || "-",
            },
            {
              id: "status",
              header: t("user.nodes.status"),
              cell: (n: AvailableNode) =>
                n.status === "online" ? (
                  <StatusIndicator type="success">{t("user.nodes.online")}</StatusIndicator>
                ) : (
                  <StatusIndicator type="stopped">{t("user.nodes.offline")}</StatusIndicator>
                ),
            },
          ]}
          empty={
            <Box textAlign="center" padding="l" color="text-status-inactive">
              {t("user.nodes.empty")}
            </Box>
          }
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
