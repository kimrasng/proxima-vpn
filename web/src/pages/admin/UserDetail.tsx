import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  ColumnLayout,
  Container,
  ContentLayout,
  DatePicker,
  Flashbar,
  Form,
  FormField,
  Header,
  Input,
  KeyValuePairs,
  ProgressBar,
  SegmentedControl,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  type StatusIndicatorProps,
  Table,
  Toggle,
} from "@cloudscape-design/components";
import {
  getUser,
  getUserLoginHistory,
  getUserTraffic,
  listPlans,
  resetUserTraffic,
  updateUser,
} from "../../api/admin";
import type {
  LoginHistoryEntry,
  Plan,
  TrafficWindow,
  UpdateUserRequest,
  UserDetail as UserDetailData,
  UserTraffic,
} from "../../api/types";
import { usePublishBreadcrumbLeaf } from "../../hooks/useBreadcrumbLeaf";
import { useManualRefresh } from "../../hooks/useManualRefresh";
import { formatBytes } from "../../utils/format";
import { formatAbsoluteTime, formatTime } from "../../utils/relativeTime";

const REFRESH_INTERVAL = 30000;
const LOGIN_HISTORY_LIMIT = 50;

const editableStatuses = ["active", "suspended", "expired", "pending"] as const;

const trafficWindows: readonly TrafficWindow[] = ["today", "week", "month"];

function isTrafficWindow(value: string): value is TrafficWindow {
  return (trafficWindows as readonly string[]).includes(value);
}

// Mirrors the list page: a disabled account denies service whatever its status
// column says, so is_active takes precedence over the status severity.
function statusIndicatorType(status: string, isActive: boolean): StatusIndicatorProps.Type {
  if (!isActive) return "error";
  switch (status) {
    case "active":
      return "success";
    case "pending":
      return "pending";
    case "expired":
      return "warning";
    case "suspended":
      return "error";
    default:
      return "info";
  }
}

interface EditForm {
  name: string;
  status: string;
  plan_id: string;
  plan_expires_at: string;
  is_active: boolean;
}

// Keyed on the route param so navigating between two user detail pages remounts
// rather than reusing the instance. Without this the edit form, traffic, and
// login history survive the id change and the page shows one user's data under
// another user's header - and a save would write it to the wrong account.
export default function UserDetail() {
  const { userId } = useParams<{ userId: string }>();
  return <UserDetailPage key={userId ?? ""} />;
}

function UserDetailPage() {
  const { userId } = useParams<{ userId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();

  const [user, setUser] = useState<UserDetailData | null>(null);
  const [traffic, setTraffic] = useState<UserTraffic | null>(null);
  const [logins, setLogins] = useState<LoginHistoryEntry[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [form, setForm] = useState<EditForm | null>(null);
  const [window, setWindow] = useState<TrafficWindow>("month");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [trafficError, setTrafficError] = useState<string | null>(null);
  const [loginError, setLoginError] = useState<string | null>(null);
  const [flash, setFlash] = useState<{ type: "success" | "error"; content: string }[]>([]);

  usePublishBreadcrumbLeaf(user?.name || user?.email);

  const fetchAll = useCallback(async () => {
    if (!userId) return;
    try {
      // Traffic and login history each degrade to their own error marker: a
      // failure in either must not blank the identity card or the edit form.
      const [detail, plansData, trafficResult, loginResult] = await Promise.all([
        getUser(userId),
        listPlans().then(
          (data) => data,
          () => [] as Plan[],
        ),
        getUserTraffic(userId, window).then(
          (data) => ({ ok: true as const, data }),
          () => ({ ok: false as const, data: null }),
        ),
        getUserLoginHistory(userId, LOGIN_HISTORY_LIMIT).then(
          (entries) => ({ ok: true as const, entries }),
          () => ({ ok: false as const, entries: [] as LoginHistoryEntry[] }),
        ),
      ]);

      setUser(detail);
      setPlans(plansData);

      // The form is seeded once. Re-seeding on every poll would discard whatever
      // the admin has typed while the interval fires underneath them.
      setForm((current) =>
        current ?? {
          name: detail.name,
          status: detail.status,
          plan_id: detail.plan_id ?? "",
          plan_expires_at: detail.plan_expires_at
            ? (detail.plan_expires_at.split("T")[0] ?? "")
            : "",
          is_active: detail.is_active,
        },
      );

      if (trafficResult.ok) {
        setTraffic(trafficResult.data);
        setTrafficError(null);
      } else {
        setTrafficError(t("admin.userDetail.trafficFetchError"));
      }

      if (loginResult.ok) {
        setLogins(loginResult.entries);
        setLoginError(null);
      } else {
        setLoginError(t("admin.userDetail.loginHistoryFetchError"));
      }

      setError(null);
    } catch {
      setError(t("admin.userDetail.fetchError"));
    } finally {
      setLoading(false);
    }
  }, [userId, window, t]);

  const { refreshing, lastUpdated, refresh, markUpdated } = useManualRefresh(
    fetchAll,
    REFRESH_INTERVAL,
  );

  const planOptions = useMemo(
    () => [
      { label: t("admin.users.noPlan"), value: "" },
      ...plans.map((p) => ({ label: p.name, value: p.id })),
    ],
    [plans, t],
  );

  const statusOptions = useMemo(
    () => editableStatuses.map((s) => ({ label: t(`admin.users.status.${s}`), value: s })),
    [t],
  );

  const handleSave = async () => {
    if (!userId || !form || !user) return;
    setSaving(true);
    try {
      // Only changed fields are sent. The form is a snapshot taken when the page
      // loaded, so submitting all of it would push stale values back and revert
      // whatever another admin - or the expiry job - changed in the meantime.
      const patch: UpdateUserRequest = {};
      if (form.name !== user.name) patch.name = form.name;
      if (form.status !== user.status) patch.status = form.status;
      if (form.is_active !== user.is_active) patch.is_active = form.is_active;
      if (form.plan_id !== (user.plan_id ?? "")) patch.plan_id = form.plan_id || undefined;

      const currentExpiry = user.plan_expires_at ? (user.plan_expires_at.split("T")[0] ?? "") : "";
      if (form.plan_expires_at !== currentExpiry) {
        patch.plan_expires_at = form.plan_expires_at
          ? new Date(form.plan_expires_at).toISOString()
          : undefined;
      }

      if (Object.keys(patch).length === 0) {
        setFlash([{ type: "success", content: t("admin.userDetail.saveSuccess") }]);
        return;
      }

      await updateUser(userId, patch);
      await fetchAll();
      markUpdated();
      setFlash([{ type: "success", content: t("admin.userDetail.saveSuccess") }]);
    } catch {
      setFlash([{ type: "error", content: t("admin.userDetail.saveError") }]);
    } finally {
      setSaving(false);
    }
  };

  const handleResetTraffic = async () => {
    if (!userId) return;
    setSaving(true);
    try {
      await resetUserTraffic(userId);
      await fetchAll();
      markUpdated();
      setFlash([{ type: "success", content: t("admin.users.resetTrafficSuccess") }]);
    } catch {
      setFlash([{ type: "error", content: t("admin.users.resetTrafficError") }]);
    } finally {
      setSaving(false);
    }
  };

  useEffect(() => {
    if (!loading && !user && !error) {
      setError(t("admin.userDetail.notFound"));
    }
  }, [loading, user, error, t]);

  if (loading && !user) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.users.title")}</Header>}>
        <Box textAlign="center" padding="xxl">
          <Spinner size="large" />
        </Box>
      </ContentLayout>
    );
  }

  if (!user || !form) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.users.title")}</Header>}>
        <SpaceBetween size="l">
          <Alert type="error" header={t("admin.userDetail.notFound")}>
            <Button onClick={() => void navigate("/admin/users")}>
              {t("admin.userDetail.backToList")}
            </Button>
          </Alert>
        </SpaceBetween>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                iconName="refresh"
                ariaLabel={t("admin.userDetail.refresh")}
                loading={refreshing}
                onClick={refresh}
              />
              <Button loading={saving} onClick={() => void handleResetTraffic()}>
                {t("admin.userDetail.resetTraffic")}
              </Button>
            </SpaceBetween>
          }
          description={
            lastUpdated ? `${t("admin.nodeDetail.lastRefreshed")}: ${formatTime(lastUpdated)}` : undefined
          }
        >
          {user.name || user.email}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {flash.length > 0 && (
          <Flashbar
            items={flash.map((f) => ({
              type: f.type,
              content: f.content,
              dismissible: true,
              onDismiss: () => setFlash([]),
            }))}
          />
        )}
        {error && (
          <Flashbar
            items={[
              { type: "error", content: error, dismissible: true, onDismiss: () => setError(null) },
            ]}
          />
        )}

        <ColumnLayout columns={2} minColumnWidth={400}>
          <Container
            fitHeight
            header={<Header variant="h2">{t("admin.userDetail.identity")}</Header>}
          >
            <KeyValuePairs
              columns={2}
              items={[
                { label: t("admin.users.col.email"), value: user.email },
                {
                  label: t("admin.users.col.status"),
                  value: (
                    <StatusIndicator type={statusIndicatorType(user.status, user.is_active)}>
                      {!user.is_active && user.status === "active"
                        ? t("admin.users.status.disabled")
                        : t(`admin.users.status.${user.status}`, { defaultValue: user.status })}
                    </StatusIndicator>
                  ),
                },
                {
                  label: t("admin.users.col.plan"),
                  value: user.plan_name ?? t("admin.users.noPlan"),
                },
                { label: t("admin.userDetail.createdAt"), value: formatAbsoluteTime(user.created_at) },
                {
                  label: t("admin.userDetail.planExpiresAt"),
                  value: user.plan_expires_at
                    ? formatAbsoluteTime(user.plan_expires_at)
                    : t("admin.userDetail.none"),
                },
                { label: t("admin.userDetail.subToken"), value: user.sub_token },
              ]}
            />
          </Container>

          <Container
            fitHeight
            header={<Header variant="h2">{t("admin.userDetail.editSection")}</Header>}
          >
            <Form
              actions={
                <Button variant="primary" loading={saving} onClick={() => void handleSave()}>
                  {t("admin.userDetail.save")}
                </Button>
              }
            >
              <SpaceBetween size="m">
                <FormField label={t("admin.userDetail.nameLabel")}>
                  <Input
                    value={form.name}
                    onChange={({ detail }) => setForm((f) => (f ? { ...f, name: detail.value } : f))}
                  />
                </FormField>
                <FormField label={t("admin.userDetail.statusLabel")}>
                  <Select
                    selectedOption={statusOptions.find((o) => o.value === form.status) ?? null}
                    options={statusOptions}
                    onChange={({ detail }) =>
                      setForm((f) =>
                        f ? { ...f, status: detail.selectedOption.value ?? "active" } : f,
                      )
                    }
                  />
                </FormField>
                <FormField label={t("admin.userDetail.planLabel")}>
                  <Select
                    selectedOption={planOptions.find((o) => o.value === form.plan_id) ?? null}
                    options={planOptions}
                    onChange={({ detail }) =>
                      setForm((f) => (f ? { ...f, plan_id: detail.selectedOption.value ?? "" } : f))
                    }
                  />
                </FormField>
                <FormField label={t("admin.userDetail.expiresAtLabel")}>
                  <DatePicker
                    value={form.plan_expires_at}
                    placeholder="YYYY/MM/DD"
                    onChange={({ detail }) =>
                      setForm((f) => (f ? { ...f, plan_expires_at: detail.value } : f))
                    }
                  />
                </FormField>
                <FormField label={t("admin.userDetail.activeLabel")}>
                  <Toggle
                    checked={form.is_active}
                    onChange={({ detail }) =>
                      setForm((f) => (f ? { ...f, is_active: detail.checked } : f))
                    }
                  >
                    {form.is_active ? t("admin.users.activate") : t("admin.users.suspend")}
                  </Toggle>
                </FormField>
              </SpaceBetween>
            </Form>
          </Container>
        </ColumnLayout>

        <Container header={<Header variant="h2">{t("admin.userDetail.traffic")}</Header>}>
          <SpaceBetween size="m">
            {trafficError && <Alert type="error">{trafficError}</Alert>}
            <KeyValuePairs
              columns={4}
              items={[
                {
                  label: t("admin.userDetail.trafficUsed"),
                  value: formatBytes(traffic?.traffic_used ?? user.traffic_used),
                },
                {
                  label: t("admin.userDetail.trafficLimit"),
                  value: !traffic
                    ? "—"
                    : traffic.unlimited
                      ? t("admin.userDetail.unlimited")
                      : formatBytes(traffic.traffic_limit ?? 0),
                },
                {
                  label: t("admin.userDetail.trafficRemaining"),
                  value: !traffic
                    ? "—"
                    : traffic.unlimited
                      ? t("admin.userDetail.unlimited")
                      : formatBytes(traffic.traffic_remaining ?? 0),
                },
                {
                  label: t("admin.userDetail.trafficResetAt"),
                  value: user.traffic_reset_at
                    ? formatAbsoluteTime(user.traffic_reset_at)
                    : t("admin.userDetail.never"),
                },
              ]}
            />
            {traffic && !traffic.unlimited && (
              <ProgressBar
                value={traffic.percentage}
                label={t("admin.userDetail.trafficPercentage")}
                description={`${traffic.percentage.toFixed(1)}%`}
              />
            )}
          </SpaceBetween>
        </Container>

        <Container
          header={
            <Header
              variant="h2"
              actions={
                <SegmentedControl
                  selectedId={window}
                  options={trafficWindows.map((w) => ({
                    id: w,
                    text: t(`admin.userDetail.window.${w}`),
                  }))}
                  onChange={({ detail }) => {
                    if (isTrafficWindow(detail.selectedId)) setWindow(detail.selectedId);
                  }}
                />
              }
            >
              {t("admin.userDetail.byNode")}
            </Header>
          }
        >
          <SpaceBetween size="s">
            <Box variant="small" color="text-body-secondary">
              {t("admin.userDetail.byNodeHint")}
            </Box>
            <Table
              variant="embedded"
              items={traffic?.by_node ?? []}
              columnDefinitions={[
                {
                  id: "node",
                  header: t("admin.userDetail.nodeCol.node"),
                  cell: (item) => item.node_name,
                },
                {
                  id: "upload",
                  header: t("admin.userDetail.nodeCol.upload"),
                  cell: (item) => formatBytes(item.upload),
                },
                {
                  id: "download",
                  header: t("admin.userDetail.nodeCol.download"),
                  cell: (item) => formatBytes(item.download),
                },
                {
                  id: "total",
                  header: t("admin.userDetail.nodeCol.total"),
                  cell: (item) => formatBytes(item.total),
                },
              ]}
              empty={<Box textAlign="center">{t("admin.userDetail.byNodeEmpty")}</Box>}
            />
          </SpaceBetween>
        </Container>

        <ColumnLayout columns={2} minColumnWidth={420}>
          <Container
            fitHeight
            header={<Header variant="h2" counter={`(${user.devices.length})`}>{t("admin.userDetail.devices")}</Header>}
          >
            <Table
              variant="embedded"
              items={user.devices}
              columnDefinitions={[
                {
                  id: "name",
                  header: t("admin.userDetail.deviceCol.name"),
                  cell: (item) => item.name ?? "—",
                },
                {
                  id: "uuid",
                  header: t("admin.userDetail.deviceCol.uuid"),
                  cell: (item) => item.xray_uuid,
                },
                {
                  id: "created",
                  header: t("admin.userDetail.deviceCol.createdAt"),
                  cell: (item) => formatAbsoluteTime(item.created_at),
                },
              ]}
              empty={<Box textAlign="center">{t("admin.userDetail.devicesEmpty")}</Box>}
            />
          </Container>

          <Container
            fitHeight
            header={<Header variant="h2" counter={`(${logins.length})`}>{t("admin.userDetail.loginHistory")}</Header>}
          >
            <SpaceBetween size="s">
              {loginError && <Alert type="error">{loginError}</Alert>}
              <Table
                variant="embedded"
                items={logins}
                columnDefinitions={[
                  {
                    id: "time",
                    header: t("admin.userDetail.loginCol.time"),
                    cell: (item) => formatAbsoluteTime(item.created_at),
                  },
                  {
                    id: "ip",
                    header: t("admin.userDetail.loginCol.ip"),
                    cell: (item) => item.ip || "—",
                  },
                  {
                    id: "outcome",
                    header: t("admin.userDetail.loginCol.outcome"),
                    cell: (item) => (
                      <StatusIndicator type={item.success ? "success" : "error"}>
                        {item.success
                          ? t("admin.userDetail.loginSuccess")
                          : t(`admin.userDetail.reason.${item.failure_reason}`, {
                              defaultValue: t("admin.userDetail.loginFailure"),
                            })}
                      </StatusIndicator>
                    ),
                  },
                  {
                    id: "agent",
                    header: t("admin.userDetail.loginCol.agent"),
                    cell: (item) => item.user_agent || "—",
                  },
                ]}
                empty={<Box textAlign="center">{t("admin.userDetail.loginHistoryEmpty")}</Box>}
              />
            </SpaceBetween>
          </Container>
        </ColumnLayout>
      </SpaceBetween>
    </ContentLayout>
  );
}
