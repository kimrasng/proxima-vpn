import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  AppLayout,
  SideNavigation,
  type SideNavigationProps,
  TopNavigation,
} from "@cloudscape-design/components";
import { useTheme } from "../hooks/useTheme";
import { getUserToken, removeUserToken } from "../api/client";
import { updateProfile } from "../api/user";

export default function UserLayout() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { theme, toggle: toggleTheme } = useTheme();

  const navItems: SideNavigationProps.Item[] = [
    { type: "link", text: t("user.nav.dashboard"), href: "/portal/dashboard" },
    { type: "link", text: t("user.nav.devices"), href: "/portal/devices" },
    { type: "link", text: t("user.nav.nodes"), href: "/portal/nodes" },
    { type: "link", text: t("user.nav.traffic"), href: "/portal/traffic" },
    { type: "link", text: t("user.nav.plan"), href: "/portal/plan" },
    { type: "link", text: t("user.nav.account"), href: "/portal/account" },
    { type: "link", text: t("user.nav.announcements"), href: "/portal/announcements" },
  ];

  const changeLanguage = (lng: string) => {
    void i18n.changeLanguage(lng);
    // Subscription output is rendered server-side for VPN clients, which never
    // see this browser's i18n state - the switch has to reach the account.
    if (getUserToken()) {
      void updateProfile({ language: lng });
    }
  };

  return (
    <>
      <TopNavigation
        id="user-top-navigation"
        identity={{
          href: "/portal/dashboard",
          title: "Proxima VPN",
        }}
        utilities={[
          {
            type: "button",
            text: t("user.nav.logout"),
            onClick: () => { removeUserToken(); navigate("/login", { replace: true }); },
          },
          {
            type: "button",
            iconName: "light-dark",
            ariaLabel: t(theme === "dark" ? "user.nav.lightMode" : "user.nav.darkMode"),
            onClick: toggleTheme,
          },
          {
            type: "menu-dropdown",
            text: i18n.resolvedLanguage === "ko" ? "한국어" : i18n.resolvedLanguage === "zh" ? "中文" : "English",
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
        headerSelector="#user-top-navigation"
        contentType="default"
        maxContentWidth={1200}
        ariaLabels={{
          navigation: t("user.nav.navigation"),
          navigationToggle: t("user.nav.openNavigation"),
          navigationClose: t("user.nav.closeNavigation"),
        }}
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
