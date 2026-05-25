import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  AppLayout,
  SideNavigation,
  type SideNavigationProps,
  TopNavigation,
} from "@cloudscape-design/components";
import { useTheme } from "../hooks/useTheme";

export default function UserLayout() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { theme, toggle: toggleTheme } = useTheme();

  const navItems: SideNavigationProps.Item[] = [
    { type: "link", text: t("user.nav.devices"), href: "/portal/devices" },
    { type: "link", text: t("user.nav.traffic"), href: "/portal/traffic" },
    { type: "link", text: t("user.nav.plan"), href: "/portal/plan" },
    { type: "link", text: t("user.nav.account"), href: "/portal/account" },
    { type: "link", text: t("user.nav.announcements"), href: "/portal/announcements" },
  ];

  const changeLanguage = (lng: string) => {
    void i18n.changeLanguage(lng);
  };

  return (
    <>
      <TopNavigation
        identity={{
          href: "/portal/devices",
          title: "Proxima VPN",
        }}
        utilities={[
          {
            type: "button",
            iconName: "light-dark",
            ariaLabel: theme === "dark" ? "Switch to light mode" : "Switch to dark mode",
            onClick: toggleTheme,
          },
          {
            type: "menu-dropdown",
            text: i18n.language === "ko" ? "한국어" : i18n.language === "zh" ? "中文" : "English",
            items: [
              { id: "ko", text: "한국어" },
              { id: "en", text: "English" },
              { id: "zh", text: "中文" },
            ],
            onItemClick: ({ detail }) => changeLanguage(detail.id),
          },
        ]}
      />
      <AppLayout
        navigation={
          <SideNavigation
            activeHref={location.pathname}
            items={navItems}
            onFollow={(event) => {
              event.preventDefault();
              navigate(event.detail.href);
            }}
          />
        }
        content={<Outlet />}
        toolsHide
      />
    </>
  );
}
