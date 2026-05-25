import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  Container,
  Header,
  SpaceBetween,
} from "@cloudscape-design/components";

export default function NotFound() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  return (
    <Box
      padding="xxl"
      textAlign="center"
    >
      <SpaceBetween size="xl" alignItems="center">
        <Box
          fontSize="display-l"
          fontWeight="bold"
          color="text-status-inactive"
        >
          404
        </Box>
        <Container
          header={
            <Header variant="h1">{t("notFound.title", "Page Not Found")}</Header>
          }
        >
          <SpaceBetween size="m" alignItems="center">
            <Box color="text-body-secondary">
              {t(
                "notFound.description",
                "The page you're looking for doesn't exist or has been moved."
              )}
            </Box>
            <SpaceBetween direction="horizontal" size="s">
              <Button onClick={() => navigate(-1)}>
                {t("notFound.goBack", "Go Back")}
              </Button>
              <Button variant="primary" onClick={() => navigate("/portal/devices")}>
                {t("notFound.goHome", "Go to Dashboard")}
              </Button>
            </SpaceBetween>
          </SpaceBetween>
        </Container>
      </SpaceBetween>
    </Box>
  );
}
