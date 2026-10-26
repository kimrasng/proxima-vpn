import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ContentLayout,
  Flashbar,
  Header,
  SpaceBetween,
  Spinner,
  type FlashbarProps,
} from "@cloudscape-design/components";
import PortalEmpty from "../../components/PortalEmpty";
import DeviceQuickConnect from "../../components/DeviceQuickConnect";
import type { UserProfile } from "../../api/types";
import * as userApi from "../../api/user";

export default function Devices() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [profile, setProfile] = useState<UserProfile | null>(null);

  useEffect(() => {
    userApi
      .getProfile()
      .then(setProfile)
      .catch(() =>
        setFlash([
          {
            type: "error",
            content: t("user.devices.loadError"),
            dismissible: true,
            onDismiss: () => setFlash([]),
          },
        ]),
      )
      .finally(() => setLoading(false));
  }, [t]);

  const header = (
    <Header variant="h1" description={t("user.devices.description")}>
      {t("user.devices.title")}
    </Header>
  );

  if (loading) {
    return (
      <ContentLayout header={header}>
        <Box textAlign="center" padding="xl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={header}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />
        {!profile?.plan_name ? (
          <PortalEmpty
            title={t("user.devices.noPlanTitle")}
            description={t("user.devices.noPlanHint")}
            action={
              <Button
                variant="primary"
                onClick={() => navigate("/portal/plan")}
              >
                {t("user.plan.checkoutModal.viewPlans")}
              </Button>
            }
          />
        ) : (
          <DeviceQuickConnect variant="full" refreshKey={0} />
        )}
      </SpaceBetween>
    </ContentLayout>
  );
}
