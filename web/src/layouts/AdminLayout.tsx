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

interface NavGroup {
  id: string;
  title: string;
  links: { text: string; href: string }[];
}

// The nav link a path belongs to: the longest link href the path equals or sits
// under, so a detail page such as /admin/users/:id still highlights "Users".
function owningHref(hrefs: readonly string[], pathname: string): string {
  let best = "";
  for (const href of hrefs) {
    if ((pathname === href || pathname.startsWith(href + "/")) && href.length > best.length) best = href;
  }
  return best || pathname;
}

const rootCrumb = { labelKey: "admin.nav.serviceName", href: "/admin/dashboard" } as const;

// Deliberately an allowlist: deriving labels from path segments instead would
// surface a raw segment like "node-groups" as user-visible text on any route
// nobody remembered to label.
//
// `dynamic` marks a crumb whose text is the leaf the page publishes (a record
// name the route only carries as an opaque id) rather than a translation key.
const ancestorTrails: Record<
  string,
  readonly { labelKey?: string; dynamic?: boolean; href: string }[]
> = {
  "/admin/nodes/:nodeId": [rootCrumb, { labelKey: "admin.nav.nodes", href: "/admin/nodes" }],
  "/admin/nodes/:nodeId/inbounds": [
    rootCrumb,
    { labelKey: "admin.nav.nodes", href: "/admin/nodes" },
    { dynamic: true, href: "/admin/nodes/:nodeId" },
  ],
  "/admin/users/:userId": [rootCrumb, { labelKey: "admin.nav.users", href: "/admin/users" }],
  "/admin/orders/:orderId": [rootCrumb, { labelKey: "admin.nav.orders", href: "/admin/orders" }],
  "/admin/support/:ticketId": [rootCrumb, { labelKey: "admin.nav.support", href: "/admin/support" }],
};

const nestedLeafLabels: Record<string, string> = {
  "/admin/nodes/:nodeId/inbounds": "admin.nav.inbounds",
};

const staticPageLabels: Record<string, string> = {
  "/admin/dashboard": "admin.nav.dashboard",
  "/admin/alerts": "admin.nav.alerts",
  "/admin/activity": "admin.nav.activity",
  "/admin/nodes": "admin.nav.nodes",
  "/admin/node-groups": "admin.nav.nodeGroups",
  "/admin/node-chains": "admin.nav.nodeChains",
  "/admin/users": "admin.nav.users",
  "/admin/connections": "admin.nav.connections",
  "/admin/uuid-evictions": "admin.nav.uuidEvictions",
  "/admin/subscription-domains": "admin.nav.subscriptionDomains",
  "/admin/routing": "admin.nav.routing",
  "/admin/plans": "admin.nav.plans",
  "/admin/orders": "admin.nav.orders",
  "/admin/promotions": "admin.nav.promotions",
  "/admin/reset-tickets": "admin.nav.resetTickets",
  "/admin/announcements": "admin.nav.announcements",
  "/admin/support": "admin.nav.support",
  "/admin/support/help": "admin.nav.supportHelp",
  "/admin/settings": "admin.nav.settings",
  "/admin/2fa": "admin.nav.twoFactor",
};

const helpTopicKeys: Record<string, string> = {
  "/admin/dashboard": "admin.help.dashboard",
  "/admin/alerts": "admin.help.alerts",
  "/admin/activity": "admin.help.activity",
  "/admin/nodes": "admin.help.nodes",
  "/admin/node-groups": "admin.help.nodeGroups",
  "/admin/node-chains": "admin.help.nodeChains",
  "/admin/users": "admin.help.users",
  "/admin/connections": "admin.help.connections",
  "/admin/uuid-evictions": "admin.help.uuidEvictions",
  "/admin/subscription-domains": "admin.help.subscriptionDomains",
  "/admin/routing": "admin.help.routing",
  "/admin/plans": "admin.help.plans",
  "/admin/orders": "admin.help.orders",
  "/admin/promotions": "admin.help.promotions",
  "/admin/reset-tickets": "admin.help.resetTickets",
  "/admin/announcements": "admin.help.announcements",
  "/admin/support": "admin.help.support",
  "/admin/support/help": "admin.help.supportHelp",
  "/admin/settings": "admin.help.settings",
  "/admin/2fa": "admin.help.twoFactor",
};

function indicatorType(severity: AlertSeverity) {
  return severity === "info" ? "info" : severity;
}

// Substitutes every :param in an ancestor href with the value the current path
// holds at the same position. Keyed off the matched pattern rather than a fixed
// segment index so a new detail route needs no change here.
function resolveCrumbHref(href: string, pattern: string, pathname: string): string {
  const actual = pathname.split("/").filter(Boolean);
  const declared = pattern.split("/").filter(Boolean);
  return href
    .split("/")
    .map((part) => {
      if (!part.startsWith(":")) return part;
      const at = declared.indexOf(part);
      return at === -1 ? part : (actual[at] ?? part);
    })
    .join("/");
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
  const [openGroups, setOpenGroups] = useState<ReadonlySet<string>>(new Set());
  const [foldedGroups, setFoldedGroups] = useState<ReadonlySet<string>>(new Set());
  const [seenActiveGroup, setSeenActiveGroup] = useState<string | undefined>(undefined);

  const alertCount = alerts?.total ?? 0;


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

  const dashboardLink = { text: t("admin.nav.dashboard"), href: "/admin/dashboard" };
  const alertsLink = { text: t("admin.nav.alerts"), href: "/admin/alerts" };

  // Grouped by the job an operator is doing. Alerts sit outside the groups so
  // their badge stays visible while every group is folded.
  const navGroups: NavGroup[] = [
    {
      id: "monitoring",
      title: t("admin.nav.group.monitoring"),
      links: [
        { text: t("admin.nav.activity"), href: "/admin/activity" },
        { text: t("admin.nav.connections"), href: "/admin/connections" },
        { text: t("admin.nav.uuidEvictions"), href: "/admin/uuid-evictions" },
      ],
    },
    {
      id: "infrastructure",
      title: t("admin.nav.group.infrastructure"),
      links: [
        { text: t("admin.nav.nodes"), href: "/admin/nodes" },
        { text: t("admin.nav.nodeGroups"), href: "/admin/node-groups" },
        { text: t("admin.nav.nodeChains"), href: "/admin/node-chains" },
        { text: t("admin.nav.routing"), href: "/admin/routing" },
        { text: t("admin.nav.subscriptionDomains"), href: "/admin/subscription-domains" },
      ],
    },
    {
      id: "sales",
      title: t("admin.nav.group.sales"),
      links: [
        { text: t("admin.nav.users"), href: "/admin/users" },
        { text: t("admin.nav.plans"), href: "/admin/plans" },
        { text: t("admin.nav.orders"), href: "/admin/orders" },
        { text: t("admin.nav.promotions"), href: "/admin/promotions" },
        { text: t("admin.nav.resetTickets"), href: "/admin/reset-tickets" },
      ],
    },
    {
      id: "support",
      title: t("admin.nav.group.support"),
      links: [
        { text: t("admin.nav.support"), href: "/admin/support" },
        { text: t("admin.nav.supportHelp"), href: "/admin/support/help" },
        { text: t("admin.nav.announcements"), href: "/admin/announcements" },
        { text: t("admin.nav.guides"), href: "/admin/guides" },
      ],
    },
    {
      id: "system",
      title: t("admin.nav.group.system"),
      links: [
        { text: t("admin.nav.settings"), href: "/admin/settings" },
        { text: t("admin.nav.twoFactor"), href: "/admin/2fa" },
      ],
    },
  ];

  const activeHref = owningHref(
    [dashboardLink, alertsLink, ...navGroups.flatMap((group) => group.links)].map((link) => link.href),
    location.pathname,
  );
  const activeGroupId = navGroups.find((group) => group.links.some((link) => link.href === activeHref))?.id;

  // Arriving at a page re-opens its group even if the user folded it earlier.
  if (activeGroupId !== seenActiveGroup) {
    setSeenActiveGroup(activeGroupId);
    if (activeGroupId && foldedGroups.has(activeGroupId)) {
      setFoldedGroups((previous) => {
        const next = new Set(previous);
        next.delete(activeGroupId);
        return next;
      });
    }
  }

  const navItems: SideNavigationProps.Item[] = [
    { type: "link", text: dashboardLink.text, href: dashboardLink.href },
    {
      type: "link",
      text: alertsLink.text,
      href: alertsLink.href,
      info: alertCount > 0 ? <Badge color="red">{alertCount}</Badge> : undefined,
    },
    { type: "divider" },
    ...navGroups.map((group): SideNavigationProps.Section => ({
      type: "section",
      text: group.title,
      defaultExpanded: (group.id === activeGroupId && !foldedGroups.has(group.id)) || openGroups.has(group.id),
      items: group.links.map((link) => ({ type: "link", text: link.text, href: link.href })),
    })),
  ];

  const trackGroupToggle: NonNullable<SideNavigationProps["onChange"]> = ({ detail }) => {
    const first = detail.item.items[0];
    const group = first?.type === "link" ? navGroups.find((candidate) => candidate.links[0]?.href === first.href) : undefined;
    if (!group) return;
    const toggle = (previous: ReadonlySet<string>, add: boolean) => {
      const next = new Set(previous);
      if (add) next.add(group.id);
      else next.delete(group.id);
      return next;
    };
    setOpenGroups((previous) => toggle(previous, detail.expanded));
    setFoldedGroups((previous) => toggle(previous, !detail.expanded));
  };

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
            text: crumb.dynamic ? breadcrumbLeaf : t(crumb.labelKey ?? ""),
            href: resolveCrumbHref(crumb.href, trailPattern, location.pathname),
          })),
          {
            text: nestedLeafLabels[trailPattern]
              ? t(nestedLeafLabels[trailPattern])
              : breadcrumbLeaf,
            href: location.pathname,
          },
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
                    {t(`admin.dashboard.alert.${item.kind}`, { count: item.count, defaultValue: item.kind })}
                  </StatusIndicator>
                  <Box variant="small" color="text-body-secondary">
                    {t(`admin.dashboard.alert.${item.kind}_desc`, { defaultValue: item.kind })}
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
            key={activeGroupId ?? "none"}
            activeHref={activeHref}
            items={navItems}
            onChange={trackGroupToggle}
            onFollow={(event) => {
              event.preventDefault();
              navigate(event.detail.href);
            }}
          />
        }
        drawers={drawers}
        contentType="dashboard"
        maxContentWidth={location.pathname === "/admin/activity" ? 1600 : 1200}
        content={
          <Box padding={{ top: "xs" }}>
            <Outlet />
          </Box>
        }
      />
    </>
  );
}
