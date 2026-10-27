"""Run: python3 -m unittest discover -s tests/access_domains -v"""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import ssl
import subprocess
import tempfile
import unittest
from unittest.mock import MagicMock, patch
from urllib.error import HTTPError
from urllib.parse import parse_qs, urlsplit

SCRIPT = Path(__file__).resolve().parents[2] / "scripts/add-access-domain.py"
SPEC = importlib.util.spec_from_file_location("add_access_domain", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
ZONE = "a" * 32
DOMAIN = "access.example.com"
ORIGIN = "18.232.151.51"
ORIGINAL = "(panel) {\n reverse_proxy web:8080\n}\nold.example.com {\n import panel\n}\n"


class Response(io.BytesIO):
    status = 200


class AccessDomainTest(unittest.TestCase):
    """Mutable fixtures model only network and Docker process boundaries."""

    def setUp(self):
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.root = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        self.config = self.root / "production.Caddyfile"
        self.config.write_text(ORIGINAL)
        self.before = ORIGINAL
        self.inode = self.config.stat().st_ino
        self.records = []
        self.entry = None
        self.events = []
        self.validate_failure = False
        self.reload_failures = 0
        self.health_status = 200
        self.origin_failure = False
        self.origin_failures = 0
        self.api_failure = False
        self.output = io.StringIO()
        self.stack.enter_context(contextlib.redirect_stdout(self.output))
        self.stack.enter_context(contextlib.redirect_stderr(self.output))
        self.stack.enter_context(patch.dict(os.environ, {"CLOUDFLARE_API_TOKEN": "private-token"}, clear=True))
        self.network = self.stack.enter_context(patch.object(MODULE, "build_opener"))
        self.network.return_value.open.side_effect = self.open
        self.process = self.stack.enter_context(patch.object(MODULE.subprocess, "run", side_effect=self.docker))
        self.stack.enter_context(patch.object(MODULE.socket, "create_connection", return_value=MagicMock()))
        context = self.stack.enter_context(patch.object(MODULE.ssl, "create_default_context"))
        context.return_value.wrap_socket.side_effect = self.certificate
        self.sleep = self.stack.enter_context(patch.object(MODULE.time, "sleep"))

    def certificate(self, connection, *, server_hostname):
        self.assertEqual(server_hostname, DOMAIN)
        self.events.append("certificate")
        if self.origin_failure:
            raise ssl.SSLCertVerificationError("not issued")
        if self.origin_failures:
            self.origin_failures -= 1
            raise ssl.SSLCertVerificationError("certificate issuance retry")
        return MagicMock()

    def open(self, request, timeout):
        url = request if isinstance(request, str) else request.full_url
        parts = urlsplit(url)
        if parts.hostname == DOMAIN:
            self.events.append("health")
            if self.health_status != 200:
                raise HTTPError(url, self.health_status, "redirect", {"Location": url}, io.BytesIO())
            return Response(b"ok")
        method = request.get_method()
        path = parts.path.removeprefix("/client/v4")
        self.events.append((method, path))
        if self.api_failure:
            raise HTTPError(url, 403, "forbidden", {}, io.BytesIO(b'{"errors":[{"code":10000}]}'))
        data = json.loads(request.data) if request.data else None
        if parts.hostname == "169.254.169.254":
            return Response(b"imds-token" if method == "PUT" else ORIGIN.encode())
        self.assertEqual(parts.hostname, "api.cloudflare.com")
        self.assertEqual(request.get_header("Authorization"), "Bearer private-token")
        if path == "/zones":
            result = [{"id": ZONE, "name": "example.com"}] if parse_qs(parts.query)["name"] == ["example.com"] else []
        elif path == "/zones/" + ZONE:
            result = {"id": ZONE, "name": "example.com"}
        elif path.endswith("/dns_records"):
            if method == "POST":
                self.records.append(data)
            else:
                self.assertEqual(parse_qs(parts.query)["name"], [DOMAIN])
            result = self.records
        elif path.endswith("/entrypoint"):
            if self.entry is None:
                raise HTTPError(url, 404, "absent", {}, io.BytesIO())
            result = self.entry
        elif path.endswith("/rulesets") and method == "POST":
            self.entry = {"id": "ruleset", **data}
            result = self.entry
        elif path.endswith("/rulesets/ruleset/rules") and method == "POST":
            self.entry["rules"].append(data)
            result = self.entry
        elif "/rulesets/ruleset/rules/" in path and method == "PATCH":
            self.assertEqual(data, {"position": {"after": ""}})
            rule_id = path.rsplit("/", 1)[1]
            index = next(i for i, rule in enumerate(self.entry["rules"]) if rule["id"] == rule_id)
            self.entry["rules"].append(self.entry["rules"].pop(index))
            result = self.entry
        else:
            self.fail(f"Unexpected API call: {method} {path}")
        return Response(json.dumps({"success": True, "result": result}).encode())

    def docker(self, command, **kwargs):
        self.assertEqual(command[0], "docker")
        if command[1] == "inspect":
            stdout = json.dumps([{"Source": str(self.config), "Destination": "/etc/caddy/Caddyfile"}])
            return subprocess.CompletedProcess(command, 0, stdout, "")
        self.assertIn(MODULE.CONTAINER, command)
        action = "validate" if "validate" in command else "reload"
        self.events.append(action)
        if action == "validate":
            self.assertIn("--adapter", command)
            self.assertIn("import panel", kwargs["input"])
            self.assertEqual(self.config.read_text(), self.before)
            failed = self.validate_failure
        else:
            failed = self.reload_failures > 0
            self.reload_failures -= int(failed)
        return subprocess.CompletedProcess(command, int(failed), "", "invalid" if failed else "")

    def run_cli(self, *extra):
        self.before = self.config.read_text()
        return MODULE.main([DOMAIN, "--origin-ip", ORIGIN, "--project-dir", str(self.root), *extra])

    def test_injection_rejected_before_any_side_effect(self):
        for domain in ("https://a.test", "a.test/path", "*.a.test", "a.test\n{}", "a.test;{}", "한글.test", "-a.test", "a..test"):
            with self.subTest(domain=domain):
                self.assertEqual(MODULE.main(["--", domain]), 1)
        self.network.assert_not_called()
        self.process.assert_not_called()
        self.assertEqual(self.config.read_text(), ORIGINAL)

    def test_missing_noninteractive_token_rejected_before_mutation(self):
        with patch.dict(os.environ, {}, clear=True), patch.object(MODULE.sys.stdin, "isatty", return_value=False):
            self.assertEqual(self.run_cli(), 1)
        self.network.assert_not_called()
        self.process.assert_not_called()

    def test_nonpublic_and_nonipv4_origins_rejected_before_side_effects(self):
        for address in ("127.0.0.1", "10.0.0.1", "::1", "224.0.0.1", "invalid"):
            with self.subTest(address=address):
                self.assertEqual(self.run_cli("--origin-ip", address), 1)
        self.network.assert_not_called()
        self.process.assert_not_called()

    def test_existing_record_conflict_preserves_config_and_remote(self):
        self.records = [{"type": "AAAA", "content": "::1", "name": DOMAIN}]
        self.assertEqual(self.run_cli(), 1)
        self.assertEqual(self.config.read_text(), ORIGINAL)
        self.assertEqual(len(self.records), 1)
        self.assertFalse(any(isinstance(e, tuple) and e[0] == "POST" for e in self.events))

    def test_success_creates_host_strict_after_origin_certificate_and_is_idempotent(self):
        self.assertEqual(self.run_cli(), 0)
        self.assertEqual(self.config.stat().st_ino, self.inode)
        self.assertTrue(self.config.read_text().startswith(ORIGINAL))
        self.assertEqual(self.records, [{"type": "A", "name": DOMAIN, "content": ORIGIN, "ttl": 1, "proxied": True}])
        self.assertEqual(self.entry["rules"], [{"action": "set_config", "action_parameters": {"ssl": "strict"},
                                               "expression": f'(http.host eq "{DOMAIN}")', "enabled": True}])
        self.assertLess(self.events.index("reload"), self.events.index("certificate"))
        self.assertLess(self.events.index("certificate"), self.events.index(("POST", f"/zones/{ZONE}/rulesets")))
        self.assertLess(self.events.index("validate"), self.events.index(("POST", f"/zones/{ZONE}/dns_records")))
        self.assertEqual(next(self.root.glob("Caddyfile.backup-*")).read_text(), ORIGINAL)
        self.assertEqual(self.run_cli(), 0)
        self.assertEqual(len(self.records), 1)
        self.assertEqual(len(self.entry["rules"]), 1)
        self.assertEqual(self.config.read_text().count(DOMAIN), 1)
        self.sleep.assert_not_called()

    def test_existing_ruleset_append_preserves_unrelated_rule(self):
        unrelated = {"expression": 'http.host eq "other.example.com"', "action": "set_config",
                     "action_parameters": {"ssl": "flexible"}}
        self.entry = {"id": "ruleset", "rules": [unrelated]}
        self.assertEqual(self.run_cli("--zone-id", ZONE), 0)
        self.assertEqual(self.entry["rules"][0], unrelated)
        self.assertIn(("POST", f"/zones/{ZONE}/rulesets/ruleset/rules"), self.events)
        self.assertEqual(len(self.entry["rules"]), 2)

    def test_existing_strict_rule_moves_after_later_ssl_override_without_duplication(self):
        strict = {"id": "strict", "expression": f'(http.host eq "{DOMAIN}")',
                  "action": "set_config", "action_parameters": {"ssl": "strict"},
                  "enabled": True, "description": "preserved"}
        later = {"id": "later", "expression": f'(http.host eq "{DOMAIN}")',
                 "action": "set_config", "action_parameters": {"ssl": "full"}}
        self.entry = {"id": "ruleset", "rules": [strict, later]}
        self.assertEqual(self.run_cli(), 0)
        self.assertEqual(self.entry["rules"], [later, strict])
        self.assertIn(("PATCH", f"/zones/{ZONE}/rulesets/ruleset/rules/strict"), self.events)
        self.assertIn("certificate", self.events)
        self.assertLess(self.events.index("certificate"),
                        self.events.index(("PATCH", f"/zones/{ZONE}/rulesets/ruleset/rules/strict")))
        self.assertEqual(self.run_cli(), 0)
        self.assertEqual(self.entry["rules"], [later, strict])
        self.assertEqual(self.events.count(("PATCH", f"/zones/{ZONE}/rulesets/ruleset/rules/strict")), 1)

    def test_validation_failure_precedes_remote_writes_and_preserves_config(self):
        self.validate_failure = True
        self.assertEqual(self.run_cli(), 1)
        self.assertEqual(self.config.read_text(), ORIGINAL)
        self.network.assert_not_called()

    def test_reload_failure_restores_mounted_inode_and_reports_partial_dns(self):
        self.reload_failures = 1
        self.assertEqual(self.run_cli(), 1)
        self.assertEqual(self.config.read_text(), ORIGINAL)
        self.assertEqual(self.config.stat().st_ino, self.inode)
        self.assertEqual(self.events.count("reload"), 2)
        self.assertEqual(len(self.records), 1)
        self.assertIsNone(self.entry)
        self.assertIn(f"/zones/{ZONE}/dns_records", self.output.getvalue())

    def test_dns_only_waits_for_certificate_retry_without_creating_rules(self):
        self.origin_failures = 20
        self.assertEqual(self.run_cli("--dns-only"), 0)
        self.assertFalse(self.records[0]["proxied"])
        self.assertIsNone(self.entry)
        self.assertEqual(self.origin_failures, 0)
        self.assertLess(self.events.index("certificate"), self.events.index("health"))

    def test_origin_certificate_failure_keeps_dns_config_but_never_enables_strict(self):
        self.origin_failure = True
        self.assertEqual(self.run_cli(), 1)
        self.assertIsNone(self.entry)
        self.assertEqual(len(self.records), 1)
        self.assertIn(DOMAIN, self.config.read_text())
        self.assertEqual(self.events.count("certificate"), 41)

    def test_origin_certificate_can_recover_after_acme_retry_delay(self):
        self.origin_failures = 20
        self.assertEqual(self.run_cli(), 0)
        self.assertEqual(self.origin_failures, 0)
        self.assertIsNotNone(self.entry)
        self.assertLess(self.events.index("certificate"), self.events.index(("POST", f"/zones/{ZONE}/rulesets")))

    def test_https_redirect_is_not_success(self):
        self.health_status = 308
        self.assertEqual(self.run_cli(), 1)
        self.assertEqual(self.events.count("health"), 12)
        self.assertIsNotNone(self.entry)
        handler = MODULE.NoRedirect()
        self.assertIsNone(handler.redirect_request(None, None, 308, "", {}, "https://elsewhere.test"))

    def test_api_permission_failure_is_readable_and_does_not_leak_token(self):
        self.api_failure = True
        self.assertEqual(self.run_cli(), 1)
        self.assertEqual(self.config.read_text(), ORIGINAL)
        self.assertNotIn("private-token", self.output.getvalue())
        self.assertIn("403", self.output.getvalue())

    def test_imdsv2_discovers_public_ip_without_proxy(self):
        self.assertEqual(MODULE.main([DOMAIN, "--project-dir", str(self.root)]), 0)
        self.assertEqual(self.records[0]["content"], ORIGIN)
        self.assertIn(("PUT", "/latest/api/token"), self.events)
        self.assertIn(("GET", "/latest/meta-data/public-ipv4"), self.events)


if __name__ == "__main__":
    unittest.main()
