#!/usr/bin/env python3
"""Host/network command stubs used only by test_install.py's temporary sandbox."""

import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
from urllib.parse import urlsplit


root = Path(os.environ["INSTALL_TEST_ROOT"])
name = Path(sys.argv[0]).name
args = sys.argv[1:]
with (root / "commands.jsonl").open("a") as log:
    log.write(json.dumps([name, *args]) + "\n")


def sandbox_path(value):
    path = Path(value).resolve()
    if root not in path.parents:
        raise RuntimeError("Refusing a write outside the installer test sandbox: " + value)
    return path


if name in ("apt-get", "yum", "dnf"):
    pass
elif name == "uname":
    print("Linux" if args == ["-s"] else os.environ.get("INSTALL_TEST_ARCH", "x86_64"))
elif name == "hostname":
    print("test-node")
elif name == "curl":
    url = args[-1]
    if url.endswith("/role"):
        real_curl = os.environ.get("INSTALL_TEST_REAL_ROLE_CURL")
        if real_curl:
            if urlsplit(url).hostname not in ("127.0.0.1", "localhost"):
                raise RuntimeError("Real curl is only permitted for a loopback role request")
            os.execv(real_curl, [real_curl, *args])
        if os.environ.get("INSTALL_TEST_ROLE_HTTP_FAIL"):
            sys.exit(22)
        response = os.environ.get("INSTALL_TEST_ROLE_RESPONSE", '{"role":"relay"}')
        sys.stdout.write(response)
        if "--write-out" in args:
            status = os.environ.get("INSTALL_TEST_ROLE_HTTP_STATUS", "200")
            sys.stdout.write(args[args.index("--write-out") + 1].replace("%{http_code}", status))
    elif url == "https://api.github.com/repos/XTLS/Xray-core/releases/latest":
        print('{"tag_name":"v26.3.27"}')
    elif "/downloads/node-agent-" in url or "/download/node-agent-" in url:
        if "/downloads/" in url and os.environ.get("INSTALL_TEST_AGENT_FALLBACK"):
            sys.exit(22)
        target = sandbox_path(args[args.index("-o") + 1])
        shutil.copyfile(__file__, target)
    elif url.endswith(".zip") and "XTLS/Xray-core" in url:
        sandbox_path(args[args.index("-o") + 1]).write_text("stub archive")
    else:
        raise RuntimeError("Unexpected network request: " + url)
elif name == "node-agent":
    if args[0] != "register":
        raise RuntimeError("Unexpected node-agent command")
    if os.environ.get("INSTALL_TEST_REGISTER_FAIL"):
        sys.exit(1)
    config = sandbox_path(str(root / "etc/node-agent/config.json"))
    config.write_text(os.environ.get(
        "INSTALL_TEST_CONFIG",
        '{"node_id":"test-id","api_key":"test-key","server_url":"https://panel.test"}',
    ))
    config.chmod(0o600)
elif name == "unzip":
    target = sandbox_path(args[args.index("-d") + 1])
    target.mkdir(parents=True)
    (target / "xray").write_text("stub xray")
elif name == "mktemp":
    if args != ["-d"]:
        raise RuntimeError("Unexpected mktemp arguments")
    print(tempfile.mkdtemp(dir=root / "tmp"))
elif name == "systemctl":
    if args == ["is-active", "node-agent"]:
        print("active")
elif name == "ufw":
    if args == ["status"]:
        print("Status: active")
elif name == "firewall-cmd":
    if args == ["--state"]:
        print("running")
else:
    raise RuntimeError("Unexpected stub command: " + name)
