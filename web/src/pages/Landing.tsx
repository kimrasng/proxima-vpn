import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { resolveLanguage } from "../utils/language";
import laying from "../assets/landing/laying.svg";
import selfie from "../assets/landing/selfie.svg";
import "./landing.css";

const LANGUAGES = [
  { id: "ko", text: "한국어" },
  { id: "en", text: "English" },
  { id: "zh", text: "中文" },
] as const;

export default function Landing() {
  const { t, i18n } = useTranslation();
  const current = resolveLanguage(i18n.resolvedLanguage ?? i18n.language);

  return (
    <div className="landing">
      <header className="landing__header">
        <Link to="/" className="landing__brand" aria-label={t("auth.portal.name")}>
          <span className="landing__logo" aria-hidden="true">P</span>
          <span>Proxima</span>
        </Link>
        <label className="landing__lang">
          <span className="landing__sr">{t("auth.portal.language")}</span>
          <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
            <circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" strokeWidth="1.3" />
            <path d="M1.5 8h13M8 1.5c2 2.2 2 10.8 0 13M8 1.5c-2 2.2-2 10.8 0 13" fill="none" stroke="currentColor" strokeWidth="1.3" />
          </svg>
          <select value={current} onChange={(event) => void i18n.changeLanguage(event.target.value)}>
            {LANGUAGES.map((language) => <option key={language.id} value={language.id}>{language.text}</option>)}
          </select>
        </label>
      </header>

      <main className="landing__hero">
        <section className="landing__copy">
          <span className="landing__badge">
            <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true">
              <path d="M8 1l1.6 4.4L14 7l-4.4 1.6L8 13l-1.6-4.4L2 7l4.4-1.6z" fill="currentColor" />
            </svg>
            {t("landing.badge")}
          </span>
          <h1>{t("landing.title")}</h1>
          <p className="landing__lead">{t("landing.lead")}</p>
          <ul className="landing__points">
            <li>{t("landing.point1")}</li>
            <li>{t("landing.point2")}</li>
            <li>{t("landing.point3")}</li>
          </ul>
          <div className="landing__actions">
            <Link to="/register" className="landing__btn landing__btn--primary">
              {t("landing.start")}
              <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
                <path d="M3 8h9.5M8.5 4l4 4-4 4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </Link>
            <Link to="/login" className="landing__btn landing__btn--ghost">{t("landing.login")}</Link>
          </div>
        </section>

        <figure className="landing__art" aria-hidden="true">
          <img src={laying} alt="" className="landing__art-main" />
          <img src={selfie} alt="" className="landing__art-side" />
          <svg className="landing__squiggle landing__squiggle--a" viewBox="0 0 120 20"><path d="M2 14c18-14 26 8 40-2s22-8 76-2" /></svg>
          <svg className="landing__squiggle landing__squiggle--b" viewBox="0 0 160 24"><path d="M2 18c22-16 34 10 56-4s40 2 100-6" /></svg>
        </figure>
      </main>

      <footer className="landing__footer">© {new Date().getFullYear()} Proxima. {t("landing.rights")}</footer>
    </div>
  );
}
