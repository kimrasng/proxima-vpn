import { Outlet } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Box, SpaceBetween, Button } from "@cloudscape-design/components";

export default function AuthLayout() {
  const { i18n } = useTranslation();

  const changeLanguage = (lng: string) => {
    void i18n.changeLanguage(lng);
  };

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        padding: "2rem",
      }}
    >
      <div style={{ position: "absolute", top: "1rem", right: "1rem" }}>
        <SpaceBetween direction="horizontal" size="xs">
          <Button
            variant={i18n.language === "ko" ? "primary" : "normal"}
            onClick={() => changeLanguage("ko")}
          >
            한국어
          </Button>
          <Button
            variant={i18n.language === "en" ? "primary" : "normal"}
            onClick={() => changeLanguage("en")}
          >
            English
          </Button>
          <Button
            variant={i18n.language === "zh" ? "primary" : "normal"}
            onClick={() => changeLanguage("zh")}
          >
            中文
          </Button>
        </SpaceBetween>
      </div>
      <Box padding={{ top: "xxxl" }}>
        <div style={{ width: "100%", maxWidth: "480px" }}>
          <Outlet />
        </div>
      </Box>
    </div>
  );
}
