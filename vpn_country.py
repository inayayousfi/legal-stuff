#!/usr/bin/env python3
"""Serve a page that selects the Gluetun VPN country and keeps that choice applied."""

from __future__ import annotations

import html
import json
import os
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


DATA_DIR = Path(os.environ.get("DATA_DIR", "/data"))
COUNTRIES_FILE = DATA_DIR / "countries.json"
SELECTION_FILE = DATA_DIR / "selection.json"
GLUETUN_URL = os.environ.get("GLUETUN_URL", "http://gluetun:8000")
GLUETUN_KEY = os.environ.get("GLUETUN_CONTROL_KEY", "")
PORT = 8090
REAPPLY_SECONDS = 30


def read_json(path: Path, default):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return default


def available_countries() -> list[str]:
    countries = read_json(COUNTRIES_FILE, [])
    return [country for country in countries if isinstance(country, str)]


def saved_countries() -> list[str]:
    selection = read_json(SELECTION_FILE, {})
    countries = selection.get("countries", []) if isinstance(selection, dict) else []
    return [country for country in countries if isinstance(country, str)]


def save_countries(countries: list[str]) -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    file_descriptor, temporary_name = tempfile.mkstemp(dir=DATA_DIR, prefix=".selection.")
    try:
        with os.fdopen(file_descriptor, "w", encoding="utf-8") as handle:
            json.dump({"countries": countries}, handle)
        os.replace(temporary_name, SELECTION_FILE)
    except BaseException:
        Path(temporary_name).unlink(missing_ok=True)
        raise


def gluetun(method: str, path: str, body: dict | None = None) -> dict:
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(
        GLUETUN_URL + path,
        data=data,
        method=method,
        headers={"X-API-Key": GLUETUN_KEY, "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        content = response.read().decode()
    try:
        return json.loads(content)
    except ValueError:
        return {"outcome": content.strip()}


def selection_body(countries: list[str]) -> dict:
    return {"provider": {"server_selection": {"countries": countries}}}


def current_countries() -> list[str]:
    settings = gluetun("GET", "/v1/vpn/settings")
    return settings.get("provider", {}).get("server_selection", {}).get("countries") or []


def apply_countries(countries: list[str]) -> None:
    gluetun("PUT", "/v1/vpn/settings", selection_body(countries))


def reapply_saved_selection() -> None:
    if not SELECTION_FILE.exists():
        return
    countries = saved_countries()
    if current_countries() != countries:
        apply_countries(countries)


def reapply_loop() -> None:
    while True:
        try:
            reapply_saved_selection()
        except (OSError, urllib.error.URLError, ValueError):
            pass
        time.sleep(REAPPLY_SECONDS)


def requested_countries(form_value: str, choices: list[str]) -> list[str]:
    if form_value == "":
        return []
    if form_value not in choices:
        raise ValueError(f"Unknown country: {form_value}")
    return [form_value]


def render_page(message: str = "") -> str:
    choices = available_countries()
    selected = saved_countries()
    try:
        address = gluetun("GET", "/v1/publicip/ip")
        if address.get("public_ip"):
            location = f"{address['public_ip']} ({address.get('country', '?')})"
        else:
            location = "Reconnecting"
    except (OSError, urllib.error.URLError, ValueError):
        location = "Unavailable"
    options = ['<option value="">Any country</option>'] + [
        f'<option value="{html.escape(country)}"{" selected" if [country] == selected else ""}>'
        f"{html.escape(country)}</option>"
        for country in choices
    ]
    notice = f'<p class="message">{html.escape(message)}</p>' if message else ""
    unavailable = "" if choices else '<p class="message">The country list is unavailable until the stack starts.</p>'
    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>VPN Country</title>
<style>
:root {{ color-scheme: dark; --bg: #1e293b; --panel: #334155; --text: #e2e8f0; --muted: #94a3b8; --accent: #38bdf8; }}
body {{ margin: 0; background: var(--bg); color: var(--text); font: 16px system-ui, sans-serif; }}
main {{ max-width: 28rem; margin: 3rem auto; padding: 0 16px; }}
section {{ background: var(--panel); border-radius: 8px; padding: 1.25rem; }}
p {{ margin: 0 0 1rem; }}
.muted {{ color: var(--muted); }}
.message {{ color: var(--accent); }}
select, button {{ width: 100%; padding: .6rem; border-radius: 6px; font: inherit; margin-top: .5rem; }}
button {{ background: var(--accent); color: #0f172a; border: 0; font-weight: 600; cursor: pointer; }}
</style>
</head>
<body>
<main>
<h1>VPN Country</h1>
<section>
<p><span class="muted">Current address</span><br>{html.escape(location)}</p>
{notice}{unavailable}
<form method="post">
<label for="country" class="muted">Connect through</label>
<select id="country" name="country">{"".join(options)}</select>
<button type="submit">Connect</button>
</form>
</section>
</main>
</body>
</html>
"""


class Handler(BaseHTTPRequestHandler):
    def send_page(self, status: int, message: str = "") -> None:
        content = render_page(message).encode()
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(content)))
        self.end_headers()
        self.wfile.write(content)

    def do_GET(self) -> None:
        if self.path != "/":
            self.send_error(404)
            return
        self.send_page(200)

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        form = urllib.parse.parse_qs(self.rfile.read(length).decode())
        try:
            countries = requested_countries(form.get("country", [""])[0], available_countries())
            apply_countries(countries)
            save_countries(countries)
        except ValueError as error:
            self.send_page(400, str(error))
            return
        except (OSError, urllib.error.URLError) as error:
            self.send_page(502, f"Gluetun did not accept the change: {error}")
            return
        target = countries[0] if countries else "any country"
        self.send_page(200, f"Reconnecting through {target}. The address updates within a minute.")


def main() -> None:
    threading.Thread(target=reapply_loop, daemon=True).start()
    ThreadingHTTPServer(("", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
