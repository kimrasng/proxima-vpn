import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { parseDocument } from "yaml";
import {
  Badge,
  Box,
  Button,
  Input,
  Modal,
  SegmentedControl,
  Select,
  SpaceBetween,
  Tabs,
  Textarea,
  Toggle,
  type SelectProps,
} from "@cloudscape-design/components";
import type {
  ClashDNSSettings,
  ClashGeneralSettings,
  ClashSettings,
  ClashSnifferSettings,
} from "../../api/types";
import {
  CLASH_PRESETS,
  DNS_SERVER_SUGGESTIONS,
  EXTRA_YAML_SNIPPETS,
  RESERVED_EXTRA_KEYS,
  type ClashPresetId,
} from "./clashPresets";

/** Field id (section.name) -> the YAML key it writes. */
const YAML_KEYS: Record<string, string> = {
  "general.mixed_port": "mixed-port",
  "general.port": "port",
  "general.socks_port": "socks-port",
  "general.mode": "mode",
  "general.log_level": "log-level",
  "general.find_process_mode": "find-process-mode",
  "general.allow_lan": "allow-lan",
  "general.bind_address": "bind-address",
  "general.ipv6": "ipv6",
  "general.unified_delay": "unified-delay",
  "general.tcp_concurrent": "tcp-concurrent",
  "general.keep_alive_interval": "keep-alive-interval",
  "general.keep_alive_idle": "keep-alive-idle",
  "general.disable_keep_alive": "disable-keep-alive",
  "general.external_controller": "external-controller",
  "general.secret": "secret",
  "general.store_selected": "profile.store-selected",
  "general.store_fake_ip": "profile.store-fake-ip",
  "general.geodata_mode": "geodata-mode",
  "general.geo_auto_update": "geo-auto-update",
  "general.geo_update_interval": "geo-update-interval",
  "dns.enable": "dns.enable",
  "dns.listen": "dns.listen",
  "dns.enhanced_mode": "dns.enhanced-mode",
  "dns.fake_ip_range": "dns.fake-ip-range",
  "dns.fake_ip_filter_mode": "dns.fake-ip-filter-mode",
  "dns.cache_algorithm": "dns.cache-algorithm",
  "dns.ipv6": "dns.ipv6",
  "dns.respect_rules": "dns.respect-rules",
  "dns.prefer_h3": "dns.prefer-h3",
  "dns.use_hosts": "dns.use-hosts",
  "dns.use_system_hosts": "dns.use-system-hosts",
  "dns.direct_nameserver_follow_policy": "dns.direct-nameserver-follow-policy",
  "dns.default_nameserver": "dns.default-nameserver",
  "dns.nameserver": "dns.nameserver",
  "dns.proxy_server_nameserver": "dns.proxy-server-nameserver",
  "dns.direct_nameserver": "dns.direct-nameserver",
  "dns.fallback": "dns.fallback",
  "dns.fake_ip_filter": "dns.fake-ip-filter",
  "dns.nameserver_policy": "dns.nameserver-policy",
  "dns.ff_geoip": "dns.fallback-filter.geoip",
  "dns.ff_geoip_code": "dns.fallback-filter.geoip-code",
  "dns.ff_ipcidr": "dns.fallback-filter.ipcidr",
  "dns.ff_domain": "dns.fallback-filter.domain",
  "sniffer.enable": "sniffer.enable",
  "sniffer.force_dns_mapping": "sniffer.force-dns-mapping",
  "sniffer.parse_pure_ip": "sniffer.parse-pure-ip",
  "sniffer.override_destination": "sniffer.override-destination",
  "sniffer.http_override_destination": "sniffer.sniff.HTTP.override-destination",
  "sniffer.http_ports": "sniffer.sniff.HTTP.ports",
  "sniffer.tls_ports": "sniffer.sniff.TLS.ports",
  "sniffer.quic_ports": "sniffer.sniff.QUIC.ports",
  "sniffer.skip_domain": "sniffer.skip-domain",
  "sniffer.force_domain": "sniffer.force-domain",
  "sniffer.skip_src_address": "sniffer.skip-src-address",
  "sniffer.skip_dst_address": "sniffer.skip-dst-address",
  "advanced.hosts": "hosts",
  "advanced.extra_yaml": "(extra)",
};

const PORT_SPEC = /^\d{1,5}(-\d{1,5})?$/;
const CIDR = /^[0-9a-fA-F:.]+\/\d{1,3}$/;
const DOMAIN = /^(\+\.|\*\.|\.)?[A-Za-z0-9*_-]+(\.[A-Za-z0-9*_-]+)*\.?$/;
const DNS_SERVER = /^(system|dhcp:\/\/\S+|(udp|tcp|tls|https|quic|h3):\/\/\S+|[0-9a-fA-F:.]+(#\S+)?|\[[0-9a-fA-F:]+\](:\d+)?)$/;

type Validator = (v: string) => boolean;
type ValidatorKind = "port" | "cidr" | "domain" | "dns" | "any";

const validators: Record<ValidatorKind, Validator> = {
  port: (v) => PORT_SPEC.test(v) && v.split("-").every((p) => Number(p) >= 1 && Number(p) <= 65535),
  cidr: (v) => CIDR.test(v),
  domain: (v) => DOMAIN.test(v) || v.startsWith("rule-set:") || v.startsWith("geosite:"),
  dns: (v) => DNS_SERVER.test(v),
  any: (v) => v.trim().length > 0,
};

function opts(values: string[], label?: (v: string) => string): SelectProps.Option[] {
  return values.map((v) => ({ value: v, label: label ? label(v) : v }));
}

function Row({ id, children, block, badge }: { id: string; children: ReactNode; block?: boolean; badge?: ReactNode }) {
  const { t } = useTranslation();
  return (
    <div className={block ? "clash-row clash-row--block" : "clash-row"}>
      <div className="clash-row__text">
        <div className="clash-row__label">
          {t(`admin.routing.settings.field.${id}.label`)}
          {badge}
        </div>
        <div className="clash-row__desc">{t(`admin.routing.settings.field.${id}.desc`)}</div>
        <code className="clash-row__key">{YAML_KEYS[id]}</code>
      </div>
      <div className="clash-row__control">{children}</div>
    </div>
  );
}

function Group({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="clash-group">
      <header className="clash-group__head">
        <h3>{title}</h3>
        {hint && <p>{hint}</p>}
      </header>
      <div className="clash-group__rows">{children}</div>
    </section>
  );
}

function ChipList({
  value,
  onChange,
  placeholder,
  kind = "any",
  suggestions,
}: {
  value: string[] | null;
  onChange: (v: string[]) => void;
  placeholder?: string;
  kind?: ValidatorKind;
  suggestions?: { value: string; label: string }[];
}) {
  const { t } = useTranslation();
  const list = value ?? [];
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const valid = validators[kind];

  const add = (raw: string) => {
    const items = raw
      .split(/[\n,]/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (!items.length) return;
    const bad = items.filter((i) => !valid(i));
    if (bad.length) {
      setError(t(`admin.routing.settings.invalid.${kind}`, { value: bad[0] }));
      return;
    }
    const next = [...list];
    for (const i of items) if (!next.includes(i)) next.push(i);
    onChange(next);
    setDraft("");
    setError(null);
  };

  const unused = (suggestions ?? []).filter((s) => !list.includes(s.value));

  return (
    <div className="clash-chips">
      {list.length > 0 && (
        <ul className="clash-chips__list">
          {list.map((item, i) => (
            <li key={`${item}-${i}`} className="clash-chip">
              <code>{item}</code>
              <button
                type="button"
                className="clash-chip__x"
                aria-label={t("admin.routing.settings.removeItem", { value: item })}
                onClick={() => onChange(list.filter((_, j) => j !== i))}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="clash-chips__add">
        <Input
          value={draft}
          placeholder={placeholder ?? t("admin.routing.settings.addPlaceholder")}
          invalid={!!error}
          onChange={({ detail }) => {
            setDraft(detail.value);
            if (error) setError(null);
          }}
          onKeyDown={({ detail }) => {
            if (detail.key === "Enter") add(draft);
          }}
        />
        <Button iconName="add-plus" onClick={() => add(draft)} disabled={!draft.trim()}>
          {t("admin.routing.settings.add")}
        </Button>
      </div>
      {error && <div className="clash-field-error">{error}</div>}
      {unused.length > 0 && (
        <div className="clash-suggest">
          <span>{t("admin.routing.settings.suggest")}</span>
          {unused.map((s) => (
            <button key={s.value} type="button" className="clash-suggest__item" onClick={() => add(s.value)}>
              + {s.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function PairTable({
  value,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
  keyKind = "domain",
}: {
  value: [string, string][];
  onChange: (v: [string, string][]) => void;
  keyPlaceholder: string;
  valuePlaceholder: string;
  keyKind?: ValidatorKind;
}) {
  const { t } = useTranslation();
  const [k, setK] = useState("");
  const [v, setV] = useState("");
  const [error, setError] = useState<string | null>(null);
  const add = () => {
    const key = k.trim();
    const val = v.trim();
    if (!key || !val) return;
    if (!validators[keyKind](key)) {
      setError(t(`admin.routing.settings.invalid.${keyKind}`, { value: key }));
      return;
    }
    const next = value.filter(([ek]) => ek !== key);
    next.push([key, val]);
    onChange(next);
    setK("");
    setV("");
    setError(null);
  };
  return (
    <div className="clash-pairs">
      {value.length > 0 && (
        <ul className="clash-pairs__list">
          {value.map(([key, val], i) => (
            <li key={key} className="clash-pair">
              <code className="clash-pair__k">{key}</code>
              <span className="clash-pair__arrow" aria-hidden>→</span>
              <code className="clash-pair__v">{val}</code>
              <button
                type="button"
                className="clash-chip__x"
                aria-label={t("admin.routing.settings.removeItem", { value: key })}
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="clash-pairs__add">
        <Input value={k} placeholder={keyPlaceholder} invalid={!!error}
          onChange={({ detail }) => { setK(detail.value); setError(null); }}
          onKeyDown={({ detail }) => { if (detail.key === "Enter") add(); }} />
        <Input value={v} placeholder={valuePlaceholder}
          onChange={({ detail }) => setV(detail.value)}
          onKeyDown={({ detail }) => { if (detail.key === "Enter") add(); }} />
        <Button iconName="add-plus" onClick={add} disabled={!k.trim() || !v.trim()}>
          {t("admin.routing.settings.add")}
        </Button>
      </div>
      {error && <div className="clash-field-error">{error}</div>}
    </div>
  );
}

function PortInput({ value, onChange, placeholder }: { value: number; onChange: (v: number) => void; placeholder?: string }) {
  const { t } = useTranslation();
  const bad = value < 0 || value > 65535;
  return (
    <div className="clash-num">
      <Input type="number" inputMode="numeric" value={value ? String(value) : ""} placeholder={placeholder}
        invalid={bad} onChange={({ detail }) => onChange(detail.value ? Number(detail.value) : 0)} />
      {!value && <span className="clash-num__hint">{t("admin.routing.settings.portOff")}</span>}
    </div>
  );
}

function NumberInput({ value, onChange, placeholder, unit }: { value: number; onChange: (v: number) => void; placeholder?: string; unit?: string }) {
  return (
    <div className="clash-num">
      <Input type="number" inputMode="numeric" value={value ? String(value) : ""} placeholder={placeholder}
        onChange={({ detail }) => onChange(detail.value ? Math.max(0, Number(detail.value)) : 0)} />
      {unit && <span className="clash-num__hint">{unit}</span>}
    </div>
  );
}

function Switch({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  const { t } = useTranslation();
  return (
    <Toggle checked={checked} onChange={({ detail }) => onChange(detail.checked)}>
      {checked ? t("admin.routing.on") : t("admin.routing.off")}
    </Toggle>
  );
}

function Segments({ value, options, onChange }: { value: string; options: { id: string; text: string }[]; onChange: (v: string) => void }) {
  return <SegmentedControl selectedId={value} options={options} onChange={({ detail }) => onChange(detail.selectedId)} />;
}

function Choice({ value, options, onChange }: { value: string; options: SelectProps.Option[]; onChange: (v: string) => void }) {
  return (
    <Select selectedOption={options.find((o) => o.value === value) ?? null} options={options}
      onChange={({ detail }) => onChange(detail.selectedOption.value ?? "")} />
  );
}

/** Checks extra YAML the same way the server does, so mistakes show before saving. */
function useExtraYamlCheck(text: string) {
  return useMemo(() => {
    if (!text.trim()) return { ok: true as const, keys: [] as string[] };
    const doc = parseDocument(text);
    if (doc.errors.length) {
      const e = doc.errors[0]!;
      const line = e.linePos?.[0]?.line;
      return { ok: false as const, kind: "syntax" as const, message: e.message.split("\n")[0] ?? "", line };
    }
    const data = doc.toJS() as unknown;
    if (data === null || typeof data !== "object" || Array.isArray(data)) {
      return { ok: false as const, kind: "mapping" as const, message: "" };
    }
    const keys = Object.keys(data as Record<string, unknown>);
    const reserved = keys.filter((k) => RESERVED_EXTRA_KEYS.includes(k));
    if (reserved.length) return { ok: false as const, kind: "reserved" as const, message: reserved.join(", ") };
    return { ok: true as const, keys };
  }, [text]);
}

export default function ClashSettingsPanel({
  value,
  onChange,
  quicBlocked,
}: {
  value: ClashSettings;
  onChange: (s: ClashSettings) => void;
  quicBlocked: boolean;
}) {
  const { t } = useTranslation();
  const [tab, setTab] = useState("basic");
  const [presetConfirm, setPresetConfirm] = useState<ClashPresetId | null>(null);
  const g = value.general;
  const d = value.dns;
  const sn = value.sniffer;
  const ff = d.fallback_filter;
  const setG = (patch: Partial<ClashGeneralSettings>) => onChange({ ...value, general: { ...g, ...patch } });
  const setD = (patch: Partial<ClashDNSSettings>) => onChange({ ...value, dns: { ...d, ...patch } });
  const setS = (patch: Partial<ClashSnifferSettings>) => onChange({ ...value, sniffer: { ...sn, ...patch } });
  const setFF = (patch: Partial<ClashDNSSettings["fallback_filter"]>) => setD({ fallback_filter: { ...ff, ...patch } });
  const extraCheck = useExtraYamlCheck(value.extra_yaml);

  const applyPreset = (id: ClashPresetId) => {
    onChange(CLASH_PRESETS[id].apply(value));
    setPresetConfirm(null);
  };

  const dnsSuggest = DNS_SERVER_SUGGESTIONS.map((s) => ({ value: s.value, label: t(`admin.routing.settings.dnsSuggest.${s.key}`) }));
  const fakeIp = d.enhanced_mode === "fake-ip";

  const basic = (
    <SpaceBetween size="l">
      <Group title={t("admin.routing.settings.group.ports")} hint={t("admin.routing.settings.groupHint.ports")}>
        <Row id="general.mixed_port"><PortInput value={g.mixed_port} placeholder="7890" onChange={(v) => setG({ mixed_port: v })} /></Row>
        <Row id="general.mode">
          <Segments value={g.mode} onChange={(v) => setG({ mode: v as ClashGeneralSettings["mode"] })}
            options={["rule", "global", "direct"].map((id) => ({ id, text: t(`admin.routing.settings.mode.${id}`) }))} />
        </Row>
        <Row id="general.allow_lan"><Switch checked={g.allow_lan} onChange={(v) => setG({ allow_lan: v })} /></Row>
        {g.allow_lan && (
          <Row id="general.bind_address">
            <Input value={g.bind_address} placeholder="*" onChange={({ detail }) => setG({ bind_address: detail.value })} />
          </Row>
        )}
        <Row id="general.ipv6"><Switch checked={g.ipv6} onChange={(v) => setG({ ipv6: v })} /></Row>
      </Group>

      <Group title={t("admin.routing.settings.group.dnsBasic")} hint={t("admin.routing.settings.groupHint.dnsBasic")}>
        <Row id="dns.enable"><Switch checked={d.enable} onChange={(v) => setD({ enable: v })} /></Row>
        {d.enable && (
          <>
            <Row id="dns.enhanced_mode">
              <Segments value={d.enhanced_mode} onChange={(v) => setD({ enhanced_mode: v as ClashDNSSettings["enhanced_mode"] })}
                options={["fake-ip", "redir-host", "normal"].map((id) => ({ id, text: t(`admin.routing.settings.enhanced.${id}`) }))} />
            </Row>
            <Row id="dns.nameserver" block>
              <ChipList kind="dns" value={d.nameserver} onChange={(v) => setD({ nameserver: v })}
                placeholder="https://cloudflare-dns.com/dns-query" suggestions={dnsSuggest} />
            </Row>
          </>
        )}
      </Group>

      <Group title={t("admin.routing.settings.group.sniffBasic")} hint={t("admin.routing.settings.groupHint.sniffBasic")}>
        <Row id="sniffer.enable"><Switch checked={sn.enable} onChange={(v) => setS({ enable: v })} /></Row>
      </Group>
    </SpaceBetween>
  );

  const network = (
    <SpaceBetween size="l">
      <Group title={t("admin.routing.settings.group.extraPorts")} hint={t("admin.routing.settings.groupHint.extraPorts")}>
        <Row id="general.port"><PortInput value={g.port} placeholder="0" onChange={(v) => setG({ port: v })} /></Row>
        <Row id="general.socks_port"><PortInput value={g.socks_port} placeholder="0" onChange={(v) => setG({ socks_port: v })} /></Row>
      </Group>
      <Group title={t("admin.routing.settings.group.connection")} hint={t("admin.routing.settings.groupHint.connection")}>
        <Row id="general.tcp_concurrent"><Switch checked={g.tcp_concurrent} onChange={(v) => setG({ tcp_concurrent: v })} /></Row>
        <Row id="general.unified_delay"><Switch checked={g.unified_delay} onChange={(v) => setG({ unified_delay: v })} /></Row>
        <Row id="general.disable_keep_alive"><Switch checked={g.disable_keep_alive} onChange={(v) => setG({ disable_keep_alive: v })} /></Row>
        {!g.disable_keep_alive && (
          <>
            <Row id="general.keep_alive_interval">
              <NumberInput value={g.keep_alive_interval} placeholder="15" unit={t("admin.routing.settings.unit.seconds")} onChange={(v) => setG({ keep_alive_interval: v })} />
            </Row>
            <Row id="general.keep_alive_idle">
              <NumberInput value={g.keep_alive_idle} placeholder="0" unit={t("admin.routing.settings.unit.seconds")} onChange={(v) => setG({ keep_alive_idle: v })} />
            </Row>
          </>
        )}
        <Row id="general.find_process_mode">
          <Segments value={g.find_process_mode} onChange={(v) => setG({ find_process_mode: v as ClashGeneralSettings["find_process_mode"] })}
            options={["off", "strict", "always"].map((id) => ({ id, text: t(`admin.routing.settings.process.${id}`) }))} />
        </Row>
      </Group>
      <Group title={t("admin.routing.settings.group.app")} hint={t("admin.routing.settings.groupHint.app")}>
        <Row id="general.log_level">
          <Choice value={g.log_level} onChange={(v) => setG({ log_level: v as ClashGeneralSettings["log_level"] })}
            options={opts(["silent", "error", "warning", "info", "debug"], (v) => t(`admin.routing.settings.log.${v}`))} />
        </Row>
        <Row id="general.store_selected"><Switch checked={g.store_selected} onChange={(v) => setG({ store_selected: v })} /></Row>
        <Row id="general.store_fake_ip"><Switch checked={g.store_fake_ip} onChange={(v) => setG({ store_fake_ip: v })} /></Row>
        <Row id="general.external_controller">
          <Input value={g.external_controller} placeholder="127.0.0.1:9090" onChange={({ detail }) => setG({ external_controller: detail.value })} />
        </Row>
        {g.external_controller.trim() !== "" && (
          <Row id="general.secret">
            <Input type="password" value={g.secret} onChange={({ detail }) => setG({ secret: detail.value })} />
          </Row>
        )}
      </Group>
      <Group title={t("admin.routing.settings.group.geo")} hint={t("admin.routing.settings.groupHint.geo")}>
        <Row id="general.geodata_mode"><Switch checked={g.geodata_mode} onChange={(v) => setG({ geodata_mode: v })} /></Row>
        <Row id="general.geo_auto_update"><Switch checked={g.geo_auto_update} onChange={(v) => setG({ geo_auto_update: v })} /></Row>
        {g.geo_auto_update && (
          <Row id="general.geo_update_interval">
            <NumberInput value={g.geo_update_interval} placeholder="24" unit={t("admin.routing.settings.unit.hours")} onChange={(v) => setG({ geo_update_interval: v })} />
          </Row>
        )}
      </Group>
    </SpaceBetween>
  );

  const dns = !d.enable ? (
    <div className="clash-empty">
      <Box>{t("admin.routing.settings.dnsOff")}</Box>
      <Button onClick={() => setD({ enable: true })}>{t("admin.routing.settings.dnsTurnOn")}</Button>
    </div>
  ) : (
    <SpaceBetween size="l">
      <Group title={t("admin.routing.settings.group.dnsServers")} hint={t("admin.routing.settings.groupHint.dnsServers")}>
        <Row id="dns.nameserver" block>
          <ChipList kind="dns" value={d.nameserver} onChange={(v) => setD({ nameserver: v })} placeholder="https://cloudflare-dns.com/dns-query" suggestions={dnsSuggest} />
        </Row>
        <Row id="dns.default_nameserver" block>
          <ChipList kind="dns" value={d.default_nameserver} onChange={(v) => setD({ default_nameserver: v })} placeholder="1.1.1.1"
            suggestions={[{ value: "1.1.1.1", label: "1.1.1.1" }, { value: "8.8.8.8", label: "8.8.8.8" }, { value: "223.5.5.5", label: "223.5.5.5" }]} />
        </Row>
        <Row id="dns.proxy_server_nameserver" block>
          <ChipList kind="dns" value={d.proxy_server_nameserver} onChange={(v) => setD({ proxy_server_nameserver: v })} placeholder="https://1.1.1.1/dns-query" suggestions={dnsSuggest} />
        </Row>
        <Row id="dns.direct_nameserver" block>
          <ChipList kind="dns" value={d.direct_nameserver} onChange={(v) => setD({ direct_nameserver: v })} placeholder="system"
            suggestions={[{ value: "system", label: t("admin.routing.settings.dnsSuggest.system") }, ...dnsSuggest]} />
        </Row>
        <Row id="dns.direct_nameserver_follow_policy"><Switch checked={d.direct_nameserver_follow_policy} onChange={(v) => setD({ direct_nameserver_follow_policy: v })} /></Row>
      </Group>

      <Group title={t("admin.routing.settings.group.fakeIp")} hint={t("admin.routing.settings.groupHint.fakeIp")}>
        <Row id="dns.enhanced_mode">
          <Segments value={d.enhanced_mode} onChange={(v) => setD({ enhanced_mode: v as ClashDNSSettings["enhanced_mode"] })}
            options={["fake-ip", "redir-host", "normal"].map((id) => ({ id, text: t(`admin.routing.settings.enhanced.${id}`) }))} />
        </Row>
        {fakeIp && (
          <>
            <Row id="dns.fake_ip_range"><Input value={d.fake_ip_range} placeholder="198.18.0.1/16" onChange={({ detail }) => setD({ fake_ip_range: detail.value })} /></Row>
            <Row id="dns.fake_ip_filter_mode">
              <Segments value={d.fake_ip_filter_mode} onChange={(v) => setD({ fake_ip_filter_mode: v as ClashDNSSettings["fake_ip_filter_mode"] })}
                options={["rule", "blacklist", "whitelist"].map((id) => ({ id, text: t(`admin.routing.settings.filterMode.${id}`) }))} />
            </Row>
            <Row id="dns.fake_ip_filter" block>
              <ChipList value={d.fake_ip_filter} onChange={(v) => setD({ fake_ip_filter: v })}
                placeholder={d.fake_ip_filter_mode === "rule" ? "RULE-SET,private_domain,real-ip" : "+.lan"}
                suggestions={d.fake_ip_filter_mode === "rule"
                  ? [{ value: "RULE-SET,private_domain,real-ip", label: "private_domain → real-ip" }, { value: "MATCH,fake-ip", label: "MATCH → fake-ip" }]
                  : [{ value: "+.lan", label: "+.lan" }, { value: "+.local", label: "+.local" }, { value: "+.msftconnecttest.com", label: "+.msftconnecttest.com" }]} />
            </Row>
          </>
        )}
      </Group>

      <Group title={t("admin.routing.settings.group.dnsPolicy")} hint={t("admin.routing.settings.groupHint.dnsPolicy")}>
        <Row id="dns.nameserver_policy" block>
          <PairTable
            value={Object.entries(d.nameserver_policy ?? {}).map(([k, v]) => [k, v.join(", ")])}
            keyPlaceholder="+.naver.com"
            valuePlaceholder="system"
            onChange={(pairs) => setD({
              nameserver_policy: Object.fromEntries(pairs.map(([k, v]) => [k, v.split(",").map((s) => s.trim()).filter(Boolean)])),
            })}
          />
        </Row>
      </Group>

      <Group title={t("admin.routing.settings.group.dnsBehavior")}>
        <Row id="dns.respect_rules"><Switch checked={d.respect_rules} onChange={(v) => setD({ respect_rules: v })} /></Row>
        <Row id="dns.ipv6"><Switch checked={d.ipv6} onChange={(v) => setD({ ipv6: v })} /></Row>
        <Row id="dns.prefer_h3"><Switch checked={d.prefer_h3} onChange={(v) => setD({ prefer_h3: v })} /></Row>
        <Row id="dns.use_hosts"><Switch checked={d.use_hosts} onChange={(v) => setD({ use_hosts: v })} /></Row>
        <Row id="dns.use_system_hosts"><Switch checked={d.use_system_hosts} onChange={(v) => setD({ use_system_hosts: v })} /></Row>
        <Row id="dns.cache_algorithm">
          <Segments value={d.cache_algorithm} onChange={(v) => setD({ cache_algorithm: v as ClashDNSSettings["cache_algorithm"] })}
            options={[{ id: "", text: t("admin.routing.settings.dnsDefault") }, { id: "lru", text: "LRU" }, { id: "arc", text: "ARC" }]} />
        </Row>
        <Row id="dns.listen"><Input value={d.listen} placeholder="0.0.0.0:1053" onChange={({ detail }) => setD({ listen: detail.value })} /></Row>
      </Group>

      <Group title={t("admin.routing.settings.group.fallback")} hint={t("admin.routing.settings.groupHint.fallback")}>
        <Row id="dns.fallback" block>
          <ChipList kind="dns" value={d.fallback} onChange={(v) => setD({ fallback: v })} placeholder="tls://8.8.4.4" suggestions={dnsSuggest} />
        </Row>
        {(d.fallback ?? []).length > 0 && (
          <>
            <Row id="dns.ff_geoip"><Switch checked={ff.geoip} onChange={(v) => setFF({ geoip: v })} /></Row>
            {ff.geoip && (
              <Row id="dns.ff_geoip_code">
                <Input value={ff.geoip_code} placeholder="KR" onChange={({ detail }) => setFF({ geoip_code: detail.value.toUpperCase().slice(0, 2) })} />
              </Row>
            )}
            <Row id="dns.ff_ipcidr" block><ChipList kind="cidr" value={ff.ipcidr} onChange={(v) => setFF({ ipcidr: v })} placeholder="240.0.0.0/4" /></Row>
            <Row id="dns.ff_domain" block><ChipList kind="domain" value={ff.domain} onChange={(v) => setFF({ domain: v })} placeholder="+.google.com" /></Row>
          </>
        )}
      </Group>
    </SpaceBetween>
  );

  const sniffer = !sn.enable ? (
    <div className="clash-empty">
      <Box>{t("admin.routing.settings.snifferOff")}</Box>
      <Button onClick={() => setS({ enable: true })}>{t("admin.routing.settings.snifferTurnOn")}</Button>
    </div>
  ) : (
    <SpaceBetween size="l">
      <Group title={t("admin.routing.settings.group.sniffPorts")} hint={t("admin.routing.settings.groupHint.sniffPorts")}>
        <Row id="sniffer.tls_ports" block><ChipList kind="port" value={sn.tls_ports} onChange={(v) => setS({ tls_ports: v })} placeholder="443" suggestions={[{ value: "443", label: "443" }, { value: "8443", label: "8443" }]} /></Row>
        <Row id="sniffer.http_ports" block><ChipList kind="port" value={sn.http_ports} onChange={(v) => setS({ http_ports: v })} placeholder="80" suggestions={[{ value: "80", label: "80" }, { value: "8080-8880", label: "8080-8880" }]} /></Row>
        <Row id="sniffer.quic_ports" block badge={quicBlocked && (sn.quic_ports ?? []).length > 0 ? <Badge color="severity-medium">{t("admin.routing.settings.quicBadge")}</Badge> : undefined}>
          <SpaceBetween size="xxs">
            <ChipList kind="port" value={sn.quic_ports} onChange={(v) => setS({ quic_ports: v })} placeholder="443" suggestions={[{ value: "443", label: "443" }]} />
            {quicBlocked && (sn.quic_ports ?? []).length > 0 && <div className="clash-field-warn">{t("admin.routing.settings.quicWarning")}</div>}
          </SpaceBetween>
        </Row>
      </Group>
      <Group title={t("admin.routing.settings.group.sniffBehavior")}>
        <Row id="sniffer.force_dns_mapping"><Switch checked={sn.force_dns_mapping} onChange={(v) => setS({ force_dns_mapping: v })} /></Row>
        <Row id="sniffer.parse_pure_ip"><Switch checked={sn.parse_pure_ip} onChange={(v) => setS({ parse_pure_ip: v })} /></Row>
        <Row id="sniffer.override_destination"><Switch checked={sn.override_destination} onChange={(v) => setS({ override_destination: v })} /></Row>
        <Row id="sniffer.http_override_destination"><Switch checked={sn.http_override_destination} onChange={(v) => setS({ http_override_destination: v })} /></Row>
      </Group>
      <Group title={t("admin.routing.settings.group.sniffExceptions")} hint={t("admin.routing.settings.groupHint.sniffExceptions")}>
        <Row id="sniffer.skip_domain" block><ChipList kind="domain" value={sn.skip_domain} onChange={(v) => setS({ skip_domain: v })} placeholder="+.push.apple.com"
          suggestions={[{ value: "+.push.apple.com", label: "+.push.apple.com" }, { value: "Mijia Cloud", label: "Mijia Cloud" }]} /></Row>
        <Row id="sniffer.force_domain" block><ChipList kind="domain" value={sn.force_domain} onChange={(v) => setS({ force_domain: v })} placeholder="+.netflix.com" /></Row>
        <Row id="sniffer.skip_src_address" block><ChipList kind="cidr" value={sn.skip_src_address} onChange={(v) => setS({ skip_src_address: v })} placeholder="192.168.0.0/16" /></Row>
        <Row id="sniffer.skip_dst_address" block><ChipList kind="cidr" value={sn.skip_dst_address} onChange={(v) => setS({ skip_dst_address: v })} placeholder="10.0.0.0/8" /></Row>
      </Group>
    </SpaceBetween>
  );

  const advanced = (
    <SpaceBetween size="l">
      <Group title={t("admin.routing.settings.group.hosts")} hint={t("admin.routing.settings.groupHint.hosts")}>
        <Row id="advanced.hosts" block>
          <PairTable value={Object.entries(value.hosts ?? {})} keyPlaceholder="router.lan" valuePlaceholder="192.168.0.1"
            onChange={(pairs) => onChange({ ...value, hosts: Object.fromEntries(pairs) })} />
        </Row>
      </Group>
      <Group title={t("admin.routing.settings.group.extra")} hint={t("admin.routing.settings.groupHint.extra")}>
        <div className="clash-snippets">
          <span>{t("admin.routing.settings.snippets")}</span>
          {EXTRA_YAML_SNIPPETS.map((s) => {
            const has = extraCheck.ok && extraCheck.keys.includes(s.key);
            return (
              <button key={s.key} type="button" className="clash-suggest__item" disabled={has}
                onClick={() => onChange({ ...value, extra_yaml: `${value.extra_yaml.replace(/\s*$/, "")}${value.extra_yaml.trim() ? "\n" : ""}${s.yaml}\n` })}>
                + {t(`admin.routing.settings.snippet.${s.key}`)}
              </button>
            );
          })}
        </div>
        <Textarea
          rows={12}
          spellcheck={false}
          value={value.extra_yaml}
          invalid={!extraCheck.ok}
          placeholder={"tun:\n  enable: false\n  stack: mixed"}
          onChange={({ detail }) => onChange({ ...value, extra_yaml: detail.value })}
        />
        {extraCheck.ok ? (
          value.extra_yaml.trim() && (
            <div className="clash-field-ok">
              {t("admin.routing.settings.extraOk", { keys: extraCheck.keys.join(", ") })}
            </div>
          )
        ) : (
          <div className="clash-field-error">
            {extraCheck.kind === "syntax"
              ? t("admin.routing.settings.extraSyntax", { line: extraCheck.line ?? "?", message: extraCheck.message })
              : extraCheck.kind === "mapping"
                ? t("admin.routing.settings.extraMapping")
                : t("admin.routing.settings.extraReserved", { keys: extraCheck.message })}
          </div>
        )}
        <div>
          <Button iconName="external" href="https://wiki.metacubex.one/config/" target="_blank">
            {t("admin.routing.settings.docs")}
          </Button>
        </div>
      </Group>
    </SpaceBetween>
  );

  return (
    <div className="clash-settings">
      <div className="clash-presets">
        <div className="clash-presets__head">
          <h3>{t("admin.routing.settings.presetsTitle")}</h3>
          <p>{t("admin.routing.settings.presetsHint")}</p>
        </div>
        <div className="clash-presets__grid">
          {(Object.keys(CLASH_PRESETS) as ClashPresetId[]).map((id) => (
            <button key={id} type="button" className="clash-preset" onClick={() => setPresetConfirm(id)}>
              <strong>{t(`admin.routing.settings.preset.${id}.title`)}</strong>
              <span>{t(`admin.routing.settings.preset.${id}.desc`)}</span>
            </button>
          ))}
        </div>
      </div>

      <Tabs
        variant="container"
        activeTabId={tab}
        onChange={({ detail }) => setTab(detail.activeTabId)}
        tabs={[
          { id: "basic", label: t("admin.routing.settings.tabs.basic"), content: basic },
          { id: "network", label: t("admin.routing.settings.tabs.network"), content: network },
          { id: "dns", label: t("admin.routing.settings.tabs.dns"), content: dns },
          { id: "sniffer", label: t("admin.routing.settings.tabs.sniffer"), content: sniffer },
          {
            id: "advanced",
            label: extraCheck.ok ? t("admin.routing.settings.tabs.advanced") : `${t("admin.routing.settings.tabs.advanced")} ⚠`,
            content: advanced,
          },
        ]}
      />

      <Modal
        visible={presetConfirm !== null}
        onDismiss={() => setPresetConfirm(null)}
        header={presetConfirm ? t(`admin.routing.settings.preset.${presetConfirm}.title`) : ""}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setPresetConfirm(null)}>{t("common.cancel")}</Button>
              <Button variant="primary" onClick={() => presetConfirm && applyPreset(presetConfirm)}>{t("admin.routing.settings.presetApply")}</Button>
            </SpaceBetween>
          </Box>
        }
      >
        {presetConfirm && (
          <div className="clash-preset-confirm">
            <p>{t(`admin.routing.settings.preset.${presetConfirm}.detail`)}</p>
            <p className="clash-row__desc">{t("admin.routing.settings.presetNote")}</p>
          </div>
        )}
      </Modal>
    </div>
  );
}
