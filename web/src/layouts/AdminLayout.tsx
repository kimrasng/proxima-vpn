import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  AppLayout,
  SideNavigation,
  type SideNavigationProps,
  TopNavigation,
} from "@cloudscape-design/components";
import { useTheme } from "../hooks/useTheme";

export default function AdminLayout() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { theme, toggle: toggleTheme } = useTheme();

  const navItems: SideNavigationProps.Item[] = [
    { type: "link", text: t("admin.nav.dashboard"), href: "/admin/dashboard" },
    { type: "link", text: t("admin.nav.nodes"), href: "/admin/nodes" },
    { type: "link", text: t("admin.nav.nodeGroups"), href: "/admin/node-groups" },
    { type: "link", text: t("admin.nav.plans"), href: "/admin/plans" },
    { type: "link", text: t("admin.nav.userTemplates"), href: "/admin/user-templates" },
    { type: "link", text: t("admin.nav.users"), href: "/admin/users" },
    { type: "link", text: t("admin.nav.planRequests"), href: "/admin/plan-requests" },
    { type: "link", text: t("admin.nav.announcements"), href: "/admin/announcements" },
    { type: "divider" },
    { type: "link", text: t("admin.nav.settings"), href: "/admin/settings" },
    { type: "link", text: t("admin.nav.twoFactor"), href: "/admin/2fa" },
  ];

  const changeLanguage = (lng: string) => {
    void i18n.changeLanguage(lng);
  };

  return (
    <>
      <TopNavigation
        identity={{
          href: "/admin/dashboard",
          title: "Proxima VPN Admin",
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
            text: i18n.language === "ko" ? "한국어" : "English",
            items: [
              { id: "ko", text: "한국어" },
              { id: "en", text: "English" },
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
