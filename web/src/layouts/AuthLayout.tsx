import { Outlet } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { SpaceBetween, Button } from "@cloudscape-design/components";
import "./authLayout.css";

export default function AuthLayout() {
  const { i18n } = useTranslation();

  const changeLanguage = (lng: string) => {
    void i18n.changeLanguage(lng);
  };

  return (
    <div className="auth-layout">
      <div className="auth-layout__languages">
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
      <div className="auth-layout__form">
        <Outlet />
      </div>
    </div>
  );
}
