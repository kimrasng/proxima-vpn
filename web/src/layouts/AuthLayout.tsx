import { Outlet, useLocation } from "react-router-dom";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { ButtonDropdown, Icon } from "@cloudscape-design/components";
import { resolveLanguage } from "../utils/language";
import laying from "../assets/landing/laying.svg";
import selfie from "../assets/landing/selfie.svg";
import "./authLayout.css";

const LANGUAGES = [
  { id: "ko", text: "한국어" },
  { id: "en", text: "English" },
  { id: "zh", text: "中文" },
] as const;

export default function AuthLayout() {
  const location = useLocation();
  useEffect(() => {
    const code = new URLSearchParams(location.search).get("ref")?.trim();
    if (code) window.localStorage.setItem("proxima_referral_code", code);
  }, [location.search]);
  const { t, i18n } = useTranslation();
  const current = resolveLanguage(i18n.resolvedLanguage ?? i18n.language);
  const showArt = !location.pathname.startsWith("/admin");
  const currentLabel = LANGUAGES.find((language) => language.id === current)?.text ?? LANGUAGES[0].text;

  return (
    <div className={`auth-layout${showArt ? " auth-layout--art" : ""}`}>
      <div className="auth-layout__languages">
        <ButtonDropdown
          variant="normal"
          ariaLabel={`${t("auth.portal.language")}: ${currentLabel}`}
          expandToViewport
          items={LANGUAGES.map((language) => ({
            id: language.id,
            text: language.text,
            disabled: language.id === current,
          }))}
          onItemClick={({ detail }) => {
            void i18n.changeLanguage(detail.id);
          }}
        >
          <span className="auth-layout__lang-label">
            <Icon name="globe" />
            <span>{currentLabel}</span>
          </span>
        </ButtonDropdown>
      </div>

      <div className="auth-layout__body">
        {showArt && (
          <aside className="auth-layout__art" aria-hidden="true">
            <img src={laying} alt="" className="auth-layout__art-main" />
            <img src={selfie} alt="" className="auth-layout__art-side" />
          </aside>
        )}
        <main className="auth-layout__main">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
