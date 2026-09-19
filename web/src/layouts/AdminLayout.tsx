import { useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  AppLayoutToolbar,
  type AppLayoutToolbarProps,
  Badge,
  Box,
  BreadcrumbGroup,
  type BreadcrumbGroupProps,
  HelpPanel,
  Link,
  SideNavigation,
  type SideNavigationProps,
  SpaceBetween,
  StatusIndicator,
  TopNavigation,
} from "@cloudscape-design/components";
import { useTheme } from "../hooks/useTheme";
import { BreadcrumbLeafProvider } from "../hooks/BreadcrumbLeafProvider";
import { useBreadcrumbLeafValue } from "../hooks/useBreadcrumbLeaf";
import { getDashboardAlerts } from "../api/admin";
import type { AlertSeverity, DashboardAlerts } from "../api/types";

const badgePollInterval = 60000;

const rootCrumb = { labelKey: "admin.nav.serviceName", href: "/admin/dashboard" } as const;

// Deliberately an allowlist: deriving labels from path segments instead would
// surface a raw segment like "node-groups" as user-visible text on any route
// nobody remembered to label.
const ancestorTrails: Record<string, readonly { labelKey: string; href: string }[]> = {
  "/admin/nodes/:nodeId": [rootCrumb, { labelKey: "admin.nav.nodes", href: "/admin/nodes" }],
  "/admin/nodes/:nodeId/inbounds": [
    rootCrumb,
    { labelKey: "admin.nav.nodes", href: "/admin/nodes" },
  ],
};

const staticPageLabels: Record<string, string> = {
  "/admin/dashboard": "admin.nav.dashboard",
  "/admin/nodes": "admin.nav.nodes",
  "/admin/node-groups": "admin.nav.nodeGroups",
  "/admin/users": "admin.nav.users",
  "/admin/connections": "admin.nav.connections",
  "/admin/user-templates": "admin.nav.userTemplates",
  "/admin/plans": "admin.nav.plans",
  "/admin/plan-requests": "admin.nav.planRequests",
  "/admin/announcements": "admin.nav.announcements",
  "/admin/settings": "admin.nav.settings",
  "/admin/2fa": "admin.nav.twoFactor",
};

const helpTopicKeys: Record<string, string> = {
  "/admin/dashboard": "admin.help.dashboard",
  "/admin/nodes": "admin.help.nodes",
  "/admin/node-groups": "admin.help.nodeGroups",
  "/admin/users": "admin.help.users",
  "/admin/connections": "admin.help.connections",
  "/admin/user-templates": "admin.help.userTemplates",
  "/admin/plans": "admin.help.plans",
  "/admin/plan-requests": "admin.help.planRequests",
  "/admin/announcements": "admin.help.announcements",
  "/admin/settings": "admin.help.settings",
  "/admin/2fa": "admin.help.twoFactor",
};

function indicatorType(severity: AlertSeverity) {
  return severity === "info" ? "info" : severity;
}

function matchTrailPattern(pathname: string): string | null {
  const segments = pathname.split("/").filter(Boolean);
  for (const pattern of Object.keys(ancestorTrails)) {
    const patternSegments = pattern.split("/").filter(Boolean);
    if (patternSegments.length !== segments.length) continue;
    const matches = patternSegments.every(
      (part, i) => part.startsWith(":") || part === segments[i],
    );
    if (matches) return pattern;
  }
  return null;
}

export default function AdminLayout() {
  return (
    <BreadcrumbLeafProvider>
      <AdminLayoutShell />
    </BreadcrumbLeafProvider>
  );
}

function AdminLayoutShell() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { theme, toggle: toggleTheme } = useTheme();
  const breadcrumbLeaf = useBreadcrumbLeafValue();
  const [alerts, setAlerts] = useState<DashboardAlerts | null>(null);

  const alertCount = alerts?.total ?? 0;
  const pendingCount = alerts?.pending_requests ?? 0;

  useEffect(() => {
    const load = async () => {
      try {
        setAlerts(await getDashboardAlerts());
      } catch {
        // intentionally ignored
      }
    };
    void load();
    const interval = setInterval(() => void load(), badgePollInterval);
    return () => clearInterval(interval);
  }, []);

  const navItems: SideNavigationProps.Item[] = [
    {
      type: "link",
      text: t("admin.nav.dashboard"),
      href: "/admin/dashboard",
      info: alertCount > 0 ? <Badge color="red">{alertCount}</Badge> : undefined,
    },
    {
      type: "section-group",
      title: t("admin.nav.group.infrastructure"),
      items: [
        { type: "link", text: t("admin.nav.nodes"), href: "/admin/nodes" },
        { type: "link", text: t("admin.nav.nodeGroups"), href: "/admin/node-groups" },
      ],
    },
    {
      type: "section-group",
      title: t("admin.nav.group.customers"),
      items: [
        { type: "link", text: t("admin.nav.users"), href: "/admin/users" },
        { type: "link", text: t("admin.nav.connections"), href: "/admin/connections" },
        { type: "link", text: t("admin.nav.userTemplates"), href: "/admin/user-templates" },
        { type: "link", text: t("admin.nav.plans"), href: "/admin/plans" },
        {
          type: "link",
          text: t("admin.nav.planRequests"),
          href: "/admin/plan-requests",
          info: pendingCount > 0 ? <Badge color="blue">{pendingCount}</Badge> : undefined,
        },
        { type: "link", text: t("admin.nav.announcements"), href: "/admin/announcements" },
      ],
    },
    {
      type: "section-group",
      title: t("admin.nav.group.system"),
      items: [
        { type: "link", text: t("admin.nav.settings"), href: "/admin/settings" },
        { type: "link", text: t("admin.nav.twoFactor"), href: "/admin/2fa" },
      ],
    },
  ];

  const changeLanguage = (lng: string) => {
    void i18n.changeLanguage(lng);
  };

  const languageLabel =
    i18n.language === "ko" ? "한국어" : i18n.language === "zh" ? "中文" : "English";

  const trailPattern = matchTrailPattern(location.pathname);
  const staticLabelKey = staticPageLabels[location.pathname];

  const breadcrumbItems: BreadcrumbGroupProps.Item[] = staticLabelKey
    ? [
        { text: t(rootCrumb.labelKey), href: rootCrumb.href },
        { text: t(staticLabelKey), href: location.pathname },
      ]
    : trailPattern && breadcrumbLeaf
      ? [
          ...(ancestorTrails[trailPattern] ?? []).map((crumb) => ({
            text: t(crumb.labelKey),
            href: crumb.href,
          })),
          { text: breadcrumbLeaf, href: location.pathname },
        ]
      : [];

  const helpTopicKey = helpTopicKeys[location.pathname];

  const drawers: AppLayoutToolbarProps.Drawer[] = [
    {
      id: "info",
      trigger: { iconName: "status-info" },
      ariaLabels: {
        drawerName: t("admin.help.title"),
        closeButton: t("admin.help.close"),
        triggerButton: t("admin.help.title"),
      },
      resizable: true,
      defaultSize: 320,
      content: (
        <HelpPanel header={<h2>{t("admin.help.title")}</h2>}>
          <SpaceBetween size="m">
            <Box variant="p">
              {helpTopicKey ? t(helpTopicKey) : t("admin.help.fallback")}
            </Box>
            <Link external href="https://github.com/XTLS/Xray-core">
              Xray-core
            </Link>
          </SpaceBetween>
        </HelpPanel>
      ),
    },
    {
      id: "notifications",
      trigger: { iconName: "notification" },
      badge: alertCount > 0,
      ariaLabels: {
        drawerName: t("admin.notifications.title"),
        closeButton: t("admin.help.close"),
        triggerButton: t("admin.notifications.title"),
      },
      resizable: true,
      defaultSize: 340,
      content: (
        <HelpPanel header={<h2>{t("admin.notifications.title")}</h2>}>
          {alerts && alerts.items.length > 0 ? (
            <SpaceBetween size="m">
              {alerts.items.map((item) => (
                <SpaceBetween key={item.kind} size="xxxs">
                  <StatusIndicator type={indicatorType(item.severity)}>
                    {t(`admin.dashboard.alert.${item.kind}`, { count: item.count })}
                  </StatusIndicator>
                  <Box variant="small" color="text-body-secondary">
                    {t(`admin.dashboard.alert.${item.kind}_desc`)}
                  </Box>
                </SpaceBetween>
              ))}
              <Link
                href="/admin/dashboard"
                onFollow={(event) => {
                  event.preventDefault();
                  navigate("/admin/dashboard");
                }}
              >
                {t("admin.dashboard.viewAll")}
              </Link>
            </SpaceBetween>
          ) : (
            <StatusIndicator type="success">{t("admin.dashboard.noAlerts")}</StatusIndicator>
          )}
        </HelpPanel>
      ),
    },
  ];

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
            text: languageLabel,
            items: [
              { id: "ko", text: "한국어" },
              { id: "en", text: "English" },
              { id: "zh", text: "中文" },
            ],
            onItemClick: ({ detail }) => changeLanguage(detail.id),
          },
        ]}
      />
      <AppLayoutToolbar
        breadcrumbs={
          breadcrumbItems.length > 0 ? (
            <BreadcrumbGroup
              items={breadcrumbItems}
              onFollow={(event) => {
                event.preventDefault();
                navigate(event.detail.href);
              }}
            />
          ) : undefined
        }
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
        drawers={drawers}
        contentType="dashboard"
        maxContentWidth={1200}
        content={
          <Box padding={{ top: "xs" }}>
            <Outlet />
          </Box>
        }
      />
    </>
  );
}
