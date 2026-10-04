"""Run a path-rewritten installer with an allowlisted PATH; never touch the host.

Run: python3 -m unittest discover -s tests/installer -v
"""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest


REPO = Path(__file__).resolve().parents[2]
STUB = Path(__file__).with_name("stub.py")


class InstallerTest(unittest.TestCase):
    def run_install(self, role="relay", manager="apt-get", firewall="ufw", args=(), env=None, existing_xray=False, curlrc=None):
        with tempfile.TemporaryDirectory(prefix="installer-test-") as temp:
            root = Path(temp).resolve()
            tools = root / "tools"
            tools.mkdir()
            # No inherited PATH: a missing stub must fail instead of executing a
            # host package manager, firewall, service manager, or network client.
            for command in ("python3", "jq", "mkdir", "chmod", "cp", "cat", "grep", "sort", "head", "rm"):
                real = shutil.which(command)
                self.assertIsNotNone(real, "Test prerequisite not found: " + command)
                (tools / command).symlink_to(real)
            for command in (manager, "uname", "hostname", "curl", "unzip", "systemctl", "mktemp", firewall):
                if command:
                    target = tools / command
                    shutil.copyfile(STUB, target)
                    target.chmod(0o755)
            (root / "etc/systemd/system").mkdir(parents=True)
            (root / "tmp").mkdir()
            if curlrc is not None:
                (root / ".curlrc").write_text(curlrc)
            if existing_xray:
                (root / "usr/local/bin").mkdir(parents=True)
                (root / "usr/local/bin/xray").write_text("existing xray")

            # Rewrite every absolute installation path, including heredoc output
            # and direct node-agent execution. This is a temporary COPY; the real
            # installer is never run against host paths or with sudo/root.
            script = (REPO / "scripts/install.sh").read_text()
            script = script.replace("[[ $EUID -ne 0 ]]", "[[ 0 -ne 0 ]]")
            for prefix in ("/usr/local/bin", "/etc/node-agent", "/var/log/proxima", "/etc/systemd/system"):
                script = script.replace(prefix, str(root) + prefix)
            target = root / "install.sh"
            target.write_text(script)
            install_env = {
                "PATH": str(tools),
                "HOME": str(root),
                "TMPDIR": str(root / "tmp"),
                "INSTALL_TEST_ROOT": str(root),
                "INSTALL_TEST_ROLE_RESPONSE": json.dumps({"role": role}),
            }
            if env:
                install_env.update(env)
            result = subprocess.run(
                [shutil.which("bash"), str(target), "--server", "https://panel.test/", "--token", "test-token", *args],
                env=install_env, capture_output=True, text=True, timeout=30,
            )
            log = root / "commands.jsonl"
            commands = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
            files = {str(path.relative_to(root)): path.read_text()
                     for prefix in ("usr", "etc") for path in (root / prefix).rglob("*")
                     if path.is_file() and not path.is_symlink()}
            return result, commands, files

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def assert_no_vpn(self, commands, files, existing=False):
        self.assertFalse(any("XTLS/Xray-core" in arg for command in commands for arg in command))
        self.assertFalse(any(command[0] == "unzip" for command in commands))
        self.assertFalse(any("unzip" in command[1:] for command in commands if command[0] in ("apt-get", "yum", "dnf")))
        self.assertEqual("usr/local/bin/xray" in files, existing)

    def test_relay_common_dependencies_and_systemd_without_vpn_for_each_package_manager(self):
        for manager, ip_package, procps_package in (("apt-get", "iproute2", "procps"), ("yum", "iproute", "procps-ng"), ("dnf", "iproute", "procps-ng")):
            with self.subTest(manager=manager):
                result, commands, files = self.run_install(manager=manager)
                self.assert_success(result)
                self.assert_no_vpn(commands, files)
                packages = next(command for command in commands if command[:2] == [manager, "install"])
                for package in ("curl", "ca-certificates", "jq", "systemd", "nftables", ip_package, procps_package):
                    self.assertIn(package, packages)
                self.assertIn("usr/local/bin/node-agent", files)
                self.assertIn("etc/node-agent/config.json", files)
                service = files["etc/systemd/system/node-agent.service"]
                self.assertIn("/usr/local/bin/node-agent run", service)
                self.assertNotIn("xray", service)
                self.assertIn(["systemctl", "daemon-reload"], commands)
                self.assertIn(["systemctl", "enable", "node-agent"], commands)
                self.assertIn(["systemctl", "start", "node-agent"], commands)
                role_call = next(command for command in commands if command[0] == "curl" and command[-1].endswith("/role"))
                self.assertEqual(role_call[-1], "https://panel.test/api/v1/nodes/test-id/role")
                self.assertIn("X-Node-Key: test-key", role_call)

    def test_exit_and_both_register_and_fetch_authoritative_role_before_xray(self):
        for role in ("exit", "both"):
            with self.subTest(role=role):
                result, commands, files = self.run_install(role)
                self.assert_success(result)
                register = next(i for i, command in enumerate(commands) if command[0] == "node-agent")
                role_fetch = next(i for i, command in enumerate(commands) if command[-1].endswith("/role"))
                vpn_packages = next(i for i, command in enumerate(commands) if command[0] == "apt-get" and "unzip" in command)
                version_fetch = next(i for i, command in enumerate(commands) if command[-1].endswith("releases/latest"))
                self.assertLess(register, role_fetch)
                self.assertLess(role_fetch, vpn_packages)
                self.assertLess(vpn_packages, version_fetch)
                self.assertIn("usr/local/bin/xray", files)
                self.assertTrue(any(command[0] == "unzip" for command in commands))
                self.assertTrue(any(command[-1].endswith("v26.3.27/Xray-linux-64.zip") for command in commands))

    def test_role_failures_abort_before_vpn_or_service_start(self):
        scenarios = (
            {"INSTALL_TEST_ROLE_HTTP_FAIL": "1"},
            {"INSTALL_TEST_ROLE_RESPONSE": "not json"},
            {"INSTALL_TEST_ROLE_RESPONSE": "{}"},
            {"INSTALL_TEST_ROLE_RESPONSE": '{"role":null}'},
            {"INSTALL_TEST_ROLE_RESPONSE": '{"role":"future"}'},
            {"INSTALL_TEST_CONFIG": '{"node_id":"test-id"}'},
            {"INSTALL_TEST_CONFIG": '{"node_id":"","api_key":"test-key"}'},
        )
        for scenario in scenarios:
            with self.subTest(scenario=scenario):
                result, commands, files = self.run_install(env=scenario)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Node registered successfully", result.stdout)
                self.assert_no_vpn(commands, files)
                self.assertNotIn(["systemctl", "start", "node-agent"], commands)
                self.assertNotIn("etc/systemd/system/node-agent.service", files)

    def test_role_non_2xx_status_rejects_even_a_valid_exit_body(self):
        for status in ("000", "199", "300", "304", "401", "500"):
            with self.subTest(status=status):
                result, commands, files = self.run_install("exit", env={"INSTALL_TEST_ROLE_HTTP_STATUS": status})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("returned HTTP " + status, result.stdout)
                self.assert_no_vpn(commands, files)
                self.assertNotIn(["systemctl", "start", "node-agent"], commands)

    def test_local_http_role_accepts_2xx_but_never_forwards_key_on_redirect(self):
        real_curl = shutil.which("curl")
        self.assertIsNotNone(real_curl, "Test prerequisite not found: curl")

        class RoleHandler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.server.requests.append((self.path, self.headers.get("X-Node-Key")))
                body = b'{"role":"relay"}'
                self.send_response(self.server.role_status)
                if 300 <= self.server.role_status < 400:
                    self.send_header("Location", self.server.redirect_url)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_):
                pass

        origin = ThreadingHTTPServer(("127.0.0.1", 0), RoleHandler)
        destination = ThreadingHTTPServer(("127.0.0.1", 0), RoleHandler)
        threads = []
        try:
            for server in (origin, destination):
                server.requests = []
                server.role_status = 200
                thread = threading.Thread(target=server.serve_forever, daemon=True)
                thread.start()
                threads.append(thread)
            origin.redirect_url = "http://localhost:%d/redirected-role" % destination.server_port
            for status in (200, 301, 302, 303, 307, 308):
                with self.subTest(status=status):
                    origin.role_status = status
                    result, commands, files = self.run_install(
                        args=("--server", "http://127.0.0.1:%d" % origin.server_port),
                        env={"INSTALL_TEST_REAL_ROLE_CURL": real_curl},
                        # A default curl config must not restore redirect following.
                        curlrc="location\n",
                    )
                    self.assertEqual(origin.requests[-1], ("/api/v1/nodes/test-id/role", "test-key"))
                    self.assertEqual(destination.requests, [], "Credential-bearing request reached redirect destination")
                    self.assert_no_vpn(commands, files)
                    if status == 200:
                        self.assert_success(result)
                        self.assertIn(["systemctl", "start", "node-agent"], commands)
                    else:
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn("returned HTTP " + str(status), result.stdout)
                        self.assertNotIn(["systemctl", "start", "node-agent"], commands)
        finally:
            for server in (origin, destination):
                server.shutdown()
                server.server_close()
            for thread in threads:
                thread.join()

    def test_registration_failure_aborts_before_role_and_vpn(self):
        result, commands, files = self.run_install(env={"INSTALL_TEST_REGISTER_FAIL": "1"})
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_vpn(commands, files)
        self.assertFalse(any(command[-1].endswith("/role") for command in commands))
        self.assertNotIn(["systemctl", "start", "node-agent"], commands)

    def test_relay_preserves_custom_firewall_and_registration_argument_boundaries(self):
        result, commands, _ = self.run_install(args=(
            "--name", "Relay Node 'one'", "--country", "US", "--region", "New York", "--port", "8443", "--ports", "80,8443,23001-23005",
        ))
        self.assert_success(result)
        self.assertIn(["ufw", "allow", "80/tcp"], commands)
        self.assertIn(["ufw", "allow", "8443/tcp"], commands)
        self.assertIn(["ufw", "allow", "23001:23005/tcp"], commands)
        self.assertNotIn(["ufw", "allow", "20001:22000/tcp"], commands)
        register = next(command for command in commands if command[0] == "node-agent")
        self.assertEqual(register[register.index("--name") + 1], "Relay Node 'one'")
        self.assertEqual(register[register.index("--region") + 1], "New York")

    def test_legacy_arguments_keep_default_firewall_for_all_roles(self):
        for role in ("relay", "exit", "both"):
            with self.subTest(role=role):
                result, commands, _ = self.run_install(role)
                self.assert_success(result)
                self.assertIn(["ufw", "allow", "443/tcp"], commands)
                self.assertIn(["ufw", "allow", "20001:22000/tcp"], commands)

    def test_firewalld_keeps_tcp_range_syntax(self):
        result, commands, _ = self.run_install(firewall="firewall-cmd", args=("--ports", "8443,23001-23005"))
        self.assert_success(result)
        self.assertIn(["firewall-cmd", "--permanent", "--add-port=8443/tcp"], commands)
        self.assertIn(["firewall-cmd", "--permanent", "--add-port=23001-23005/tcp"], commands)
        self.assertIn(["firewall-cmd", "--reload"], commands)

    def test_no_firewall_warns_without_changing_ports(self):
        result, _, _ = self.run_install(firewall="", args=("--ports", "8443"))
        self.assert_success(result)
        self.assertIn("open 8443/tcp manually", result.stdout)

    def test_invalid_ports_fail_before_any_install_command(self):
        for ports in ("443;echo bad", "0", "65536", "22000-20001"):
            with self.subTest(ports=ports):
                result, commands, _ = self.run_install(args=("--ports", ports))
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(commands, [["hostname"]])

    def test_relay_does_not_remove_existing_xray_or_validate_unused_xray_pin(self):
        result, commands, files = self.run_install(existing_xray=True, env={"XRAY_VERSION": "v1.0.0"})
        self.assert_success(result)
        self.assert_no_vpn(commands, files, existing=True)
        self.assertEqual(files["usr/local/bin/xray"], "existing xray")

    def test_exit_retains_xray_version_floor(self):
        result, commands, files = self.run_install("exit", env={"XRAY_VERSION": "v24.12.31"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("older than the required v25.1.1", result.stdout)
        self.assertNotIn("usr/local/bin/xray", files)
        self.assertNotIn(["systemctl", "start", "node-agent"], commands)

    def test_exit_arm64_pin_keeps_architecture_download_without_latest_lookup(self):
        result, commands, files = self.run_install("exit", env={"XRAY_VERSION": "v25.1.1", "INSTALL_TEST_ARCH": "aarch64"})
        self.assert_success(result)
        self.assertIn("usr/local/bin/xray", files)
        self.assertTrue(any(command[-1].endswith("v25.1.1/Xray-linux-arm64-v8a.zip") for command in commands))
        self.assertFalse(any(command[-1].endswith("releases/latest") for command in commands))

    def test_relay_keeps_agent_download_fallback_without_xray(self):
        result, commands, files = self.run_install(env={"INSTALL_TEST_AGENT_FALLBACK": "1"})
        self.assert_success(result)
        self.assert_no_vpn(commands, files)
        self.assertTrue(any("proximavpn/proxima-vpn/releases/latest/download/node-agent-linux-amd64" in command[-1] for command in commands))


if __name__ == "__main__":
    unittest.main()
