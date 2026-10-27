#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = []
# ///
# How to run: python3 scripts/add-access-domain.py access.example.com
"""Add one access hostname without replacing existing DNS or Caddy settings."""
from __future__ import annotations

import argparse
import fcntl
import getpass
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import ssl
import subprocess
import sys
import tempfile
import time
from typing import Final, TextIO, TypedDict
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener

CONTAINER: Final = "proxima-vpn-caddy-1"
CONFIG: Final = "/etc/caddy/Caddyfile"
API: Final = "https://api.cloudflare.com/client/v4"
PERMISSIONS: Final = "Zone Read, DNS Edit, Config Rules Edit (Config Settings Write)"


class DomainError(Exception):
    """An operational failure safe to display at the CLI boundary."""


class Data(TypedDict, total=False):
    id: str
    name: str
    type: str
    content: str
    ttl: int
    proxied: bool
    action: str
    action_parameters: dict[str, str]
    expression: str
    enabled: bool
    position: dict[str, str]
    rules: list[Data]
    kind: str
    phase: str


class NoRedirect(HTTPRedirectHandler):
    # urllib's override contract requires these positional arguments.
    def redirect_request(self, req, fp, code, msg, headers, newurl) -> None:
        return None


def hostname(value: str) -> str:
    labels = value.lower().split(".")
    if (not value.isascii() or len(value) > 253 or len(labels) < 2 or labels[-1].isdigit()
            or any(not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", x)
                   for x in labels)):
        raise DomainError("DOMAIN must be an ASCII hostname, without scheme/path/wildcards.")
    return ".".join(labels)


def public_ipv4(value: str) -> str:
    address = ipaddress.IPv4Address(value)
    if not address.is_global or address.is_multicast:
        raise DomainError("Origin must be a public IPv4 address.")
    return str(address)


def origin_ip() -> str:
    """Use IMDSv2 directly, never via an environment HTTP proxy."""
    opener = build_opener(ProxyHandler({}), NoRedirect())
    base = "http://169.254.169.254/latest/"
    request = Request(base + "api/token", method="PUT",
                      headers={"X-aws-ec2-metadata-token-ttl-seconds": "60"})
    with opener.open(request, timeout=3) as response:
        token = response.read().decode()
    request = Request(base + "meta-data/public-ipv4",
                      headers={"X-aws-ec2-metadata-token": token})
    with opener.open(request, timeout=3) as response:
        return public_ipv4(response.read().decode().strip())


class Cloudflare:
    """Keep the credential only in memory; report remote write attempts on failure."""

    def __init__(self, token: str):
        self.token = token
        self.attempts: list[str] = []

    def call(self, method: str, path: str, data: Data | None = None):
        if method != "GET":
            self.attempts.append(f"{method} {path}")
        request = Request(API + path, method=method,
                          data=None if data is None else json.dumps(data).encode(),
                          headers={"Authorization": "Bearer " + self.token,
                                   "Content-Type": "application/json"})
        try:
            with build_opener(NoRedirect()).open(request, timeout=15) as response:
                payload = json.load(response)
        except HTTPError as error:
            with error:
                detail = error.read(8192).decode(errors="replace")
            # Never echo response bodies, which could contain credentials.
            codes = re.findall(r'"code"\s*:\s*(\d+)', detail)
            raise DomainError(f"Cloudflare HTTP {error.code}, codes {codes}; "
                              f"check token permissions: {PERMISSIONS}.") from None
        if not payload.get("success"):
            raise DomainError(f"Cloudflare rejected {method} {path}; check {PERMISSIONS}.")
        return payload["result"]

    def zone(self, domain: str, zone_id: str | None) -> str:
        if zone_id:
            if not re.fullmatch(r"[a-fA-F0-9]{32}", zone_id):
                raise DomainError("--zone-id must be a 32-character hexadecimal zone ID.")
            zone = self.call("GET", "/zones/" + zone_id)
            if not (domain == zone["name"] or domain.endswith("." + zone["name"])):
                raise DomainError("DOMAIN does not belong to --zone-id.")
            return zone_id
        labels = domain.split(".")
        for offset in range(len(labels) - 1):
            zones = self.call("GET", "/zones?" + urlencode({"name": ".".join(labels[offset:])}))
            if zones:
                return zones[0]["id"]
        raise DomainError("No accessible Cloudflare zone; check Zone Read and zone scope.")


def docker(args: list[str], content: str | None = None) -> str:
    result = subprocess.run(["docker", *args], input=content, capture_output=True,
                            text=True, timeout=30, check=False)
    if result.returncode:
        raise DomainError(f"Docker/Caddy {args[0]} failed: {result.stderr.strip()}")
    return result.stdout


def caddy_path(project: Path) -> Path:
    mounts = json.loads(docker(["inspect", "--format", "{{json .Mounts}}", CONTAINER]))
    for mount in mounts:
        if mount["Destination"] in (CONFIG, "/etc/caddy"):
            path = Path(mount["Source"])
            if mount["Destination"] == "/etc/caddy":
                path /= "Caddyfile"
            path = path.resolve(strict=True)
            if not path.is_relative_to(project.resolve()):
                raise DomainError("Caddyfile is outside --project-dir; specify its project root.")
            return path
    raise DomainError("Running Caddy has no supported /etc/caddy/Caddyfile bind mount.")


def reload_caddy() -> None:
    docker(["exec", CONTAINER, "caddy", "reload", "--config", CONFIG, "--adapter", "caddyfile"])


def overwrite(active: TextIO, content: str) -> None:
    active.seek(0)
    active.write(content)
    active.truncate()
    active.flush()
    os.fsync(active.fileno())


def origin_ready(domain: str, address: str) -> None:
    """Wait for the origin certificate with real SNI before enabling strict."""
    context = ssl.create_default_context()
    for attempt in range(41):
        try:
            with socket.create_connection((address, 443), timeout=5) as connection:
                with context.wrap_socket(connection, server_hostname=domain):
                    return
        except OSError as error:
            reason = str(error)
        if attempt < 40:
            time.sleep(3)
    raise DomainError(f"Origin certificate not ready; host strict was not enabled: {reason}")


def health(domain: str) -> None:
    """Bound ACME readiness to 12 attempts; redirects are failures, not followed."""
    opener = build_opener(NoRedirect())
    for attempt in range(12):
        try:
            with opener.open("https://" + domain + "/health", timeout=5) as response:
                if response.status == 200:
                    return
                reason = f"HTTP {response.status}"
        except (URLError, TimeoutError, OSError) as error:
            reason = str(error)
        if attempt < 11:
            time.sleep(3)
    raise DomainError(f"HTTPS /health not ready (certificate checked, redirects disabled): {reason}")


def apply(args: argparse.Namespace, cf: Cloudflare) -> None:
    path = caddy_path(args.project_dir)
    # Lock the mounted inode, not a replaceable sidecar; preserve it on every write.
    with path.open("r+", encoding="utf-8") as active:
        fcntl.flock(active, fcntl.LOCK_EX | fcntl.LOCK_NB)
        original = active.read()
        headers = re.findall(r"(?m)^\s*([^#{}\n]+)\s*\{", original)
        present = any(args.domain in re.split(r"[\s,]+", header.strip()) for header in headers)
        candidate = original if present else original + f"\n\n{args.domain} {{\n\timport panel\n}}\n"
        docker(["exec", "-i", CONTAINER, "caddy", "validate", "--config", "/dev/stdin",
                "--adapter", "caddyfile"], candidate)
        zone = cf.zone(args.domain, args.zone_id)
        base = "/zones/" + zone
        records = cf.call("GET", base + "/dns_records?" + urlencode({"name": args.domain, "per_page": 100}))
        if len(records) >= 100:
            raise DomainError("Too many exact records to inspect safely; no changes made.")
        expected: Data = {"type": "A", "name": args.domain, "content": args.origin_ip,
                          "ttl": 1, "proxied": not args.dns_only}
        if any(any(record.get(key) != value for key, value in expected.items()) for record in records):
            raise DomainError("Existing exact DNS record conflicts; no records or config were changed.")
        entry = None
        rule: Data = {"action": "set_config", "action_parameters": {"ssl": "strict"},
                      "expression": f'(http.host eq "{args.domain}")', "enabled": True}
        if not args.dns_only:
            try:
                entry = cf.call("GET", base + "/rulesets/phases/http_config_settings/entrypoint")
            except DomainError as error:
                # Only an absent phase entrypoint is safe to create, not a permission error.
                if "HTTP 404," not in str(error):
                    raise
        ssl_rules = [r for r in entry.get("rules", []) if r.get("enabled", True)
                     and r.get("action") == "set_config"
                     and "ssl" in r.get("action_parameters", {})] if entry else []
        existing_strict = next((r for r in reversed(ssl_rules)
            if r.get("action_parameters", {}).get("ssl") == "strict"
            and re.sub(r"[\s()]", "", r.get("expression", "")) == f'http.hosteq"{args.domain}"'
            ), None)
        # A later SSL rule may override this host; do not parse arbitrary expressions.
        matching = bool(ssl_rules and existing_strict is ssl_rules[-1])
        if not records:
            cf.call("POST", base + "/dns_records", expected)
        if candidate != original:
            with tempfile.NamedTemporaryFile(prefix="Caddyfile.backup-", dir=path.parent,
                                             mode="w", encoding="utf-8", delete=False) as backup:
                backup.write(original)
                print("Backup:", backup.name)
            try:
                overwrite(active, candidate)
                reload_caddy()
            except (DomainError, OSError, subprocess.SubprocessError):
                try:
                    overwrite(active, original)
                    reload_caddy()
                except (DomainError, OSError, subprocess.SubprocessError) as rollback_error:
                    raise DomainError(f"Reload failed and rollback needs intervention; "
                                      f"backup {backup.name}: {rollback_error}") from None
                raise
        if args.dns_only:
            origin_ready(args.domain, args.origin_ip)
        elif not matching:
            origin_ready(args.domain, args.origin_ip)
            if entry:
                rule_path = base + "/rulesets/" + entry["id"] + "/rules"
                if existing_strict:
                    cf.call("PATCH", rule_path + "/" + existing_strict["id"],
                            {"position": {"after": ""}})
                else:
                    cf.call("POST", rule_path, rule)
            else:
                cf.call("POST", base + "/rulesets", {
                    "name": "Access hostname TLS", "kind": "zone", "phase": "http_config_settings",
                    "rules": [rule]})
    health(args.domain)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("domain")
    parser.add_argument("--origin-ip", default=os.environ.get("PROXIMA_ORIGIN_IP"))
    parser.add_argument("--dns-only", action="store_true")
    parser.add_argument("--project-dir", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--zone-id")
    args = parser.parse_args(argv)
    cf = Cloudflare("")
    try:
        args.domain = hostname(args.domain)
        if args.origin_ip:
            args.origin_ip = public_ipv4(args.origin_ip)
        token = os.environ.get("CLOUDFLARE_API_TOKEN", "")
        if not token and sys.stdin.isatty():
            token = getpass.getpass("Cloudflare API token: ")
        if not token:
            raise DomainError("CLOUDFLARE_API_TOKEN required in a noninteractive terminal.")
        cf = Cloudflare(token)
        args.origin_ip = args.origin_ip or origin_ip()
        apply(args, cf)
    except (DomainError, OSError, URLError, subprocess.SubprocessError, ValueError,
            KeyError, TypeError, EOFError) as error:
        print("Failed:", str(error).replace(cf.token, "[redacted]") if cf.token else str(error),
              file=sys.stderr)
        print("Remote writes attempted (may persist; not rolled back): "
              + (", ".join(cf.attempts) or "none"), file=sys.stderr)
        return 1
    print(f"Ready: https://{args.domain}/health")
    return 0


if __name__ == "__main__":
    sys.exit(main())
