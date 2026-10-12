#!/usr/bin/env python3
"""Create and inspect the agents' GitHub Apps for the local workspace.

  github-app.py create <agent> <role>   open GitHub's App manifest flow in the browser;
                                        saves the App's key to .state/github/<agent>.pem
  github-app.py installation <agent>    find the App's installation and print its ID

Roles set the App's permissions: developer, reviewer, orchestrator. The Apps
are named ws-local-<agent>: test Apps for this machine, separate from the
example-<agent> Apps whose keys go into KMS in the cloud. Delete them when done.
"""

import base64
import html
import http.server
import json
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import webbrowser
from pathlib import Path

ORG = "example-org"
PORT = 8765
STATE = Path(__file__).resolve().parent.parent / ".state" / "github"

PERMISSIONS = {
    "developer": {"contents": "write", "pull_requests": "write", "metadata": "read"},
    "reviewer": {"contents": "read", "pull_requests": "write", "metadata": "read"},
    "orchestrator": {
        "contents": "write",
        "pull_requests": "write",
        "metadata": "read",
        "checks": "read",
    },
}


def manifest(agent: str, role: str) -> dict:
    return {
        "name": f"ws-local-{agent}",
        "url": "https://github.com/example-org/workspace",
        "description": f"Local test App for the agent {agent} ({role}). Safe to delete.",
        "hook_attributes": {
            "url": "https://example.invalid/no-webhooks",
            "active": False,
        },
        "redirect_url": f"http://localhost:{PORT}/callback",
        "public": False,
        "default_permissions": PERMISSIONS[role],
        "default_events": [],
    }


def create(agent: str, role: str) -> None:
    if role not in PERMISSIONS:
        sys.exit(f"role must be one of {', '.join(PERMISSIONS)}")
    STATE.mkdir(parents=True, exist_ok=True)
    if (STATE / f"{agent}.pem").exists():
        sys.exit(f"{agent} already has an App key in {STATE}")
    state = secrets.token_urlsafe(16)
    form = f"""<!doctype html><meta charset="utf-8"><title>Create ws-local-{agent}</title>
<form id="f" method="post" action="https://github.com/organizations/{ORG}/settings/apps/new?state={state}">
<input type="hidden" name="manifest" value="{html.escape(json.dumps(manifest(agent, role)))}">
<p>Creating the GitHub App <b>ws-local-{agent}</b> ({role}) in {ORG}…</p><button>Continue to GitHub</button></form>
<script>document.getElementById('f').submit()</script>"""
    done = {}

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            url = urllib.parse.urlparse(self.path)
            if url.path == "/":
                self._send(200, form)
                return
            q = urllib.parse.parse_qs(url.query)
            if url.path != "/callback" or q.get("state", [""])[0] != state:
                self._send(400, "unexpected request")
                return
            code = q["code"][0]
            req = urllib.request.Request(
                f"https://api.github.com/app-manifests/{code}/conversions",
                method="POST",
                headers={"Accept": "application/vnd.github+json"},
            )
            app = json.load(urllib.request.urlopen(req))
            pem = STATE / f"{agent}.pem"
            pem.write_text(app["pem"])
            pem.chmod(0o600)
            # Keep only what the broker needs; the client and webhook secrets are not used.
            (STATE / f"{agent}.json").write_text(
                json.dumps(
                    {
                        "agent": agent,
                        "role": role,
                        "app_id": app["id"],
                        "slug": app["slug"],
                    },
                    indent=2,
                )
            )
            done["slug"] = app["slug"]
            self._send(
                200,
                f"<p>Created <b>{app['slug']}</b>. Now install it on <b>{ORG}/workspace-sandbox</b> only:</p>"
                f"<p><a href='https://github.com/apps/{app['slug']}/installations/new'>Install {app['slug']}</a></p>",
            )

        def _send(self, status, body):
            self.send_response(status)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(body.encode())

    srv = http.server.HTTPServer(("127.0.0.1", PORT), Handler)
    print(
        f"Opening http://localhost:{PORT}/ - approve the App on GitHub, then install it on {ORG}/workspace-sandbox only."
    )
    webbrowser.open(f"http://localhost:{PORT}/")
    while "slug" not in done:
        srv.handle_request()
    print(
        f"saved {STATE / (agent + '.pem')}; install: https://github.com/apps/{done['slug']}/installations/new"
    )


def enc(b: bytes) -> str:
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()


def app_jwt(agent: str) -> str:
    meta = json.loads((STATE / f"{agent}.json").read_text())
    now = int(time.time())
    signing = (
        enc(b'{"alg":"RS256","typ":"JWT"}')
        + "."
        + enc(
            json.dumps(
                {"iat": now - 60, "exp": now + 540, "iss": str(meta["app_id"])}
            ).encode()
        )
    )
    sig = subprocess.run(
        ["openssl", "dgst", "-sha256", "-sign", str(STATE / f"{agent}.pem")],
        input=signing.encode(),
        capture_output=True,
        check=True,
    ).stdout
    return signing + "." + enc(sig)


def installation(agent: str) -> None:
    req = urllib.request.Request(
        f"https://api.github.com/orgs/{ORG}/installation",
        headers={
            "Authorization": f"Bearer {app_jwt(agent)}",
            "Accept": "application/vnd.github+json",
        },
    )
    try:
        found = json.load(urllib.request.urlopen(req))
    except urllib.error.HTTPError as e:
        sys.exit(f"ws-local-{agent} is not installed in {ORG} yet ({e.code})")
    meta_path = STATE / f"{agent}.json"
    meta = json.loads(meta_path.read_text())
    meta["installation_id"] = found["id"]
    meta_path.write_text(json.dumps(meta, indent=2))
    print(found["id"])


if __name__ == "__main__":
    if len(sys.argv) == 4 and sys.argv[1] == "create":
        create(sys.argv[2], sys.argv[3])
    elif len(sys.argv) == 3 and sys.argv[1] == "installation":
        installation(sys.argv[2])
    else:
        sys.exit(__doc__)
