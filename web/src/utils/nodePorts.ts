import type { NodeFirewallPreset, NodePortSpec } from "../api/types";

/**
 * Mirrors pkg/nodeprov. The server is authoritative and re-resolves everything
 * it is sent; this exists so the wizard can preview the resulting port list
 * without a round trip.
 */

// Speed-limited plans bind inbounds at PortBase+Mbps (pkg/speedtier), so this
// range is always opened and is never the operator's choice.
export const TIER_PORT_START = 20001;
export const TIER_PORT_END = 22000;

export const DEFAULT_SERVICE_PORT = 443;

export const FIREWALL_PRESETS: NodeFirewallPreset[] = [
  "standard",
  "web_alt",
  "high_port",
  "custom",
];

export function isValidPort(port: number): boolean {
  return Number.isInteger(port) && port >= 1 && port <= 65535;
}

export function isValidSpec(spec: NodePortSpec): boolean {
  return isValidPort(spec.start) && isValidPort(spec.end) && spec.end >= spec.start;
}

function presetExtras(preset: NodeFirewallPreset): NodePortSpec[] {
  return preset === "web_alt" ? [{ start: 80, end: 80 }] : [];
}

/** Sorts and coalesces specs that overlap or touch. */
function merge(specs: NodePortSpec[]): NodePortSpec[] {
  const sorted = [...specs].sort((a, b) => a.start - b.start || a.end - b.end);

  const out: NodePortSpec[] = [];
  for (const spec of sorted) {
    const last = out[out.length - 1];
    if (last !== undefined && spec.start <= last.end + 1) {
      last.end = Math.max(last.end, spec.end);
      continue;
    }
    out.push({ ...spec });
  }
  return out;
}

export function resolvePorts(
  preset: NodeFirewallPreset,
  servicePort: number,
  custom: NodePortSpec[],
): NodePortSpec[] {
  const specs: NodePortSpec[] = [
    { start: servicePort, end: servicePort },
    { start: TIER_PORT_START, end: TIER_PORT_END },
    ...presetExtras(preset),
  ];
  if (preset === "custom") {
    specs.push(...custom.filter(isValidSpec));
  }
  return merge(specs);
}

export function formatPorts(specs: NodePortSpec[]): string {
  return specs.map((s) => (s.start === s.end ? `${s.start}` : `${s.start}-${s.end}`)).join(",");
}

/**
 * Parses the operator's free-text port entry, e.g. "8080, 9000-9100". Returns
 * the specs parsed so far plus the entries that were not understood, so the form
 * can show which fragment is wrong instead of rejecting the whole field.
 */
export function parsePortInput(input: string): { specs: NodePortSpec[]; invalid: string[] } {
  const specs: NodePortSpec[] = [];
  const invalid: string[] = [];

  for (const raw of input.split(",")) {
    const field = raw.trim();
    if (field === "") continue;

    const match = /^(\d+)(?:\s*-\s*(\d+))?$/.exec(field);
    if (!match) {
      invalid.push(field);
      continue;
    }
    const start = Number(match[1]);
    const end = match[2] != null ? Number(match[2]) : start;
    const spec = { start, end };
    if (!isValidSpec(spec)) {
      invalid.push(field);
      continue;
    }
    specs.push(spec);
  }

  return { specs: merge(specs), invalid };
}
