import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Badge,
  Box,
  Button,
  Autosuggest,
  Checkbox,
  Container,
  ContentLayout,
  Flashbar,
  FormField,
  Header,
  Input,
  Modal,
  Select,
  SpaceBetween,
  Spinner,
  Table,
  Tabs,
  Textarea,
  Toggle,
  type SelectProps,
} from "@cloudscape-design/components";
import {
  getRoutingProfile,
  listNodeTags,
  listNodes,
  previewRoutingProfile,
  resetRoutingProfile,
  saveRoutingProfile,
} from "../../api/admin";
import { ApiError } from "../../api/client";
import type {
  ClashSettings,
  Node,
  NodeTagCount,
  RoutingFieldError,
  RoutingGroup,
  RoutingGroupType,
  RoutingMember,
  RoutingProfile,
  RoutingProvider,
  RoutingRule,
  RoutingRuleType,
  RoutingValidationError,
} from "../../api/types";
import { NODE_TAG_PATTERN, normalizeTag } from "../../components/NodeTagEditor";
import RuleSetCatalogModal from "./RuleSetCatalogModal";
import RuleSetViewerModal, {
  type RuleSetViewTarget,
} from "./RuleSetViewerModal";
import ClashSettingsPanel from "./ClashSettingsPanel";
import "./routing.css";

const KEY_PATTERN = /^[a-z0-9_-]{1,32}$/;
const RESERVED = new Set(["DIRECT", "REJECT", "MATCH", "GLOBAL", "PASS"]);
const RULE_TYPES: RoutingRuleType[] = [
  "RULE-SET",
  "DOMAIN",
  "DOMAIN-SUFFIX",
  "DOMAIN-KEYWORD",
  "IP-CIDR",
  "IP-CIDR6",
  "DST-PORT",
];
const NO_RESOLVE_TYPES = new Set<RoutingRuleType>([
  "RULE-SET",
  "IP-CIDR",
  "IP-CIDR6",
]);

type Editing<T> = { index: number | null; value: T } | null;

const DEFAULT_SETTINGS: ClashSettings = {
  general: {
    mixed_port: 7890,
    port: 0,
    socks_port: 0,
    allow_lan: true,
    bind_address: "*",
    mode: "rule",
    log_level: "warning",
    ipv6: false,
    unified_delay: true,
    tcp_concurrent: true,
    find_process_mode: "off",
    keep_alive_interval: 0,
    keep_alive_idle: 0,
    disable_keep_alive: false,
    external_controller: "127.0.0.1:9090",
    secret: "",
    store_selected: true,
    store_fake_ip: true,
    geodata_mode: false,
    geo_auto_update: false,
    geo_update_interval: 0,
  },
  dns: {
    enable: true,
    listen: "0.0.0.0:1053",
    ipv6: false,
    prefer_h3: false,
    use_hosts: true,
    use_system_hosts: false,
    cache_algorithm: "",
    enhanced_mode: "fake-ip",
    fake_ip_range: "198.18.0.1/16",
    fake_ip_filter_mode: "rule",
    fake_ip_filter: [],
    respect_rules: true,
    default_nameserver: ["1.1.1.1", "8.8.8.8"],
    nameserver: [
      "https://cloudflare-dns.com/dns-query",
      "https://dns.google/dns-query",
    ],
    fallback: [],
    proxy_server_nameserver: [
      "https://1.1.1.1/dns-query",
      "https://8.8.8.8/dns-query",
    ],
    direct_nameserver: ["system"],
    direct_nameserver_follow_policy: false,
    nameserver_policy: {},
    fallback_filter: { geoip: false, geoip_code: "", ipcidr: [], domain: [] },
  },
  sniffer: {
    enable: true,
    force_dns_mapping: true,
    parse_pure_ip: true,
    override_destination: false,
    http_ports: ["80", "8080-8880"],
    http_override_destination: false,
    tls_ports: ["443", "8443"],
    quic_ports: [],
    skip_domain: ["+.push.apple.com"],
    force_domain: [],
    skip_src_address: [],
    skip_dst_address: [],
  },
  hosts: {},
  extra_yaml: "",
};

function move<T>(list: T[], from: number, to: number): T[] {
  if (to < 0 || to >= list.length) return list;
  const next = [...list];
  const [item] = next.splice(from, 1);
  if (item === undefined) return list;
  next.splice(to, 0, item);
  return next;
}

function opt(value: string, label = value): SelectProps.Option {
  return { value, label };
}

function groupNameProblem(
  name: string,
  t: (k: string) => string,
): string | undefined {
  if (!name.trim() || [...name].length > 64)
    return t("admin.routing.err.nameLength");
  if (name !== name.trim()) return t("admin.routing.err.nameSpaces");
  if (/[,()\n\r]/.test(name)) return t("admin.routing.err.nameChars");
  if (RESERVED.has(name.toUpperCase()))
    return t("admin.routing.err.nameReserved");
  return undefined;
}

function valueProblem(
  value: string,
  t: (k: string) => string,
): string | undefined {
  if (!value || value.length > 253) return t("admin.routing.err.valueLength");
  if (value !== value.trim()) return t("admin.routing.err.valueSpaces");
  if (/[,\n\r]/.test(value)) return t("admin.routing.err.valueChars");
  return undefined;
}

function groupReferences(profile: RoutingProfile, key: string): string[] {
  const refs: string[] = [];
  profile.groups.forEach((g) => {
    if (
      g.key !== key &&
      g.members.some((m) => m.kind === "group" && m.ref === key)
    )
      refs.push(g.name);
  });
  profile.rules.forEach((r, i) => {
    if (r.target === key) refs.push(`#${i + 1} ${r.type},${r.value}`);
  });
  if (profile.final === key) refs.push("MATCH");
  return refs;
}

function providerReferences(profile: RoutingProfile, key: string): string[] {
  return profile.rules.flatMap((r, i) =>
    r.type === "RULE-SET" && r.value === key ? [`#${i + 1}`] : [],
  );
}

export default function Routing() {
  const { t } = useTranslation();
  const [profile, setProfile] = useState<RoutingProfile | null>(null);
  const [saved, setSaved] = useState<string>("");
  const [isDefault, setIsDefault] = useState(true);
  const [updatedAt, setUpdatedAt] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<RoutingFieldError[]>([]);
  const [success, setSuccess] = useState<string | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [confirmReset, setConfirmReset] = useState(false);
  const [blocked, setBlocked] = useState<string[] | null>(null);
  const [groupEdit, setGroupEdit] = useState<Editing<RoutingGroup>>(null);
  const [providerEdit, setProviderEdit] =
    useState<Editing<RoutingProvider>>(null);
  const [ruleEdit, setRuleEdit] = useState<Editing<RoutingRule>>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [tags, setTags] = useState<NodeTagCount[]>([]);
  const [catalogOpen, setCatalogOpen] = useState(false);
  const [tab, setTab] = useState("groups");
  const [viewing, setViewing] = useState<RuleSetViewTarget | null>(null);

  const apply = (res: {
    profile: RoutingProfile;
    is_default: boolean;
    updated_at: string | null;
  }) => {
    setProfile(res.profile);
    setSaved(JSON.stringify(res.profile));
    setIsDefault(res.is_default);
    setUpdatedAt(res.updated_at);
  };

  useEffect(() => {
    getRoutingProfile()
      .then(apply)
      .catch(() => setError(t("admin.routing.loadError")))
      .finally(() => setLoading(false));
    listNodes()
      .then((all) =>
        setNodes(
          all.filter((n) => n.role !== "relay" && n.status !== "pending"),
        ),
      )
      .catch(() => setNodes([]));
    listNodeTags()
      .then(setTags)
      .catch(() => setTags([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const dirty = profile !== null && JSON.stringify(profile) !== saved;

  const groupOptions = useMemo<SelectProps.Option[]>(
    () =>
      (profile?.groups ?? []).map((g) => opt(g.key, `${g.name} (${g.key})`)),
    [profile],
  );
  const targetOptions = useMemo<SelectProps.Option[]>(
    () => [...groupOptions, opt("DIRECT"), opt("REJECT")],
    [groupOptions],
  );
  const groupName = (key: string) =>
    key === "DIRECT" || key === "REJECT"
      ? key
      : (profile?.groups.find((g) => g.key === key)?.name ?? key);

  const reportServerError = (err: unknown, fallback: string) => {
    if (err instanceof ApiError && err.status === 400) {
      const body = err.body as Partial<RoutingValidationError> | undefined;
      if (body?.fields?.length) {
        setFieldErrors(body.fields);
        setError(t("admin.routing.validationError"));
        return;
      }
    }
    setFieldErrors([]);
    setError(fallback);
  };

  const handleSave = async () => {
    if (!profile) return;
    setBusy(true);
    try {
      apply(await saveRoutingProfile(profile));
      setFieldErrors([]);
      setError(null);
      setSuccess(t("admin.routing.saved"));
    } catch (err) {
      reportServerError(err, t("admin.routing.saveError"));
    } finally {
      setBusy(false);
    }
  };

  const handlePreview = async () => {
    if (!profile) return;
    setBusy(true);
    try {
      const res = await previewRoutingProfile(profile);
      setFieldErrors([]);
      setPreview(res.yaml);
    } catch (err) {
      reportServerError(err, t("admin.routing.previewError"));
    } finally {
      setBusy(false);
    }
  };

  const handleReset = async () => {
    setBusy(true);
    try {
      apply(await resetRoutingProfile());
      setConfirmReset(false);
      setFieldErrors([]);
      setError(null);
      setSuccess(t("admin.routing.resetDone"));
    } catch {
      setError(t("admin.routing.resetError"));
    } finally {
      setBusy(false);
    }
  };

  if (loading || !profile) {
    return (
      <ContentLayout
        header={<Header variant="h1">{t("admin.routing.title")}</Header>}
      >
        {error ? (
          <Flashbar items={[{ type: "error", content: error }]} />
        ) : (
          <Box textAlign="center" padding="xl">
            <Spinner size="large" />
          </Box>
        )}
      </ContentLayout>
    );
  }

  const update = (next: Partial<RoutingProfile>) =>
    setProfile({ ...profile, ...next });

  const memberLabel = (m: RoutingMember) => {
    switch (m.kind) {
      case "group":
        return groupName(m.ref);
      case "builtin":
        return m.value;
      case "tag": {
        const n = tags.find((x) => x.tag === m.tag)?.nodes ?? 0;
        return t("admin.routing.tagMember", { tag: m.tag, count: n });
      }
      case "node":
        return (
          nodes.find((n) => n.id === m.node_id)?.name ??
          t("admin.routing.missingNode")
        );
      case "all":
        return t("admin.routing.allNodes");
    }
  };

  const deleteGroup = (index: number) => {
    const g = profile.groups[index];
    if (!g) return;
    const refs = groupReferences(profile, g.key);
    if (refs.length) {
      setBlocked(refs);
      return;
    }
    update({ groups: profile.groups.filter((_, i) => i !== index) });
  };

  const deleteProvider = (index: number) => {
    const p = profile.providers[index];
    if (!p) return;
    const refs = providerReferences(profile, p.key);
    if (refs.length) {
      setBlocked(refs);
      return;
    }
    update({ providers: profile.providers.filter((_, i) => i !== index) });
  };

  const reorderButtons = (
    index: number,
    length: number,
    onMove: (to: number) => void,
  ) => (
    <SpaceBetween direction="horizontal" size="xxs">
      <Button
        variant="icon"
        iconName="angle-up"
        ariaLabel={t("admin.routing.moveUp")}
        disabled={index === 0}
        onClick={() => onMove(index - 1)}
      />
      <Button
        variant="icon"
        iconName="angle-down"
        ariaLabel={t("admin.routing.moveDown")}
        disabled={index === length - 1}
        onClick={() => onMove(index + 1)}
      />
    </SpaceBetween>
  );

  const rowActions = (onEdit: () => void, onDelete: () => void) => (
    <SpaceBetween direction="horizontal" size="xs">
      <Button variant="inline-link" onClick={onEdit}>
        {t("common.edit")}
      </Button>
      <Button variant="inline-link" onClick={onDelete}>
        {t("common.delete")}
      </Button>
    </SpaceBetween>
  );

  type RuleRow =
    | { kind: "quic" }
    | { kind: "rule"; index: number; rule: RoutingRule }
    | { kind: "match" };
  const ruleRows: RuleRow[] = [
    ...(profile.block_quic ? [{ kind: "quic" } as RuleRow] : []),
    ...profile.rules.map(
      (rule, index) => ({ kind: "rule", index, rule }) as RuleRow,
    ),
    { kind: "match" },
  ];

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={t("admin.routing.description")}
          info={
            isDefault ? (
              <Badge color="grey">{t("admin.routing.defaultBadge")}</Badge>
            ) : (
              <Badge color="blue">{t("admin.routing.customBadge")}</Badge>
            )
          }
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button onClick={() => setConfirmReset(true)} disabled={busy}>
                {t("admin.routing.reset")}
              </Button>
              <Button onClick={() => void handlePreview()} disabled={busy}>
                {t("admin.routing.preview")}
              </Button>
              <Button
                variant="primary"
                onClick={() => void handleSave()}
                disabled={!dirty || busy}
                loading={busy}
              >
                {t("admin.routing.save")}
              </Button>
            </SpaceBetween>
          }
        >
          {t("admin.routing.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {error && (
          <Flashbar
            items={[
              {
                type: "error",
                dismissible: true,
                onDismiss: () => {
                  setError(null);
                  setFieldErrors([]);
                },
                content: (
                  <SpaceBetween size="xxs">
                    <span>{error}</span>
                    {fieldErrors.length > 0 && (
                      <ul className="routing-field-errors">
                        {fieldErrors.map((f) => (
                          <li key={f.path + f.message}>
                            <code>{f.path}</code> {f.message}
                          </li>
                        ))}
                      </ul>
                    )}
                  </SpaceBetween>
                ),
              },
            ]}
          />
        )}
        {success && (
          <Flashbar
            items={[
              {
                type: "success",
                content: success,
                dismissible: true,
                onDismiss: () => setSuccess(null),
              },
            ]}
          />
        )}

        <Tabs
          activeTabId={tab}
          onChange={({ detail }) => setTab(detail.activeTabId)}
          tabs={[
            {
              id: "settings",
              label: t("admin.routing.tabs.settings"),
              content: (
                <SpaceBetween size="l">
                  <Container
                    header={
                      <Header
                        variant="h2"
                        description={
                          updatedAt
                            ? t("admin.routing.updatedAt", {
                                date: new Date(updatedAt).toLocaleString(),
                              })
                            : t("admin.routing.notCustomised")
                        }
                      >
                        {t("admin.routing.general")}
                      </Header>
                    }
                  >
                    <div className="routing-general">
                      <FormField
                        label={t("admin.routing.blockQuic")}
                        description={t("admin.routing.blockQuicHint")}
                      >
                        <Toggle
                          checked={profile.block_quic}
                          onChange={({ detail }) =>
                            update({ block_quic: detail.checked })
                          }
                        >
                          {profile.block_quic
                            ? t("admin.routing.on")
                            : t("admin.routing.off")}
                        </Toggle>
                      </FormField>
                      <FormField
                        label={t("admin.routing.final")}
                        description={t("admin.routing.finalHint")}
                      >
                        <Select
                          selectedOption={
                            groupOptions.find(
                              (o) => o.value === profile.final,
                            ) ?? null
                          }
                          options={groupOptions}
                          onChange={({ detail }) =>
                            update({
                              final:
                                detail.selectedOption.value ?? profile.final,
                            })
                          }
                        />
                      </FormField>
                    </div>
                  </Container>
                  <ClashSettingsPanel
                    key={saved}
                    value={profile.settings ?? DEFAULT_SETTINGS}
                    quicBlocked={profile.block_quic}
                    onChange={(settings) => update({ settings })}
                  />
                </SpaceBetween>
              ),
            },
            {
              id: "groups",
              label: t("admin.routing.tabs.groups", {
                count: profile.groups.length,
              }),
              content: (
                <div className="routing-table">
                  <Table
                    variant="container"
                    wrapLines
                    items={profile.groups}
                    header={
                      <Header
                        variant="h2"
                        counter={`(${profile.groups.length})`}
                        description={t("admin.routing.groupsHint")}
                        actions={
                          <Button
                            onClick={() =>
                              setGroupEdit({
                                index: null,
                                value: {
                                  key: "",
                                  name: "",
                                  type: "select",
                                  hidden: false,
                                  members: [
                                    { kind: "builtin", value: "DIRECT" },
                                  ],
                                },
                              })
                            }
                          >
                            {t("admin.routing.addGroup")}
                          </Button>
                        }
                      >
                        {t("admin.routing.groups")}
                      </Header>
                    }
                    columnDefinitions={[
                      {
                        id: "order",
                        header: "",
                        width: 84,
                        cell: (g) => {
                          const i = profile.groups.indexOf(g);
                          return reorderButtons(
                            i,
                            profile.groups.length,
                            (to) =>
                              update({ groups: move(profile.groups, i, to) }),
                          );
                        },
                      },
                      {
                        id: "name",
                        header: t("admin.routing.name"),
                        cell: (g) => (
                          <SpaceBetween size="xxxs">
                            <Box fontWeight="bold">{g.name}</Box>
                            <Box variant="small" color="text-body-secondary">
                              {g.key}
                              {g.hidden
                                ? ` · ${t("admin.routing.hidden")}`
                                : ""}
                            </Box>
                          </SpaceBetween>
                        ),
                      },
                      {
                        id: "type",
                        header: t("admin.routing.type"),
                        cell: (g) => t(`admin.routing.groupType.${g.type}`),
                      },
                      {
                        id: "members",
                        header: t("admin.routing.members"),
                        cell: (g) => (
                          <div className="routing-chips">
                            {g.members.map((m, i) => (
                              <span
                                key={i}
                                className={`routing-chip routing-chip--${m.kind}`}
                              >
                                {memberLabel(m)}
                              </span>
                            ))}
                          </div>
                        ),
                      },
                      {
                        id: "actions",
                        header: t("common.actions"),
                        cell: (g) => {
                          const i = profile.groups.indexOf(g);
                          return rowActions(
                            () =>
                              setGroupEdit({
                                index: i,
                                value: structuredClone(g),
                              }),
                            () => deleteGroup(i),
                          );
                        },
                      },
                    ]}
                  />
                </div>
              ),
            },
            {
              id: "providers",
              label: t("admin.routing.tabs.providers", {
                count: profile.providers.length,
              }),
              content: (
                <div className="routing-table">
                  <Table
                    variant="container"
                    wrapLines
                    items={profile.providers}
                    header={
                      <Header
                        variant="h2"
                        counter={`(${profile.providers.length})`}
                        description={t("admin.routing.providersHint")}
                        actions={
                          <SpaceBetween direction="horizontal" size="xs">
                            <Button
                              iconName="file-open"
                              onClick={() =>
                                setViewing({
                                  title: t("admin.routing.viewer.uploadTitle"),
                                  upload: true,
                                })
                              }
                            >
                              {t("admin.routing.viewer.open")}
                            </Button>
                            <Button
                              iconName="search"
                              onClick={() => setCatalogOpen(true)}
                            >
                              {t("admin.routing.catalog.open")}
                            </Button>
                            <Button
                              onClick={() =>
                                setProviderEdit({
                                  index: null,
                                  value: {
                                    key: "",
                                    type: "inline",
                                    behavior: "domain",
                                    payload: [],
                                    interval: 86400,
                                  },
                                })
                              }
                            >
                              {t("admin.routing.addProvider")}
                            </Button>
                          </SpaceBetween>
                        }
                      >
                        {t("admin.routing.providers")}
                      </Header>
                    }
                    columnDefinitions={[
                      {
                        id: "key",
                        header: t("admin.routing.key"),
                        cell: (p) => <Box fontWeight="bold">{p.key}</Box>,
                      },
                      {
                        id: "type",
                        header: t("admin.routing.type"),
                        cell: (p) =>
                          `${p.type} · ${p.behavior}${p.format ? ` · ${p.format}` : ""}`,
                      },
                      {
                        id: "source",
                        header: t("admin.routing.source"),
                        cell: (p) => (
                          <button
                            type="button"
                            className="routing-source-link"
                            title={t("admin.routing.viewContents")}
                            onClick={() =>
                              setViewing(
                                p.type === "http" && p.url
                                  ? {
                                      title: p.key,
                                      url: p.url,
                                      format: p.format,
                                    }
                                  : { title: p.key, payload: p.payload ?? [] },
                              )
                            }
                          >
                            {p.type === "http" ? (
                              <span className="routing-url">{p.url}</span>
                            ) : (
                              t("admin.routing.entries", {
                                count: p.payload?.length ?? 0,
                              })
                            )}
                          </button>
                        ),
                      },
                      {
                        id: "actions",
                        header: t("common.actions"),
                        cell: (p) => {
                          const i = profile.providers.indexOf(p);
                          return rowActions(
                            () =>
                              setProviderEdit({
                                index: i,
                                value: structuredClone(p),
                              }),
                            () => deleteProvider(i),
                          );
                        },
                      },
                    ]}
                  />
                </div>
              ),
            },
            {
              id: "rules",
              label: t("admin.routing.tabs.rules", {
                count: profile.rules.length,
              }),
              content: (
                <div className="routing-table">
                  <Table
                    variant="container"
                    wrapLines
                    items={ruleRows}
                    header={
                      <Header
                        variant="h2"
                        counter={`(${profile.rules.length})`}
                        description={t("admin.routing.rulesHint")}
                        actions={
                          <Button
                            onClick={() =>
                              setRuleEdit({
                                index: null,
                                value: {
                                  type: "DOMAIN-SUFFIX",
                                  value: "",
                                  target: profile.groups[0]?.key ?? "DIRECT",
                                },
                              })
                            }
                          >
                            {t("admin.routing.addRule")}
                          </Button>
                        }
                      >
                        {t("admin.routing.rules")}
                      </Header>
                    }
                    columnDefinitions={[
                      {
                        id: "pos",
                        header: "#",
                        width: 56,
                        cell: (row) =>
                          row.kind === "rule" ? row.index + 1 : "–",
                      },
                      {
                        id: "match",
                        header: t("admin.routing.match"),
                        cell: (row) => {
                          if (row.kind === "quic")
                            return <code>AND (UDP, 443)</code>;
                          if (row.kind === "match") return <code>MATCH</code>;
                          return (
                            <code>
                              {row.rule.type},{row.rule.value}
                              {row.rule.no_resolve ? ",no-resolve" : ""}
                            </code>
                          );
                        },
                      },
                      {
                        id: "target",
                        header: t("admin.routing.target"),
                        cell: (row) => {
                          if (row.kind === "quic") return "REJECT";
                          if (row.kind === "match")
                            return groupName(profile.final);
                          return groupName(row.rule.target);
                        },
                      },
                      {
                        id: "actions",
                        header: t("common.actions"),
                        cell: (row) => {
                          if (row.kind !== "rule") {
                            return (
                              <Box variant="small" color="text-body-secondary">
                                {row.kind === "quic"
                                  ? t("admin.routing.quicRow")
                                  : t("admin.routing.matchRow")}
                              </Box>
                            );
                          }
                          const i = row.index;
                          return (
                            <SpaceBetween
                              direction="horizontal"
                              size="xs"
                              alignItems="center"
                            >
                              {reorderButtons(i, profile.rules.length, (to) =>
                                update({ rules: move(profile.rules, i, to) }),
                              )}
                              {rowActions(
                                () =>
                                  setRuleEdit({
                                    index: i,
                                    value: { ...row.rule },
                                  }),
                                () =>
                                  update({
                                    rules: profile.rules.filter(
                                      (_, j) => j !== i,
                                    ),
                                  }),
                              )}
                            </SpaceBetween>
                          );
                        },
                      },
                    ]}
                  />
                </div>
              ),
            },
          ]}
        />
      </SpaceBetween>

      {catalogOpen && (
        <RuleSetCatalogModal
          existingUrls={
            new Set(profile.providers.flatMap((p) => (p.url ? [p.url] : [])))
          }
          onClose={() => setCatalogOpen(false)}
          onView={(e) =>
            setViewing({
              title: `${e.kind}/${e.name}`,
              url: e.url,
              format: "mrs",
            })
          }
          onAdd={(picked) => {
            const used = new Set(profile.providers.map((p) => p.key));
            const added: RoutingProvider[] = picked.map((e) => {
              const base = `${e.kind === "geoip" ? "ip_" : ""}${e.name}`
                .toLowerCase()
                .replace(/[^a-z0-9_-]/g, "_")
                .slice(0, 32);
              let key = base;
              for (let n = 2; used.has(key); n++)
                key = `${base.slice(0, 29)}_${n}`;
              used.add(key);
              return {
                key,
                type: "http",
                behavior: e.behavior,
                format: "mrs",
                url: e.url,
                interval: 86400,
              };
            });
            update({ providers: [...profile.providers, ...added] });
            setCatalogOpen(false);
            setSuccess(
              t("admin.routing.catalog.added_n", { count: added.length }),
            );
          }}
        />
      )}
      {viewing && (
        <RuleSetViewerModal target={viewing} onClose={() => setViewing(null)} />
      )}

      {groupEdit && (
        <GroupModal
          editing={groupEdit}
          profile={profile}
          nodes={nodes}
          tags={tags}
          memberLabel={memberLabel}
          onCancel={() => setGroupEdit(null)}
          onSave={(g) => {
            const groups = [...profile.groups];
            if (groupEdit.index === null) groups.push(g);
            else groups[groupEdit.index] = g;
            update({ groups });
            setGroupEdit(null);
          }}
        />
      )}
      {providerEdit && (
        <ProviderModal
          editing={providerEdit}
          profile={profile}
          onCancel={() => setProviderEdit(null)}
          onSave={(p) => {
            const providers = [...profile.providers];
            if (providerEdit.index === null) providers.push(p);
            else providers[providerEdit.index] = p;
            update({ providers });
            setProviderEdit(null);
          }}
        />
      )}
      {ruleEdit && (
        <RuleModal
          editing={ruleEdit}
          profile={profile}
          targetOptions={targetOptions}
          onCancel={() => setRuleEdit(null)}
          onSave={(r) => {
            const rules = [...profile.rules];
            if (ruleEdit.index === null) rules.push(r);
            else rules[ruleEdit.index] = r;
            update({ rules });
            setRuleEdit(null);
          }}
        />
      )}

      <Modal
        visible={preview !== null}
        size="large"
        onDismiss={() => setPreview(null)}
        header={t("admin.routing.previewTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                onClick={() =>
                  void navigator.clipboard?.writeText(preview ?? "")
                }
              >
                {t("admin.routing.copy")}
              </Button>
              <Button variant="primary" onClick={() => setPreview(null)}>
                {t("common.close")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        <SpaceBetween size="s">
          <Box variant="small" color="text-body-secondary">
            {t("admin.routing.previewHint")}
          </Box>
          <pre className="routing-yaml" data-testid="routing-preview">
            {preview}
          </pre>
        </SpaceBetween>
      </Modal>

      <Modal
        visible={confirmReset}
        onDismiss={() => setConfirmReset(false)}
        header={t("admin.routing.resetTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setConfirmReset(false)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="primary"
                loading={busy}
                onClick={() => void handleReset()}
              >
                {t("admin.routing.reset")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        {t("admin.routing.resetConfirm")}
      </Modal>

      <Modal
        visible={blocked !== null}
        onDismiss={() => setBlocked(null)}
        header={t("admin.routing.inUseTitle")}
        footer={
          <Box float="right">
            <Button variant="primary" onClick={() => setBlocked(null)}>
              {t("common.close")}
            </Button>
          </Box>
        }
      >
        <SpaceBetween size="xs">
          <span>{t("admin.routing.inUse")}</span>
          <ul className="routing-field-errors">
            {(blocked ?? []).map((r) => (
              <li key={r}>{r}</li>
            ))}
          </ul>
        </SpaceBetween>
      </Modal>
    </ContentLayout>
  );
}

function ModalFooter({
  onCancel,
  onSave,
}: {
  onCancel: () => void;
  onSave: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Box float="right">
      <SpaceBetween direction="horizontal" size="xs">
        <Button variant="link" onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button variant="primary" onClick={onSave}>
          {t("admin.routing.apply")}
        </Button>
      </SpaceBetween>
    </Box>
  );
}

function GroupModal({
  editing,
  profile,
  nodes,
  tags,
  memberLabel,
  onCancel,
  onSave,
}: {
  editing: { index: number | null; value: RoutingGroup };
  profile: RoutingProfile;
  nodes: Node[];
  tags: NodeTagCount[];
  memberLabel: (m: RoutingMember) => string;
  onCancel: () => void;
  onSave: (g: RoutingGroup) => void;
}) {
  const { t } = useTranslation();
  const [g, setG] = useState<RoutingGroup>(editing.value);
  const [show, setShow] = useState(false);
  const others = profile.groups.filter((_, i) => i !== editing.index);
  const badTag = g.members.some(
    (m) => m.kind === "tag" && !NODE_TAG_PATTERN.test(m.tag),
  );
  const missingNode = g.members.some((m) => m.kind === "node" && !m.node_id);
  const errors = {
    key: !KEY_PATTERN.test(g.key)
      ? t("admin.routing.err.key")
      : others.some((o) => o.key === g.key)
        ? t("admin.routing.err.keyTaken")
        : undefined,
    name:
      groupNameProblem(g.name, t) ??
      (others.some((o) => o.name === g.name)
        ? t("admin.routing.err.nameTaken")
        : undefined),
    members:
      g.members.length === 0
        ? t("admin.routing.err.members")
        : badTag
          ? t("admin.routing.err.tag")
          : missingNode
            ? t("admin.routing.err.node")
            : undefined,
  };
  const timed = g.type !== "select";
  const setMember = (i: number, m: RoutingMember) =>
    setG({ ...g, members: g.members.map((x, j) => (j === i ? m : x)) });
  const kindOptions = [
    opt("tag", t("admin.routing.memberKind.tag")),
    opt("node", t("admin.routing.memberKind.node")),
    opt("group", t("admin.routing.memberKind.group")),
    opt("builtin", t("admin.routing.memberKind.builtin")),
    opt("all", t("admin.routing.memberKind.all")),
  ];
  const groupRefOptions = others.map((o) => opt(o.key, `${o.name} (${o.key})`));
  const tagOptions = tags.map((x) => ({
    value: x.tag,
    label: `#${x.tag}`,
    description: t("admin.nodes.tags.usedBy", { count: x.nodes }),
  }));
  const nodeOptions = nodes.map((n) => ({
    value: n.id,
    label: n.name,
    description: [n.country, ...(n.tags ?? []).map((x) => `#${x}`)]
      .filter(Boolean)
      .join(" · "),
  }));
  const taggedNodes = (tag: string) =>
    nodes.filter((n) => (n.tags ?? []).includes(tag)).map((n) => n.name);

  return (
    <Modal
      visible
      size="large"
      onDismiss={onCancel}
      header={t(
        editing.index === null
          ? "admin.routing.addGroup"
          : "admin.routing.editGroup",
      )}
      footer={
        <ModalFooter
          onCancel={onCancel}
          onSave={() => {
            setShow(true);
            if (!errors.key && !errors.name && !errors.members) onSave(g);
          }}
        />
      }
    >
      <SpaceBetween size="m">
        <div className="routing-form-grid">
          <FormField
            label={t("admin.routing.key")}
            errorText={show && errors.key}
            description={t("admin.routing.keyHint")}
          >
            <Input
              value={g.key}
              disabled={editing.index !== null}
              onChange={({ detail }) => setG({ ...g, key: detail.value })}
            />
          </FormField>
          <FormField
            label={t("admin.routing.name")}
            errorText={show && errors.name}
            description={t("admin.routing.nameHint")}
          >
            <Input
              value={g.name}
              onChange={({ detail }) => setG({ ...g, name: detail.value })}
            />
          </FormField>
          <FormField label={t("admin.routing.type")}>
            <Select
              selectedOption={opt(
                g.type,
                t(`admin.routing.groupType.${g.type}`),
              )}
              options={(
                ["select", "url-test", "fallback"] as RoutingGroupType[]
              ).map((v) => opt(v, t(`admin.routing.groupType.${v}`)))}
              onChange={({ detail }) =>
                setG({
                  ...g,
                  type: detail.selectedOption.value as RoutingGroupType,
                })
              }
            />
          </FormField>
          <FormField label={t("admin.routing.hidden")}>
            <Toggle
              checked={g.hidden}
              onChange={({ detail }) => setG({ ...g, hidden: detail.checked })}
            >
              {t("admin.routing.hiddenHint")}
            </Toggle>
          </FormField>
          {timed && (
            <>
              <FormField label={t("admin.routing.healthUrl")}>
                <Input
                  value={g.url ?? ""}
                  placeholder="https://cp.cloudflare.com/generate_204"
                  onChange={({ detail }) =>
                    setG({ ...g, url: detail.value || undefined })
                  }
                />
              </FormField>
              <FormField label={t("admin.routing.interval")}>
                <Input
                  type="number"
                  value={String(g.interval ?? "")}
                  placeholder="300"
                  onChange={({ detail }) =>
                    setG({
                      ...g,
                      interval: detail.value ? Number(detail.value) : undefined,
                    })
                  }
                />
              </FormField>
            </>
          )}
          {g.type === "url-test" && (
            <FormField label={t("admin.routing.tolerance")}>
              <Input
                type="number"
                value={String(g.tolerance ?? "")}
                placeholder="50"
                onChange={({ detail }) =>
                  setG({
                    ...g,
                    tolerance: detail.value ? Number(detail.value) : undefined,
                  })
                }
              />
            </FormField>
          )}
        </div>

        <FormField
          label={t("admin.routing.members")}
          errorText={show && errors.members}
          description={t("admin.routing.membersHint")}
          stretch
        >
          <SpaceBetween size="xs">
            {g.members.map((m, i) => (
              <div key={i} className="routing-member-row">
                <span className="routing-member-index">{i + 1}</span>
                <Select
                  selectedOption={
                    kindOptions.find((o) => o.value === m.kind) ?? null
                  }
                  options={kindOptions}
                  ariaLabel={t("admin.routing.memberKindLabel")}
                  onChange={({ detail }) => {
                    const kind = detail.selectedOption.value;
                    setMember(
                      i,
                      kind === "group"
                        ? {
                            kind: "group",
                            ref: groupRefOptions[0]?.value ?? "",
                          }
                        : kind === "builtin"
                          ? { kind: "builtin", value: "DIRECT" }
                          : kind === "tag"
                            ? { kind: "tag", tag: tagOptions[0]?.value ?? "" }
                            : kind === "node"
                              ? {
                                  kind: "node",
                                  node_id: nodeOptions[0]?.value ?? "",
                                }
                              : { kind: "all" },
                    );
                  }}
                />
                <div className="routing-member-value">
                  {m.kind === "group" && (
                    <Select
                      selectedOption={
                        groupRefOptions.find((o) => o.value === m.ref) ?? null
                      }
                      options={groupRefOptions}
                      placeholder={t("admin.routing.pickGroup")}
                      ariaLabel={t("admin.routing.pickGroup")}
                      onChange={({ detail }) =>
                        setMember(i, {
                          kind: "group",
                          ref: detail.selectedOption.value ?? "",
                        })
                      }
                    />
                  )}
                  {m.kind === "builtin" && (
                    <Select
                      selectedOption={opt(m.value)}
                      options={[opt("DIRECT"), opt("REJECT")]}
                      ariaLabel={t("admin.routing.memberKind.builtin")}
                      onChange={({ detail }) =>
                        setMember(i, {
                          kind: "builtin",
                          value: detail.selectedOption.value as
                            "DIRECT" | "REJECT",
                        })
                      }
                    />
                  )}
                  {m.kind === "tag" && (
                    <SpaceBetween size="xxs">
                      <Autosuggest
                        value={m.tag}
                        options={tagOptions}
                        placeholder={t("admin.routing.pickTag")}
                        ariaLabel={t("admin.routing.pickTag")}
                        enteredTextLabel={(v) =>
                          t("admin.nodes.tags.add", { tag: normalizeTag(v) })
                        }
                        onChange={({ detail }) =>
                          setMember(i, {
                            kind: "tag",
                            tag: normalizeTag(detail.value),
                          })
                        }
                      />
                      <Box variant="small" color="text-body-secondary">
                        {m.tag && taggedNodes(m.tag).length > 0
                          ? t("admin.routing.tagMatches", {
                              nodes: taggedNodes(m.tag).join(", "),
                            })
                          : t("admin.routing.tagNoMatch")}
                      </Box>
                    </SpaceBetween>
                  )}
                  {m.kind === "node" && (
                    <Select
                      selectedOption={
                        nodeOptions.find((o) => o.value === m.node_id) ?? null
                      }
                      options={nodeOptions}
                      filteringType="auto"
                      placeholder={t("admin.routing.pickNode")}
                      ariaLabel={t("admin.routing.pickNode")}
                      empty={t("admin.routing.noNodes")}
                      onChange={({ detail }) =>
                        setMember(i, {
                          kind: "node",
                          node_id: detail.selectedOption.value ?? "",
                        })
                      }
                    />
                  )}
                  {m.kind === "all" && (
                    <Box variant="small" color="text-body-secondary">
                      {t("admin.routing.allNodesHint", { count: nodes.length })}
                    </Box>
                  )}
                </div>
                <SpaceBetween direction="horizontal" size="xxs">
                  <Button
                    variant="icon"
                    iconName="angle-up"
                    ariaLabel={t("admin.routing.moveUp")}
                    disabled={i === 0}
                    onClick={() =>
                      setG({ ...g, members: move(g.members, i, i - 1) })
                    }
                  />
                  <Button
                    variant="icon"
                    iconName="angle-down"
                    ariaLabel={t("admin.routing.moveDown")}
                    disabled={i === g.members.length - 1}
                    onClick={() =>
                      setG({ ...g, members: move(g.members, i, i + 1) })
                    }
                  />
                  <Button
                    variant="icon"
                    iconName="remove"
                    ariaLabel={t("admin.routing.removeMember", {
                      name: memberLabel(m),
                    })}
                    onClick={() =>
                      setG({
                        ...g,
                        members: g.members.filter((_, j) => j !== i),
                      })
                    }
                  />
                </SpaceBetween>
              </div>
            ))}
            <SpaceBetween direction="horizontal" size="xs">
              <Button
                iconName="add-plus"
                onClick={() =>
                  setG({
                    ...g,
                    members: [
                      ...g.members,
                      { kind: "tag", tag: tagOptions[0]?.value ?? "" },
                    ],
                  })
                }
              >
                {t("admin.routing.addTagMember")}
              </Button>
              <Button
                iconName="add-plus"
                onClick={() =>
                  setG({
                    ...g,
                    members: [
                      ...g.members,
                      { kind: "node", node_id: nodeOptions[0]?.value ?? "" },
                    ],
                  })
                }
              >
                {t("admin.routing.addNodeMember")}
              </Button>
              <Button
                iconName="add-plus"
                onClick={() =>
                  setG({
                    ...g,
                    members: [
                      ...g.members,
                      { kind: "builtin", value: "DIRECT" },
                    ],
                  })
                }
              >
                {t("admin.routing.addMember")}
              </Button>
            </SpaceBetween>
          </SpaceBetween>
        </FormField>
      </SpaceBetween>
    </Modal>
  );
}

function ProviderModal({
  editing,
  profile,
  onCancel,
  onSave,
}: {
  editing: { index: number | null; value: RoutingProvider };
  profile: RoutingProfile;
  onCancel: () => void;
  onSave: (p: RoutingProvider) => void;
}) {
  const { t } = useTranslation();
  const [p, setP] = useState<RoutingProvider>(editing.value);
  const [payloadText, setPayloadText] = useState(
    (editing.value.payload ?? []).join("\n"),
  );
  const [show, setShow] = useState(false);
  const others = profile.providers.filter((_, i) => i !== editing.index);
  const payload = payloadText
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  const errors = {
    key: !KEY_PATTERN.test(p.key)
      ? t("admin.routing.err.key")
      : others.some((o) => o.key === p.key)
        ? t("admin.routing.err.keyTaken")
        : undefined,
    url:
      p.type === "http" && !/^https:\/\/\S+$/.test(p.url ?? "")
        ? t("admin.routing.err.url")
        : undefined,
    format:
      p.type === "http" && p.format === "mrs" && p.behavior === "classical"
        ? t("admin.routing.err.mrs")
        : undefined,
    payload:
      p.type === "inline" && (payload.length === 0 || payload.length > 5000)
        ? t("admin.routing.err.payload")
        : p.type === "inline" &&
            p.behavior !== "classical" &&
            payload.some((e) => e.includes(","))
          ? t("admin.routing.err.valueChars")
          : undefined,
  };
  const save = () => {
    setShow(true);
    if (Object.values(errors).some(Boolean)) return;
    onSave(
      p.type === "http"
        ? {
            key: p.key,
            type: "http",
            behavior: p.behavior,
            format: p.format ?? "mrs",
            url: p.url,
            interval: p.interval,
          }
        : { key: p.key, type: "inline", behavior: p.behavior, payload },
    );
  };

  return (
    <Modal
      visible
      size="medium"
      onDismiss={onCancel}
      header={t(
        editing.index === null
          ? "admin.routing.addProvider"
          : "admin.routing.editProvider",
      )}
      footer={<ModalFooter onCancel={onCancel} onSave={save} />}
    >
      <SpaceBetween size="m">
        <FormField
          label={t("admin.routing.key")}
          errorText={show && errors.key}
          description={t("admin.routing.keyHint")}
        >
          <Input
            value={p.key}
            disabled={editing.index !== null}
            onChange={({ detail }) => setP({ ...p, key: detail.value })}
          />
        </FormField>
        <div className="routing-form-grid">
          <FormField label={t("admin.routing.type")}>
            <Select
              selectedOption={opt(
                p.type,
                t(`admin.routing.providerType.${p.type}`),
              )}
              options={[
                opt("http", t("admin.routing.providerType.http")),
                opt("inline", t("admin.routing.providerType.inline")),
              ]}
              onChange={({ detail }) =>
                setP({
                  ...p,
                  type: detail.selectedOption.value as "http" | "inline",
                  format: p.format ?? "mrs",
                })
              }
            />
          </FormField>
          <FormField label={t("admin.routing.behavior")}>
            <Select
              selectedOption={opt(p.behavior)}
              options={[opt("domain"), opt("ipcidr"), opt("classical")]}
              onChange={({ detail }) =>
                setP({
                  ...p,
                  behavior: detail.selectedOption
                    .value as RoutingProvider["behavior"],
                })
              }
            />
          </FormField>
          {p.type === "http" && (
            <FormField
              label={t("admin.routing.format")}
              errorText={show && errors.format}
            >
              <Select
                selectedOption={opt(p.format ?? "mrs")}
                options={[opt("mrs"), opt("yaml"), opt("text")]}
                onChange={({ detail }) =>
                  setP({
                    ...p,
                    format: detail.selectedOption
                      .value as RoutingProvider["format"],
                  })
                }
              />
            </FormField>
          )}
          {p.type === "http" && (
            <FormField label={t("admin.routing.intervalSeconds")}>
              <Input
                type="number"
                value={String(p.interval ?? 86400)}
                onChange={({ detail }) =>
                  setP({ ...p, interval: Number(detail.value) || undefined })
                }
              />
            </FormField>
          )}
        </div>
        {p.type === "http" ? (
          <FormField label="URL" errorText={show && errors.url} stretch>
            <Input
              value={p.url ?? ""}
              placeholder="https://cdn.jsdelivr.net/gh/..."
              onChange={({ detail }) => setP({ ...p, url: detail.value })}
            />
          </FormField>
        ) : (
          <FormField
            label={t("admin.routing.payload")}
            description={t("admin.routing.payloadHint")}
            errorText={show && errors.payload}
            stretch
          >
            <Textarea
              rows={10}
              value={payloadText}
              onChange={({ detail }) => setPayloadText(detail.value)}
            />
          </FormField>
        )}
      </SpaceBetween>
    </Modal>
  );
}

function RuleModal({
  editing,
  profile,
  targetOptions,
  onCancel,
  onSave,
}: {
  editing: { index: number | null; value: RoutingRule };
  profile: RoutingProfile;
  targetOptions: SelectProps.Option[];
  onCancel: () => void;
  onSave: (r: RoutingRule) => void;
}) {
  const { t } = useTranslation();
  const [r, setR] = useState<RoutingRule>(editing.value);
  const [show, setShow] = useState(false);
  const providerOptions = profile.providers.map((p) => opt(p.key));
  const errors = {
    value:
      r.type === "RULE-SET"
        ? profile.providers.some((p) => p.key === r.value)
          ? undefined
          : t("admin.routing.err.provider")
        : valueProblem(r.value, t),
    target: targetOptions.some((o) => o.value === r.target)
      ? undefined
      : t("admin.routing.err.target"),
  };
  const save = () => {
    setShow(true);
    if (errors.value || errors.target) return;
    onSave({
      ...r,
      no_resolve:
        NO_RESOLVE_TYPES.has(r.type) && r.no_resolve ? true : undefined,
    });
  };

  return (
    <Modal
      visible
      onDismiss={onCancel}
      header={t(
        editing.index === null
          ? "admin.routing.addRule"
          : "admin.routing.editRule",
      )}
      footer={<ModalFooter onCancel={onCancel} onSave={save} />}
    >
      <SpaceBetween size="m">
        <FormField label={t("admin.routing.type")}>
          <Select
            selectedOption={opt(r.type)}
            options={RULE_TYPES.map((v) => opt(v))}
            onChange={({ detail }) =>
              setR({
                ...r,
                type: detail.selectedOption.value as RoutingRuleType,
                value: "",
              })
            }
          />
        </FormField>
        <FormField
          label={t("admin.routing.value")}
          errorText={show && errors.value}
          description={t(`admin.routing.valueHint.${r.type}`)}
        >
          {r.type === "RULE-SET" ? (
            <Select
              selectedOption={
                providerOptions.find((o) => o.value === r.value) ?? null
              }
              options={providerOptions}
              placeholder={t("admin.routing.pickProvider")}
              onChange={({ detail }) =>
                setR({ ...r, value: detail.selectedOption.value ?? "" })
              }
            />
          ) : (
            <Input
              value={r.value}
              onChange={({ detail }) => setR({ ...r, value: detail.value })}
            />
          )}
        </FormField>
        <FormField
          label={t("admin.routing.target")}
          errorText={show && errors.target}
        >
          <Select
            selectedOption={
              targetOptions.find((o) => o.value === r.target) ?? null
            }
            options={targetOptions}
            onChange={({ detail }) =>
              setR({ ...r, target: detail.selectedOption.value ?? "" })
            }
          />
        </FormField>
        {NO_RESOLVE_TYPES.has(r.type) && (
          <Checkbox
            checked={Boolean(r.no_resolve)}
            onChange={({ detail }) =>
              setR({ ...r, no_resolve: detail.checked })
            }
          >
            {t("admin.routing.noResolve")}
          </Checkbox>
        )}
      </SpaceBetween>
    </Modal>
  );
}
