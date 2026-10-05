#!/usr/bin/env python3
"""Set up and operate the local media stack without external dependencies."""

from __future__ import annotations

import argparse
import getpass
import hashlib
import json
import os
import re
import secrets
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import xml.etree.ElementTree as ElementTree
from pathlib import Path
from typing import Callable, Mapping, Sequence


ROOT = Path(__file__).resolve().parent
ENV_FILE = ROOT / ".env"
CONFIG_TEMPLATE = ROOT / "recyclarr" / "recyclarr.yml"
HOMEPAGE_TEMPLATE_DIR = ROOT / "homepage"
CORE_SERVICES = (
    "dockerproxy",
    "homepage",
    "glances",
    "caddy",
    "tinyauth",
    "jellyfin-app",
    "qbittorrent",
    "prowlarr",
    "sonarr-app",
    "radarr-app",
    "seerr",
)
API_KEY_PATTERN = re.compile(r"^[0-9a-fA-F]{32}$")
BCRYPT_HASH_PATTERN = r"\$2[aby]\$\d\d\$[./A-Za-z0-9]{53}"
NETWORK_NAME = "media-stack"
NETWORK_SUBNET = "172.31.250.0/24"
SEERR_PORT = 5055
SIGN_IN_PORT = 9091
ACCESS_MODE_OPTIONS = (
    ("1", "tailscale", "Tailscale HTTPS certificate"),
    ("2", "domain", "Own domain with a Let's Encrypt certificate"),
    ("3", "local", "Local network name without encryption"),
)
VPN_SERVICES = ("qbittorrent", "prowlarr")
GLUETUN_ONLY_SERVICES = ("vpn-country",)
DOCKER_WAIT_SECONDS = 300
RENAMED_ENV_KEYS = {
    "JELLYFIN_USER": "JELLYFIN_ADMIN_USER",
    "JELLYFIN_PASS": "JELLYFIN_ADMIN_PASS",
}
CREDENTIAL_GROUPS = {
    "admin": (("Username", "ADMIN_USER", "text"), ("Password", "ADMIN_PASS", "password")),
    "sonarr": (("API key", "SONARR_API_KEY", "api_key"),),
    "radarr": (("API key", "RADARR_API_KEY", "api_key"),),
    "jellyfin": (("Administrator username", "JELLYFIN_ADMIN_USER", "text"),
                 ("Administrator password", "JELLYFIN_ADMIN_PASS", "password"),
                 ("API key", "JELLYFIN_API_KEY", "api_key")),
    "vpn": (("Provider", "VPN_SERVICE_PROVIDER", None), ("Gateway", "VPN_GATEWAY_SERVICE", None),
            ("Server countries", "VPN_SERVER_COUNTRIES", None),
            ("Service username", "VPN_OPENVPN_USER", "text"),
            ("Service password", "VPN_OPENVPN_PASSWORD", "password"),
            ("Exit node", "TAILSCALE_EXIT_NODE", "text"), ("Auth key", "TAILSCALE_AUTH_KEY", "password")),
    "access": (("Mode", "ACCESS_MODE", None), ("Address", "ACCESS_URL", None),
               ("Media directory", "MEDIA_DIR", None),
               ("Configuration directory", "CONFIG_DIR", None)),
}
STACK_USED_KEYS = {
    "ADMIN_USER",
    "ADMIN_PASS",
    "SONARR_API_KEY",
    "RADARR_API_KEY",
    "VPN_OPENVPN_USER",
    "VPN_OPENVPN_PASSWORD",
    "TAILSCALE_EXIT_NODE",
    "TAILSCALE_AUTH_KEY",
}
VPN_PROVIDER_OPTIONS = (
    ("1", "nordvpn", "NordVPN"),
    ("2", "protonvpn", "Proton VPN"),
    ("3", "surfshark", "Surfshark"),
    ("4", "private internet access", "Private Internet Access"),
    ("5", "tailscale", "Tailscale exit node"),
    ("6", "other", "Other Gluetun OpenVPN provider"),
)


class StackError(RuntimeError):
    pass


def run(
    command: Sequence[str],
    *,
    check: bool = True,
    capture_output: bool = False,
    input_text: str | None = None,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=ROOT,
        check=check,
        capture_output=capture_output,
        input=input_text,
        text=True,
    )


def compose(*arguments: str, **kwargs: object) -> subprocess.CompletedProcess[str]:
    return run(("docker", "compose", *arguments), **kwargs)


def wait_for_docker(
    timeout: int = DOCKER_WAIT_SECONDS,
    runner: Callable[..., subprocess.CompletedProcess[str]] = run,
) -> None:
    if shutil.which("docker") is None:
        raise StackError(
            "Docker is not installed. Follow https://docs.docker.com/get-docker/."
        )

    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = runner(
            ("docker", "info"), check=False, capture_output=True
        )
        if result.returncode == 0:
            compose_result = runner(
                ("docker", "compose", "version"),
                check=False,
                capture_output=True,
            )
            if compose_result.returncode == 0:
                return
            raise StackError(
                "Docker Compose is unavailable. Follow "
                "https://docs.docker.com/compose/install/."
            )
        time.sleep(2)

    raise StackError("Docker did not become ready within five minutes.")


def dotenv_value(value: str) -> str:
    if "\n" in value or "\r" in value:
        raise StackError("Environment values cannot contain line breaks.")
    return json.dumps(value.replace("$", "$$"), ensure_ascii=True)


def write_env(path: Path, values: Mapping[str, str]) -> None:
    content = "".join(f"{key}={dotenv_value(value)}\n" for key, value in values.items())
    path.parent.mkdir(parents=True, exist_ok=True)
    file_descriptor, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", dir=path.parent, text=True
    )
    try:
        with os.fdopen(file_descriptor, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(content)
        os.replace(temporary_name, path)
        if os.name != "nt":
            path.chmod(0o600)
    except BaseException:
        Path(temporary_name).unlink(missing_ok=True)
        raise


def read_env_values(path: Path = ENV_FILE) -> dict[str, str]:
    if not path.exists():
        return {}

    values: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, raw_value = line.split("=", 1)
        try:
            values[key] = json.loads(raw_value)
        except json.JSONDecodeError:
            values[key] = raw_value
        values[key] = str(values[key]).replace("$$", "$")
    if any(old in values for old in RENAMED_ENV_KEYS):
        values = {RENAMED_ENV_KEYS.get(key, key): value for key, value in values.items()}
        write_env(path, values)
    return values


def restore_file(path: Path, content: bytes | None) -> None:
    if content is None:
        path.unlink(missing_ok=True)
        return
    file_descriptor, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", dir=path.parent
    )
    try:
        with os.fdopen(file_descriptor, "wb") as handle:
            handle.write(content)
        os.replace(temporary_name, path)
    except BaseException:
        Path(temporary_name).unlink(missing_ok=True)
        raise


def read_env(path: Path = ENV_FILE) -> dict[str, str]:
    if not path.exists():
        raise StackError("Run 'python media_stack.py setup' first.")

    values = read_env_values(path)

    required = {
        "MEDIA_DIR",
        "CONFIG_DIR",
        "PUID",
        "PGID",
        "TZ",
        "QBT_LEGAL_NOTICE",
        "ADMIN_USER",
        "ADMIN_PASS",
        "ACCESS_MODE",
        "ADMIN_ACCESS_HOST",
        "SONARR_API_KEY",
        "RADARR_API_KEY",
        "VPN_GATEWAY_SERVICE",
    }
    missing = sorted(key for key in required if not values.get(key))
    if missing:
        raise StackError(
            f"Missing required .env values: {', '.join(missing)}. "
            "Run 'python media_stack.py setup' to add them."
        )
    for key in ("MEDIA_DIR", "CONFIG_DIR"):
        if not Path(values[key]).is_absolute():
            raise StackError(f"{key} must be an absolute path.")
    validate_api_key(values["SONARR_API_KEY"])
    validate_api_key(values["RADARR_API_KEY"])
    validate_vpn_config(values)
    validate_access_config(values)
    if values["QBT_LEGAL_NOTICE"] != "confirm":
        raise StackError("qBittorrent's legal notice is not confirmed in .env.")
    return values


def normalized_path(raw_path: str) -> Path:
    value = raw_path.strip()
    if not value:
        raise StackError("Path cannot be empty.")
    path = Path(value).expanduser()
    try:
        return path.resolve()
    except OSError as error:
        raise StackError(f"Cannot resolve path: {raw_path}") from error


def compose_path(path: Path) -> str:
    return path.as_posix()


def tailscale_dns_name() -> str:
    tailscale = shutil.which("tailscale")
    if not tailscale:
        return ""
    result = run(
        (tailscale, "status", "--self", "--json"),
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        return ""
    try:
        return json.loads(result.stdout)["Self"]["DNSName"].rstrip(".")
    except (json.JSONDecodeError, KeyError, TypeError, AttributeError):
        return ""


TAILSCALE_SERVE_CONFLICT = (
    "Tailscale Serve already uses HTTPS port 443 on this computer, so the stack cannot use it. "
    "Remove the existing Tailscale Serve configuration with this command:\n"
    "tailscale serve reset"
)


def tailscale_serve_uses_https_port() -> bool:
    tailscale = shutil.which("tailscale")
    if not tailscale:
        return False
    result = run((tailscale, "serve", "status", "--json"), check=False, capture_output=True)
    try:
        status = json.loads(result.stdout) if result.returncode == 0 and result.stdout.strip() else {}
        return "443" in (status.get("TCP") or {})
    except (ValueError, AttributeError):
        return False


def local_network_name() -> str:
    return f"{socket.gethostname().split('.')[0]}.local"


def validate_access_host(host: str) -> str:
    host = host.strip().rstrip(".").lower()
    if not re.fullmatch(r"[a-z0-9-]+(\.[a-z0-9-]+)+", host) or re.fullmatch(r"[0-9.]+", host):
        raise StackError("The address must be a name containing a dot, such as media.example.com.")
    return host


def validate_access_config(values: Mapping[str, str]) -> None:
    if values.get("ACCESS_MODE") not in {mode for _number, mode, _label in ACCESS_MODE_OPTIONS}:
        raise StackError("ACCESS_MODE must be tailscale, domain, or local.")
    validate_access_host(values.get("ADMIN_ACCESS_HOST", ""))


def apply_access_values(values: dict[str, str]) -> None:
    scheme = "http" if values["ACCESS_MODE"] == "local" else "https"
    values["ACCESS_URL"] = f"{scheme}://{values['ADMIN_ACCESS_HOST']}"
    values["TINYAUTH_SECURE_COOKIE"] = "true" if scheme == "https" else "false"


def print_access_mode_guide() -> None:
    print(
        """
Secure access

Every web page is reached through one address. Choose how that address is encrypted:

1. Tailscale HTTPS certificate
Pros: every device trusts the certificate, nothing to buy, works on Windows and Linux.
Cons: the pages are reachable only from devices signed in to your Tailscale network. Tailscale must be installed on this computer.

2. Own domain with a Let's Encrypt certificate
Pros: every device trusts the certificate, and the pages are reachable from anywhere.
Cons: you need a domain name and a router that forwards ports to this computer. The pages are reachable from the whole internet, protected by the admin sign-in.

3. Local network name without encryption
Pros: nothing to buy or configure outside this computer.
Cons: passwords and pages cross your network unencrypted. Devices that cannot resolve .local names, including some Android phones, cannot open the pages.
""".strip()
    )


def prompt_access_mode() -> str:
    while True:
        choice = input("Select an access mode [1]: ").strip() or "1"
        for number, mode, _label in ACCESS_MODE_OPTIONS:
            if choice == number:
                return mode
        print("Select a number from 1 to 3.")


def print_tailscale_https_guide() -> None:
    print(
        """
Tailscale HTTPS certificate

1. Open the Tailscale admin console DNS page.
https://login.tailscale.com/admin/dns
2. Under HTTPS Certificates, click Enable HTTPS if it is not already enabled.
""".strip()
    )


def print_domain_guide() -> None:
    print(
        f"""
Own domain

1. At your domain provider, create an A record that points your chosen name to this network's public IP address.
2. On your router, forward TCP ports 80, 443, {SEERR_PORT}, and {SIGN_IN_PORT} to this computer.
3. The following prompt requests the name, for example media.example.com.

Let's Encrypt connects to this computer through those ports to issue the certificate. Until both steps are done, the pages do not open.
""".strip()
    )


def prompt_access_host(prompt: str, default: str | None = None) -> str:
    while True:
        value = input(f"{prompt} [{default}]: " if default else f"{prompt}: ").strip()
        try:
            return validate_access_host(value or default or "")
        except StackError as error:
            print(f"Error: {error}")


def ensure_access_config(values: dict[str, str]) -> None:
    if values.get("ACCESS_MODE"):
        validate_access_config(values)
        apply_access_values(values)
        return
    print_access_mode_guide()
    while True:
        mode = prompt_access_mode()
        if mode != "tailscale":
            break
        host = tailscale_dns_name()
        if not host:
            print("Tailscale is not installed or not signed in on this computer. Choose another mode.")
        elif tailscale_serve_uses_https_port():
            print(TAILSCALE_SERVE_CONFLICT)
        else:
            print_tailscale_https_guide()
            wait_for_step("Tailscale HTTPS")
            break
    if mode == "domain":
        print_domain_guide()
        host = prompt_access_host("Domain name")
    elif mode == "local":
        host = prompt_access_host("Local network name", local_network_name())
    values["ACCESS_MODE"] = mode
    values["ADMIN_ACCESS_HOST"] = host
    apply_access_values(values)


def user_ids() -> tuple[str, str]:
    if hasattr(os, "getuid") and hasattr(os, "getgid"):
        return str(os.getuid()), str(os.getgid())
    return "1000", "1000"


def validate_api_key(value: str) -> str:
    value = value.strip()
    if not API_KEY_PATTERN.fullmatch(value):
        raise StackError("API keys must contain exactly 32 hexadecimal characters.")
    return value.lower()


def prompt_api_key(service: str) -> str:
    while True:
        try:
            return validate_api_key(input(f"Paste the {service} API key: "))
        except StackError as error:
            print(f"Error: {error}")


def confirm_qbittorrent_notice() -> None:
    while True:
        answer = input("Accept qBittorrent's legal notice? [Y/n]: ").strip().lower()
        if answer in {"", "y", "yes"}:
            return
        if answer in {"n", "no"}:
            raise StackError("qBittorrent's legal notice was not accepted.")
        print("Enter Y or N.")


def prompt_username(service: str, default: str = "admin") -> str:
    value = input(f"{service} username [{default}]: ").strip()
    return value or default


def read_masked_password(
    prompt: str,
    read_character: Callable[[], str],
    write: Callable[[str], object],
) -> str:
    characters: list[str] = []
    write(prompt)
    while True:
        character = read_character()
        if character in {"\r", "\n"}:
            write("\n")
            return "".join(characters)
        if character == "\x03":
            write("\n")
            raise KeyboardInterrupt
        if character in {"\b", "\x7f"}:
            if characters:
                characters.pop()
                write("\b \b")
            continue
        if character in {"\x00", "\xe0"}:
            read_character()
            continue
        if character.isprintable():
            characters.append(character)
            write("*")


def poll_for_character(
    is_available: Callable[[], bool], read_character: Callable[[], str]
) -> str:
    while not is_available():
        time.sleep(0.05)
    return read_character()


def restore_windows_console_input() -> None:
    if os.name != "nt" or not sys.stdin.isatty():
        return

    import ctypes
    import msvcrt

    try:
        handle = msvcrt.get_osfhandle(sys.stdin.fileno())
        mode = ctypes.c_uint()
        kernel32 = ctypes.windll.kernel32
        if kernel32.GetConsoleMode(handle, ctypes.byref(mode)):
            kernel32.SetConsoleMode(handle, mode.value | 0x0001 | 0x0002 | 0x0004)
    except (OSError, ValueError):
        pass


def masked_password(prompt: str) -> str:
    if not sys.stdin.isatty():
        return getpass.getpass(prompt)

    def write(value: str) -> None:
        sys.stdout.write(value)
        sys.stdout.flush()

    if os.name == "nt":
        import msvcrt

        return read_masked_password(
            prompt,
            lambda: poll_for_character(msvcrt.kbhit, msvcrt.getwch),
            write,
        )

    import termios
    import tty

    descriptor = sys.stdin.fileno()
    previous_settings = termios.tcgetattr(descriptor)
    try:
        tty.setraw(descriptor)
        return read_masked_password(prompt, lambda: sys.stdin.read(1), write)
    finally:
        termios.tcsetattr(descriptor, termios.TCSADRAIN, previous_settings)


def prompt_password(service: str) -> str:
    while True:
        password = masked_password(f"{service} password: ")
        if not password:
            print("Password cannot be empty.")
            continue
        confirmation = masked_password(f"Repeat {service} password: ")
        if password == confirmation:
            return password
        print("Passwords do not match.")


def print_admin_sign_in_guide() -> None:
    print(
        """
Admin sign-in

Create one username and password. They protect Homepage, qBittorrent, Sonarr, Radarr, Prowlarr, and the VPN country page, which no longer ask for their own logins. Jellyfin and Seerr keep their own logins.

The following prompts request the username and password.
""".strip()
    )


def ensure_credentials(
    values: dict[str, str],
    service_keys: Sequence[str] | None = None,
) -> None:
    selected_keys = set(service_keys) if service_keys is not None else None
    for key, service in (
        ("ADMIN", "Admin"),
        ("JELLYFIN_ADMIN", "Jellyfin administrator"),
    ):
        if selected_keys is not None and key not in selected_keys:
            continue
        user_key = f"{key}_USER"
        password_key = f"{key}_PASS"
        while not values.get(user_key):
            username = prompt_username(service)
            if ":" in username:
                print("Username cannot contain ':'.")
                continue
            values[user_key] = username
        if not values.get(password_key):
            values[password_key] = prompt_password(service)


def admin_login_fingerprint(values: Mapping[str, str]) -> str:
    login = f"{values['ADMIN_USER']}\0{values['ADMIN_PASS']}".encode()
    return hashlib.sha256(login).hexdigest()


def ensure_tinyauth_users(values: dict[str, str]) -> bool:
    fingerprint = admin_login_fingerprint(values)
    if values.get("TINYAUTH_USERS") and values.get("ADMIN_LOGIN_FINGERPRINT") == fingerprint:
        return False
    result = compose(
        "run", "--rm", "--no-deps", "-T", "tinyauth", "user", "create",
        "--username", values["ADMIN_USER"], "--password", values["ADMIN_PASS"],
        check=False,
        capture_output=True,
    )
    output = re.sub(r"\x1b\[[0-9;]*m", "", result.stdout + result.stderr)
    match = re.search(re.escape(values["ADMIN_USER"]) + f":({BCRYPT_HASH_PATTERN})", output)
    if result.returncode != 0 or not match:
        raise StackError("Could not create the admin sign-in. Check that Docker can run the tinyauth image.")
    values["TINYAUTH_USERS"] = f"{values['ADMIN_USER']}:{match.group(1)}"
    values["ADMIN_LOGIN_FINGERPRINT"] = fingerprint
    return True


def create_directories(media_dir: Path, config_dir: Path) -> None:
    for directory in ("Movies", "Series", "Downloads"):
        (media_dir / directory).mkdir(parents=True, exist_ok=True)
    for directory in (
        "jellyfin",
        "jellyfin-cache",
        "qbittorrent",
        "prowlarr",
        "sonarr",
        "radarr",
        "recyclarr",
        "recyclarr-data",
        "homepage",
        "gluetun",
        "tailscale",
        "seerr",
        "vpn-country",
        "caddy/certs",
        "caddy/data",
        "caddy/config",
        "tinyauth",
    ):
        (config_dir / directory).mkdir(parents=True, exist_ok=True)


def copy_recyclarr_config(config_dir: Path) -> None:
    destination = config_dir / "recyclarr" / "recyclarr.yml"
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(CONFIG_TEMPLATE, destination)


def ensure_gluetun_api_keys(values: dict[str, str]) -> bool:
    if values.get("VPN_GATEWAY_SERVICE") != "gluetun":
        return False
    changed = False
    for key in ("GLUETUN_API_KEY", "GLUETUN_CONTROL_KEY"):
        if not values.get(key):
            values[key] = secrets.token_urlsafe(32)
            changed = True
    return changed


def write_gluetun_auth(config_dir: Path, values: Mapping[str, str]) -> bool:
    roles = (
        ("homepage", ("GET /v1/publicip/ip",), values["GLUETUN_API_KEY"]),
        (
            "vpn-country",
            ("GET /v1/publicip/ip", "GET /v1/vpn/settings", "PUT /v1/vpn/settings"),
            values["GLUETUN_CONTROL_KEY"],
        ),
    )
    content = "\n".join(
        "[[roles]]\n"
        f"name = {json.dumps(name)}\n"
        f"routes = {json.dumps(list(routes))}\n"
        'auth = "apikey"\n'
        f"apikey = {json.dumps(api_key)}\n"
        for name, routes, api_key in roles
    )
    destination = config_dir / "gluetun" / "auth" / "config.toml"
    if destination.exists() and destination.read_text(encoding="utf-8") == content:
        return False
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(content, encoding="utf-8")
    return True


def apply_saved_vpn_country(values: dict[str, str]) -> bool:
    selection_file = Path(values["CONFIG_DIR"]) / "vpn-country" / "selection.json"
    if values.get("VPN_GATEWAY_SERVICE") != "gluetun" or not selection_file.exists():
        return False
    try:
        countries = json.loads(selection_file.read_text(encoding="utf-8"))["countries"]
    except (OSError, ValueError, KeyError, TypeError):
        return False
    selected = ",".join(country for country in countries if isinstance(country, str))
    if values.get("VPN_SERVER_COUNTRIES", "") == selected:
        return False
    values["VPN_SERVER_COUNTRIES"] = selected
    return True


def vpn_countries(servers: Sequence[Mapping[str, object]]) -> list[str]:
    return sorted({
        str(server["country"])
        for server in servers
        if server.get("vpn") == "openvpn" and server.get("country")
    })


def save_vpn_country_list(values: Mapping[str, str]) -> None:
    provider_flag = "-" + values["VPN_SERVICE_PROVIDER"].replace(" ", "-")
    result = run(
        (
            "docker", "exec", "gluetun", "sh", "-c",
            f"/gluetun-entrypoint format-servers {shlex.quote(provider_flag)} "
            "-format json -output /tmp/servers.json >/dev/null && cat /tmp/servers.json",
        ),
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        return
    try:
        countries = vpn_countries(json.loads(result.stdout))
    except (ValueError, TypeError, AttributeError):
        return
    destination = Path(values["CONFIG_DIR"]) / "vpn-country" / "countries.json"
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(json.dumps(countries), encoding="utf-8")


def copy_homepage_config(config_dir: Path, values: Mapping[str, str]) -> bool:
    destination = config_dir / "homepage"
    destination.mkdir(parents=True, exist_ok=True)
    for filename in ("settings.yaml", "widgets.yaml", "docker.yaml", "bookmarks.yaml"):
        shutil.copyfile(HOMEPAGE_TEMPLATE_DIR / filename, destination / filename)
    gateway = values["VPN_GATEWAY_SERVICE"]
    vpn_template = "vpn-gluetun.yaml" if gateway == "gluetun" else "vpn-tailscale.yaml"
    (destination / "services.yaml").write_text(
        (HOMEPAGE_TEMPLATE_DIR / "services.yaml").read_text(encoding="utf-8")
        + (HOMEPAGE_TEMPLATE_DIR / vpn_template).read_text(encoding="utf-8"),
        encoding="utf-8",
    )
    return gateway == "gluetun" and write_gluetun_auth(config_dir, values)


def write_text_if_changed(path: Path, content: str) -> bool:
    if path.exists() and path.read_text(encoding="utf-8") == content:
        return False
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    return True


def caddyfile(values: Mapping[str, str]) -> str:
    mode = values["ACCESS_MODE"]
    host = values["ADMIN_ACCESS_HOST"]
    url = values["ACCESS_URL"]
    gateway = values["VPN_GATEWAY_SERVICE"]
    tls = "\ttls /certs/cert.pem /certs/key.pem\n" if mode == "tailscale" else ""
    scheme = "http://" if mode == "local" else ""
    vpn_country = (
        "\t\tredir /vpn-country /vpn-country/\n"
        "\t\thandle_path /vpn-country/* {\n"
        "\t\t\treverse_proxy vpn-country:8090\n"
        "\t\t}\n"
        if gateway == "gluetun"
        else ""
    )
    internal = "".join(
        f"""
# Internal address {name}:{port} adds the {path} path only when it is missing.
http://:{port} {{
\t@missing not path {path} {path}/*
\trewrite @missing {path}{{uri}}
\treverse_proxy {upstream}:{port}
}}
"""
        for name, port, path, upstream in (
            ("sonarr", 8989, "/sonarr", "sonarr-app"),
            ("radarr", 7878, "/radarr", "radarr-app"),
            ("prowlarr", 9696, "/prowlarr", gateway),
            ("jellyfin", 8096, "/jellyfin", "jellyfin-app"),
        )
    )
    return f"""# Generated by media_stack.py from .env. Changes here are overwritten.

{scheme}{host} {{
{tls}\t@jellyfin path /jellyfin /jellyfin/*
\thandle @jellyfin {{
\t\treverse_proxy jellyfin-app:8096
\t}}

\t@seerr path /seerr /seerr/*
\tredir @seerr {url}:{SEERR_PORT}/

\thandle {{
\t\tforward_auth tinyauth:3000 {{
\t\t\turi /api/auth/caddy
\t\t}}

\t\t@sonarr path /sonarr /sonarr/*
\t\thandle @sonarr {{
\t\t\treverse_proxy sonarr-app:8989
\t\t}}

\t\t@radarr path /radarr /radarr/*
\t\thandle @radarr {{
\t\t\treverse_proxy radarr-app:7878
\t\t}}

\t\t@prowlarr path /prowlarr /prowlarr/*
\t\thandle @prowlarr {{
\t\t\treverse_proxy {gateway}:9696
\t\t}}

\t\tredir /qbittorrent /qbittorrent/
\t\thandle_path /qbittorrent/* {{
\t\t\treverse_proxy {gateway}:8080
\t\t}}

{vpn_country}\t\thandle {{
\t\t\treverse_proxy homepage:3000
\t\t}}
\t}}
}}

{scheme}{host}:{SEERR_PORT} {{
{tls}\treverse_proxy seerr:5055
}}

{scheme}{host}:{SIGN_IN_PORT} {{
{tls}\treverse_proxy tinyauth:3000
}}
{internal}"""


def grant_tailscale_operator() -> None:
    if not sys.platform.startswith("linux"):
        return
    user = os.environ.get("USER")
    if not user:
        raise StackError("Cannot determine the current Linux user.")
    print("Tailscale needs permission for this user to fetch certificates. sudo asks for your password.")
    run(("sudo", shutil.which("tailscale") or "tailscale", "set", f"--operator={user}"))


def renew_tailscale_certificate(values: Mapping[str, str]) -> bool:
    if values["ACCESS_MODE"] != "tailscale":
        return False
    tailscale = shutil.which("tailscale")
    if not tailscale:
        raise StackError("Tailscale is not installed, so the HTTPS certificate cannot be renewed.")
    if tailscale_serve_uses_https_port():
        raise StackError(TAILSCALE_SERVE_CONFLICT)
    certs = Path(values["CONFIG_DIR"]) / "caddy" / "certs"
    certs.mkdir(parents=True, exist_ok=True)
    files = (certs / "cert.pem", certs / "key.pem")
    before = [path.read_bytes() if path.exists() else None for path in files]
    result = run(
        (
            tailscale, "cert",
            "--cert-file", str(files[0]), "--key-file", str(files[1]),
            values["ADMIN_ACCESS_HOST"],
        ),
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        raise StackError(
            "Tailscale did not issue the HTTPS certificate: "
            + (result.stderr or result.stdout).strip()
        )
    return [path.read_bytes() for path in files] != before


def write_caddy_config(values: Mapping[str, str]) -> bool:
    path = Path(values["CONFIG_DIR"]) / "caddy" / "Caddyfile"
    return write_text_if_changed(path, caddyfile(values))


def jellyfin_network_file(config_dir: Path) -> Path:
    return config_dir / "jellyfin" / "config" / "network.xml"


def jellyfin_base_url_is_set(config_dir: Path) -> bool:
    path = jellyfin_network_file(config_dir)
    if not path.exists():
        return False
    try:
        element = ElementTree.parse(path).getroot().find("BaseUrl")
    except ElementTree.ParseError:
        return False
    return element is not None and element.text == "/jellyfin"


def write_jellyfin_base_url(config_dir: Path) -> None:
    path = jellyfin_network_file(config_dir)
    ElementTree.register_namespace("xsi", "http://www.w3.org/2001/XMLSchema-instance")
    ElementTree.register_namespace("xsd", "http://www.w3.org/2001/XMLSchema")
    if path.exists():
        tree = ElementTree.parse(path)
    else:
        tree = ElementTree.ElementTree(ElementTree.Element("NetworkConfiguration"))
    root = tree.getroot()
    element = root.find("BaseUrl")
    if element is None:
        element = ElementTree.SubElement(root, "BaseUrl")
    element.text = "/jellyfin"
    path.parent.mkdir(parents=True, exist_ok=True)
    tree.write(path, encoding="utf-8", xml_declaration=True)


def qbittorrent_config_file(config_dir: Path) -> Path:
    return config_dir / "qbittorrent" / "qBittorrent" / "config" / "qBittorrent.conf"


def write_qbittorrent_login_bypass(config_dir: Path) -> bool:
    path = qbittorrent_config_file(config_dir)
    settings = {
        "WebUI\\AuthSubnetWhitelistEnabled": "true",
        "WebUI\\AuthSubnetWhitelist": NETWORK_SUBNET,
    }
    original = path.read_text(encoding="utf-8") if path.exists() else ""
    lines = original.splitlines()
    try:
        section_start = lines.index("[Preferences]") + 1
    except ValueError:
        lines += ["[Preferences]"]
        section_start = len(lines)
    section_end = next(
        (index for index in range(section_start, len(lines)) if lines[index].startswith("[")),
        len(lines),
    )
    section = [
        line for line in lines[section_start:section_end]
        if line.split("=", 1)[0] not in settings
    ]
    section += [f"{key}={value}" for key, value in settings.items()]
    content = "\n".join(lines[:section_start] + section + lines[section_end:]) + "\n"
    if content == original:
        return False
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    return True


def recreate_network_if_needed() -> None:
    subnet = stack_network_subnet()
    if subnet is not None and subnet != NETWORK_SUBNET:
        compose("down", "--remove-orphans")


def stack_network_subnet() -> str | None:
    result = run(
        ("docker", "network", "inspect", NETWORK_NAME, "--format",
         "{{range .IPAM.Config}}{{.Subnet}} {{end}}"),
        check=False,
        capture_output=True,
    )
    return result.stdout.strip() if result.returncode == 0 else None


def prompt_vpn_provider() -> str:
    print("\nVPN provider")
    for number, _identifier, label in VPN_PROVIDER_OPTIONS:
        print(f"{number}. {label}")
    while True:
        choice = input("Select a VPN provider [1]: ").strip() or "1"
        for number, identifier, _label in VPN_PROVIDER_OPTIONS:
            if choice == number:
                return identifier
        print("Select a number from 1 to 6.")


def print_vpn_guide(provider: str) -> None:
    guides = {
        "nordvpn": (
            "NordVPN generates the OpenVPN service username and password automatically.",
            "Nord Account > NordVPN > Advanced Settings > Set up NordVPN manually > Service credentials",
            "Do not use the email address and password used to sign in to the NordVPN application.",
        ),
        "protonvpn": (
            "Proton VPN generates a separate OpenVPN username and password automatically.",
            "Proton Account > VPN > OpenVPN / IKEv2 username",
            "Do not use the email address and password used to sign in to Proton.",
        ),
        "surfshark": (
            "Surfshark generates the OpenVPN service username and password automatically.",
            "Surfshark Account > VPN > Manual setup > Desktop or mobile > OpenVPN > Credentials",
            "Do not use the email address and password used to sign in to Surfshark.",
        ),
        "private internet access": (
            "Private Internet Access assigns a service username beginning with p and a service password.",
            "The purchase email or Private Internet Access Client Control Panel",
            "Use the VPN service credentials, not unrelated device or operating-system credentials.",
        ),
    }
    credential_source, credential_location, warning = guides.get(
        provider,
        (
            "Your provider supplies the OpenVPN service username and password.",
            "The provider's manual OpenVPN setup documentation",
            "Use service credentials intended for manual OpenVPN connections.",
        ),
    )

    print(
        f"""
VPN kill-switch setup

qBittorrent will use Gluetun's VPN connection. If the VPN disconnects, Gluetun's firewall blocks qBittorrent network traffic.

{credential_source}

1. Open {credential_location}
2. Keep the generated service username and password available.
3. The following prompts will request those 2 values.

{warning}
""".strip()
    )


def print_tailscale_guide() -> None:
    print(
        """
Tailscale exit-node setup

qBittorrent will share a separate Tailscale container and use one fixed exit node. Setup starts qBittorrent only after that exit node reports online.

1. Open the Tailscale admin console Keys page.
https://login.tailscale.com/admin/settings/keys
2. Generate a one-off, non-ephemeral auth key. Enable Pre-approved if device approval is enabled.
3. Open the Machines page and identify the exact machine name or Tailscale IP of an available exit node.
https://login.tailscale.com/admin/machines
4. The following prompts will request the auth key and exit-node identifier.

If the Tailscale gateway later restarts, qBittorrent remains blocked but may need a stack restart to recover its network connection.
""".strip()
    )


def prompt_existing_password(prompt: str) -> str:
    while True:
        password = masked_password(prompt)
        if password:
            return password
        print("Password cannot be empty.")


def prompt_required(prompt: str, empty_message: str) -> str:
    while True:
        value = input(prompt).strip()
        if value:
            return value
        print(empty_message)


def validate_vpn_config(values: Mapping[str, str]) -> None:
    gateway = values.get("VPN_GATEWAY_SERVICE")
    if gateway == "gluetun":
        required = (
            "VPN_SERVICE_PROVIDER",
            "VPN_TYPE",
            "VPN_OPENVPN_USER",
            "VPN_OPENVPN_PASSWORD",
        )
        if values.get("VPN_TYPE") != "openvpn":
            raise StackError("The guided Gluetun providers currently use OpenVPN only.")
    elif gateway == "tailscale-vpn":
        required = ("TAILSCALE_AUTH_KEY", "TAILSCALE_EXIT_NODE")
    else:
        raise StackError("VPN gateway must be gluetun or tailscale-vpn.")
    missing = [key for key in required if not values.get(key)]
    if missing:
        raise StackError(f"Missing required VPN values: {', '.join(missing)}")


def ensure_vpn_config(values: dict[str, str]) -> None:
    gateway = values.get("VPN_GATEWAY_SERVICE")
    provider = values.get("VPN_SERVICE_PROVIDER", "").strip().lower()
    if not gateway and provider:
        gateway = "gluetun"
    if not gateway:
        provider = prompt_vpn_provider()
        gateway = "tailscale-vpn" if provider == "tailscale" else "gluetun"
        if provider == "other":
            provider = prompt_required(
                "Gluetun provider identifier: ",
                "Provider identifier cannot be empty.",
            ).lower()

    values["VPN_GATEWAY_SERVICE"] = gateway
    if gateway == "tailscale-vpn":
        if values.get("TAILSCALE_AUTH_KEY") and values.get("TAILSCALE_EXIT_NODE"):
            return
        print_tailscale_guide()
        if not values.get("TAILSCALE_AUTH_KEY"):
            values["TAILSCALE_AUTH_KEY"] = prompt_existing_password(
                "Tailscale auth key: "
            )
        if not values.get("TAILSCALE_EXIT_NODE"):
            values["TAILSCALE_EXIT_NODE"] = prompt_required(
                "Tailscale exit-node name or IP: ",
                "Exit-node identifier cannot be empty.",
            )
        return

    values["VPN_SERVICE_PROVIDER"] = provider or "nordvpn"
    values["VPN_TYPE"] = "openvpn"
    values.setdefault("VPN_SERVER_COUNTRIES", "")
    if values.get("VPN_OPENVPN_USER") and values.get("VPN_OPENVPN_PASSWORD"):
        return
    print_vpn_guide(values["VPN_SERVICE_PROVIDER"])
    if not values.get("VPN_OPENVPN_USER"):
        values["VPN_OPENVPN_USER"] = prompt_required(
            "VPN service username: ", "Username cannot be empty."
        )
    if not values.get("VPN_OPENVPN_PASSWORD"):
        values["VPN_OPENVPN_PASSWORD"] = prompt_existing_password(
            "VPN service password: "
        )


def vpn_gateway_ready(gateway: str) -> bool:
    if gateway == "gluetun":
        result = run(
            ("docker", "inspect", "gluetun", "--format", "{{.State.Health.Status}}"),
            check=False,
            capture_output=True,
        )
        return result.returncode == 0 and result.stdout.strip() == "healthy"
    result = run(
        ("docker", "exec", "tailscale-vpn", "tailscale", "status", "--json"),
        check=False,
        capture_output=True,
    )
    try:
        status = json.loads(result.stdout) if result.returncode == 0 else {}
        return status.get("ExitNodeStatus", {}).get("Online") is True
    except (ValueError, AttributeError):
        return False


def wait_for_vpn_gateway(values: Mapping[str, str]) -> None:
    gateway = values["VPN_GATEWAY_SERVICE"]
    if vpn_gateway_ready(gateway):
        return
    print("Waiting for the VPN connection. qBittorrent and Prowlarr will start when it connects.")
    while not vpn_gateway_ready(gateway):
        time.sleep(2)


def start_core_services(
    values: Mapping[str, str],
    restart_gateway: bool = False,
    restart_proxy: bool = False,
) -> None:
    gateway = values["VPN_GATEWAY_SERVICE"]
    config_dir = Path(values["CONFIG_DIR"])
    inactive_gateway = "tailscale-vpn" if gateway == "gluetun" else "gluetun"
    if gateway == "gluetun":
        gateway_services, inactive_services = GLUETUN_ONLY_SERVICES, (inactive_gateway,)
    else:
        gateway_services, inactive_services = (), (inactive_gateway, *GLUETUN_ONLY_SERVICES)
    other_services = [service for service in CORE_SERVICES if service not in VPN_SERVICES]
    compose("stop", *VPN_SERVICES, *inactive_services)
    write_qbittorrent_login_bypass(config_dir)
    if not jellyfin_base_url_is_set(config_dir):
        compose("stop", "jellyfin-app")
        write_jellyfin_base_url(config_dir)
    compose("up", "-d", "--no-deps", "--remove-orphans", gateway, *other_services, *gateway_services)
    if restart_gateway:
        compose("restart", gateway)
    if restart_proxy:
        compose("restart", "caddy")
    if gateway == "gluetun":
        save_vpn_country_list(values)
    wait_for_vpn_gateway(values)
    compose("up", "-d", "--no-deps", *VPN_SERVICES)


def read_setup_state(path: Path) -> set[str]:
    if not path.exists():
        return set()
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError) as error:
        raise StackError(f"Cannot read setup progress from {path}.") from error
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        raise StackError(f"Invalid setup progress in {path}.")
    return set(value)


def write_setup_state(path: Path, completed_steps: set[str]) -> None:
    content = json.dumps(sorted(completed_steps), indent=2) + "\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    file_descriptor, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", dir=path.parent, text=True
    )
    try:
        with os.fdopen(file_descriptor, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(content)
        os.replace(temporary_name, path)
    except BaseException:
        Path(temporary_name).unlink(missing_ok=True)
        raise


def complete_setup_step(path: Path, completed_steps: set[str], step: str) -> None:
    completed_steps.add(step)
    write_setup_state(path, completed_steps)


def print_qbittorrent_guide(access_url: str, admin_user: str, admin_password: str) -> None:
    print(
        f"""
qBittorrent setup

Open this link:
{access_url}/qbittorrent/

1. If the Media Stack sign-in page appears, sign in with the admin login.
2. Username: {admin_user}
3. Password: {admin_password}
4. In qBittorrent, open Tools > Options > Downloads.
5. Set Saving Management > Default Save Path to /media/Downloads
6. Set Keep incomplete torrents in to /media/Downloads/incomplete
7. Click Save.
""".strip()
    )


def print_sonarr_guide(access_url: str) -> None:
    print(
        f"""
Sonarr setup

Open this link:
{access_url}/sonarr

1. Open Settings > Media Management.
2. Under Root Folders, click Add Root Folder.
3. Select /media/Series and save it.
4. Open Settings > Download Clients.
5. Click Add, then select qBittorrent.
6. Set Host to qbittorrent and Port to 8080. Leave Username and Password empty.
7. Set Category to sonarr.
8. Click Test, then Save.
9. Open Settings > General > Security.
10. Copy the API Key that Sonarr generated automatically. The next terminal prompt will ask for it.
""".strip()
    )


def print_radarr_guide(access_url: str) -> None:
    print(
        f"""
Radarr setup

Open this link:
{access_url}/radarr

1. Open Settings > Media Management.
2. Under Root Folders, click Add Root Folder.
3. Select /media/Movies and save it.
4. Open Settings > Download Clients.
5. Click Add, then select qBittorrent.
6. Set Host to qbittorrent and Port to 8080. Leave Username and Password empty.
7. Set Category to radarr.
8. Click Test, then Save.
9. Open Settings > General > Security.
10. Copy the API Key that Radarr generated automatically. The next terminal prompt will ask for it.
""".strip()
    )


def print_prowlarr_guide(
    access_url: str,
    sonarr_api_key: str = "<Sonarr API key>",
    radarr_api_key: str = "<Radarr API key>",
) -> None:
    print(
        f"""
Prowlarr setup

Open this link:
{access_url}/prowlarr

1. Open Settings > Apps.
2. Click Add, then select Sonarr.
3. Set Sync Level to Full Sync.
4. Prowlarr Server: http://prowlarr:9696
5. Sonarr Server: http://sonarr:8989
6. API Key: {sonarr_api_key}
7. Click Test, then Save.
8. Click Add, then select Radarr.
9. Set Sync Level to Full Sync.
10. Prowlarr Server: http://prowlarr:9696
11. Radarr Server: http://radarr:7878
12. API Key: {radarr_api_key}
13. Click Test, then Save.
14. Open Indexers, add your indexers, then test each one.
""".strip()
    )


def print_jellyfin_guide(access_url: str) -> None:
    print(
        f"""
Jellyfin setup

Open this link:
{access_url}/jellyfin

1. Select the display language.
2. Create the Jellyfin administrator account with the username and password requested after these steps.
3. Add a Movies library using /media/Movies.
4. Add a Shows library using /media/Series.
5. Complete the remaining setup wizard pages.
6. If some media do not appear, open Dashboard > Users > your user > Parental Control. Check the maximum allowed rating and whether items with no or unrecognized rating are blocked, then save any changes.
7. Open Dashboard > Scheduled Tasks and run Scan Library to refresh the libraries.
8. Open Dashboard > API Keys, click New API Key, set App name to Radarr and Sonarr, then click Create.
9. Copy the API key that Jellyfin generated automatically. A terminal prompt after these steps will ask for it.
""".strip()
    )


def print_jellyfin_notifications_guide(access_url: str, api_key: str) -> None:
    print(
        f"""
Jellyfin notifications

Radarr link:
{access_url}/radarr

Sonarr link:
{access_url}/sonarr

1. In Radarr, open Settings > Connect, click +, then select Emby / Jellyfin.
2. Name: Jellyfin
3. Check On File Import, On File Upgrade, On Rename, On Movie Delete, On Movie File Delete, and On Movie File Delete For Upgrade.
4. Host: jellyfin
5. Port: 8096
6. API Key: {api_key}
7. Keep Update Library checked, then click Test and Save.
8. In Sonarr, open Settings > Connect, click +, then select Emby / Jellyfin.
9. Name: Jellyfin
10. Check On File Import, On File Upgrade, On Import Complete, On Rename, On Series Delete, On Episode File Delete, and On Episode File Delete For Upgrade.
11. Host: jellyfin
12. Port: 8096
13. API Key: {api_key}
14. Keep Update Library checked, then click Test and Save.
""".strip()
    )


def print_seerr_guide(access_url: str, values: Mapping[str, str]) -> None:
    print(
        f"""
Seerr setup

Open this link:
{access_url}:{SEERR_PORT}

1. Choose Jellyfin as the server type.
2. Jellyfin URL: jellyfin
3. Port: 8096
4. Email Address: enter an email address of your choice. Seerr uses it for notifications and its own sign-in.
5. Username: {values["JELLYFIN_ADMIN_USER"]}
6. Password: {values["JELLYFIN_ADMIN_PASS"]}
7. Click Sign In.
8. Click Sync Libraries, enable the Movies and Shows libraries, then click Continue.
9. Click Add Radarr Server and check Default Server.
10. Server Name: Radarr
11. Hostname or IP Address: radarr
12. Port: 7878
13. API Key: {values["RADARR_API_KEY"]}
14. Click Test.
15. Quality Profile: 4K Progressive
16. Root Folder: /media/Movies
17. Select a Minimum Availability, then click Add Server.
18. Click Add Sonarr Server and check Default Server.
19. Server Name: Sonarr
20. Hostname or IP Address: sonarr
21. Port: 8989
22. API Key: {values["SONARR_API_KEY"]}
23. Click Test.
24. Quality Profile: 4K Progressive
25. Root Folder: /media/Series
26. Check Season Folders, then click Add Server.
27. Click Finish Setup.
""".strip()
    )


def wait_for_step(service: str) -> None:
    input(f"\nPress Enter when {service} is configured.")


def media_stack_is_running() -> bool:
    result = run(
        (
            "docker",
            "ps",
            "--filter",
            "label=com.docker.compose.project=media-stack",
            "--quiet",
        ),
        capture_output=True,
    )
    return bool(result.stdout.strip())


def rollback_setup(previous_env: bytes | None, stop_stack: bool) -> None:
    try:
        if stop_stack:
            compose("down", check=False)
    except OSError as error:
        print(f"Warning: could not stop the partial stack: {error}", file=sys.stderr)
    finally:
        restore_file(ENV_FILE, previous_env)


def wait_for_api(service: str, name: str, url: str, api_key: str, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = compose(
            "exec", "-T", service, "curl", "-fsS", "-o", "/dev/null",
            "-H", f"X-Api-Key: {api_key}", url,
            check=False,
            capture_output=True,
        )
        if result.returncode == 0:
            return
        time.sleep(2)
    raise StackError(f"{name} did not become ready within three minutes.")


def wait_for_apps(values: Mapping[str, str]) -> None:
    wait_for_api(
        "sonarr-app",
        "Sonarr",
        "http://localhost:8989/sonarr/api/v3/system/status",
        values["SONARR_API_KEY"],
    )
    wait_for_api(
        "radarr-app",
        "Radarr",
        "http://localhost:7878/radarr/api/v3/system/status",
        values["RADARR_API_KEY"],
    )


def sync_recyclarr(values: Mapping[str, str]) -> None:
    config_dir = Path(values["CONFIG_DIR"])
    copy_recyclarr_config(config_dir)
    wait_for_apps(values)
    compose("run", "--rm", "recyclarr", "sync")


def systemd_unit(script: Path, python: Path, user: str | None = None) -> str:
    user_line = f"User={user}\n" if user else ""
    docker_dependency = (
        "Requires=docker.service\nAfter=docker.service network-online.target\n"
        if user
        else ""
    )
    target = "multi-user.target" if user else "default.target"
    script_arg = shlex.quote(str(script))
    python_arg = shlex.quote(str(python))
    root_arg = shlex.quote(str(ROOT))
    log_arg = shlex.quote(str(ROOT / "config" / "startup.log"))
    return (
        "[Unit]\n"
        "Description=Media stack\n"
        f"{docker_dependency}"
        "\n[Service]\n"
        "Type=oneshot\n"
        "RemainAfterExit=yes\n"
        f"{user_line}"
        f"WorkingDirectory={root_arg}\n"
        f"ExecStart={python_arg} {script_arg} start --log-file {log_arg}\n"
        f"ExecStop={python_arg} {script_arg} stop\n"
        "TimeoutStartSec=infinity\n"
        "\n[Install]\n"
        f"WantedBy={target}\n"
    )


def install_linux_autostart() -> None:
    print("\nLinux automatic start:")
    print("1. User service (starts with this user's systemd session)")
    print("2. System service (starts at boot and requires sudo)")
    choice = input("Choose 1 or 2: ").strip()
    if choice not in {"1", "2"}:
        raise StackError("Automatic start choice must be 1 or 2.")

    script = Path(__file__).resolve()
    python = Path(sys.executable).resolve()
    if choice == "1":
        unit_path = Path.home() / ".config/systemd/user/media-stack.service"
        unit_path.parent.mkdir(parents=True, exist_ok=True)
        unit_path.write_text(systemd_unit(script, python), encoding="utf-8")
        run(("systemctl", "--user", "daemon-reload"))
        run(("systemctl", "--user", "enable", "media-stack.service"))
        print(f"Installed {unit_path}")
        return

    user = os.environ.get("USER")
    if not user:
        raise StackError("Cannot determine the current Linux user.")
    unit = systemd_unit(script, python, user)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", delete=False) as handle:
        handle.write(unit)
        temporary_unit = Path(handle.name)
    try:
        run(("sudo", "install", "-m", "644", str(temporary_unit), "/etc/systemd/system/media-stack.service"))
        run(("sudo", "systemctl", "daemon-reload"))
        run(("sudo", "systemctl", "enable", "media-stack.service"))
    finally:
        temporary_unit.unlink(missing_ok=True)
    print("Installed /etc/systemd/system/media-stack.service")


def windows_launcher(script: Path, pythonw: Path, log_file: Path) -> str:
    command = f'"{pythonw}" "{script}" start --log-file "{log_file}"'
    escaped = command.replace('"', '""')
    return (
        'Set shell = CreateObject("WScript.Shell")\n'
        f'shell.Run "{escaped}", 0, False\n'
    )


def install_windows_autostart() -> None:
    appdata = os.environ.get("APPDATA")
    if not appdata:
        raise StackError("APPDATA is unavailable; cannot find the Startup folder.")
    startup = Path(appdata) / "Microsoft/Windows/Start Menu/Programs/Startup"
    startup.mkdir(parents=True, exist_ok=True)
    python = Path(sys.executable).resolve()
    pythonw = python.with_name("pythonw.exe")
    if not pythonw.exists():
        raise StackError("pythonw.exe is required for hidden Windows startup.")
    launcher = startup / "media-stack.vbs"
    launcher.write_text(
        windows_launcher(
            Path(__file__).resolve(), pythonw, ROOT / "config" / "startup.log"
        ),
        encoding="utf-8",
    )
    print(f"Installed {launcher}")


def install_autostart() -> None:
    if os.name == "nt":
        install_windows_autostart()
    elif sys.platform.startswith("linux"):
        install_linux_autostart()
    else:
        raise StackError("Automatic start is supported only on Windows and Linux.")


def prepare_proxy(values: Mapping[str, str]) -> bool:
    certificate_changed = renew_tailscale_certificate(values)
    config_changed = write_caddy_config(values)
    return certificate_changed or config_changed


def setup() -> None:
    restore_windows_console_input()
    wait_for_docker()
    print("This setup saves progress and can be rerun after an interruption.")
    print("It does not migrate named volumes created by the previous stack.")
    print("Existing named volumes are left untouched for manual recovery.")

    existing_values = read_env_values()
    if existing_values.get("MEDIA_DIR"):
        media_dir = normalized_path(existing_values["MEDIA_DIR"])
        print(f"Reusing media directory: {media_dir}")
    else:
        media_dir = normalized_path(input("Media directory: ").strip())

    if existing_values.get("QBT_LEGAL_NOTICE") != "confirm":
        confirm_qbittorrent_notice()

    config_dir = (ROOT / "config").resolve()
    puid, pgid = user_ids()
    values = dict(existing_values)
    values.update({
        "MEDIA_DIR": compose_path(media_dir),
        "CONFIG_DIR": compose_path(config_dir),
        "PUID": puid,
        "PGID": pgid,
        "TZ": existing_values.get("TZ", "Europe/Paris"),
        "QBT_LEGAL_NOTICE": "confirm",
        "SONARR_API_KEY": existing_values.get("SONARR_API_KEY", ""),
        "RADARR_API_KEY": existing_values.get("RADARR_API_KEY", ""),
        "VPN_SERVER_COUNTRIES": existing_values.get("VPN_SERVER_COUNTRIES", ""),
    })
    ensure_vpn_config(values)
    ensure_access_config(values)
    if not values.get("ADMIN_USER") or not values.get("ADMIN_PASS"):
        print_admin_sign_in_guide()
        ensure_credentials(values, ("ADMIN",))
    ensure_gluetun_api_keys(values)
    apply_saved_vpn_country(values)
    create_directories(media_dir, config_dir)
    gateway_access_changed = copy_homepage_config(config_dir, values)
    state_path = config_dir / "setup-state.json"
    completed_steps = read_setup_state(state_path)
    write_env(ENV_FILE, values)

    compose("config", "--quiet")
    recreate_network_if_needed()
    if ensure_tinyauth_users(values):
        write_env(ENV_FILE, values)
    if values["ACCESS_MODE"] == "tailscale" and "tailscale-operator" not in completed_steps:
        grant_tailscale_operator()
        complete_setup_step(state_path, completed_steps, "tailscale-operator")
    start_core_services(values, gateway_access_changed, prepare_proxy(values))

    access_url = values["ACCESS_URL"]
    if "qbittorrent" not in completed_steps:
        print_qbittorrent_guide(access_url, values["ADMIN_USER"], values["ADMIN_PASS"])
        wait_for_step("qBittorrent")
        complete_setup_step(state_path, completed_steps, "qbittorrent")

    if "sonarr" not in completed_steps or not values["SONARR_API_KEY"]:
        print_sonarr_guide(access_url)
        wait_for_step("Sonarr")
        values["SONARR_API_KEY"] = prompt_api_key("Sonarr")
        write_env(ENV_FILE, values)
        complete_setup_step(state_path, completed_steps, "sonarr")

    if "radarr" not in completed_steps or not values["RADARR_API_KEY"]:
        print_radarr_guide(access_url)
        wait_for_step("Radarr")
        values["RADARR_API_KEY"] = prompt_api_key("Radarr")
        write_env(ENV_FILE, values)
        complete_setup_step(state_path, completed_steps, "radarr")

    if "prowlarr" not in completed_steps:
        print_prowlarr_guide(
            access_url, values["SONARR_API_KEY"], values["RADARR_API_KEY"]
        )
        wait_for_step("Prowlarr")
        complete_setup_step(state_path, completed_steps, "prowlarr")

    if "jellyfin" not in completed_steps:
        print_jellyfin_guide(access_url)
        ensure_credentials(values, ("JELLYFIN_ADMIN",))
        write_env(ENV_FILE, values)
        wait_for_step("Jellyfin")
        values["JELLYFIN_API_KEY"] = prompt_api_key("Jellyfin")
        write_env(ENV_FILE, values)
        complete_setup_step(state_path, completed_steps, "jellyfin")
    elif not values.get("JELLYFIN_ADMIN_USER") or not values.get("JELLYFIN_ADMIN_PASS"):
        print("Record the existing Jellyfin administrator username and password in the following prompts.")
        ensure_credentials(values, ("JELLYFIN_ADMIN",))
        write_env(ENV_FILE, values)

    if not values.get("JELLYFIN_API_KEY"):
        print("In Jellyfin, open Dashboard > API Keys, click New API Key, set App name to Radarr and Sonarr, then click Create. The following prompt requests the generated key.")
        values["JELLYFIN_API_KEY"] = prompt_api_key("Jellyfin")
        write_env(ENV_FILE, values)

    if "jellyfin-notifications" not in completed_steps:
        print_jellyfin_notifications_guide(access_url, values["JELLYFIN_API_KEY"])
        wait_for_step("Jellyfin notification")
        complete_setup_step(state_path, completed_steps, "jellyfin-notifications")

    sync_recyclarr(values)

    if "seerr" not in completed_steps:
        print_seerr_guide(access_url, values)
        wait_for_step("Seerr")
        complete_setup_step(state_path, completed_steps, "seerr")

    install_autostart()
    print("\nSetup complete. Use 'python media_stack.py status' to inspect it.")
    print(f"Open Homepage:\n{access_url}")


def start() -> None:
    values = read_env()
    apply_access_values(values)
    ensure_gluetun_api_keys(values)
    apply_saved_vpn_country(values)
    write_env(ENV_FILE, values)
    wait_for_docker()
    recreate_network_if_needed()
    if ensure_tinyauth_users(values):
        write_env(ENV_FILE, values)
    config_dir = Path(values["CONFIG_DIR"])
    create_directories(Path(values["MEDIA_DIR"]), config_dir)
    gateway_access_changed = copy_homepage_config(config_dir, values)
    start_core_services(values, gateway_access_changed, prepare_proxy(values))
    sync_recyclarr(values)


def stop() -> None:
    wait_for_docker()
    compose("down")


def status() -> None:
    wait_for_docker()
    compose("ps")


def vpn_status() -> None:
    values = read_env()
    wait_for_docker()
    gateway = values["VPN_GATEWAY_SERVICE"]
    if gateway == "gluetun":
        state = "healthy" if vpn_gateway_ready(gateway) else "not healthy"
        print(f"Gluetun VPN: {state}")
        if values.get("VPN_SERVER_COUNTRIES"):
            print(f"Selected countries:\n{values['VPN_SERVER_COUNTRIES']}")
    elif gateway == "tailscale-vpn":
        online = vpn_gateway_ready(gateway)
        print(f"Tailscale exit node: {'online' if online else 'not online'}")
        print(f"Selected exit node:\n{values['TAILSCALE_EXIT_NODE']}")
    else:
        raise StackError("Unknown VPN gateway.")


def prompt_new_value(label: str, kind: str) -> str | None:
    if kind == "password":
        while True:
            value = masked_password(f"New {label.lower()} [Enter keeps it]: ")
            if not value:
                return None
            if masked_password(f"Repeat new {label.lower()}: ") == value:
                return value
            print("Values do not match.")
    while True:
        value = input(f"New {label.lower()} [Enter keeps it]: ").strip()
        if not value:
            return None
        if kind != "api_key":
            return value
        try:
            return validate_api_key(value)
        except StackError as error:
            print(f"Error: {error}")


def confirm_change() -> bool:
    while True:
        answer = input("Change these values? [y/N]: ").strip().lower()
        if answer in {"", "n", "no"}:
            return False
        if answer in {"y", "yes"}:
            return True
        print("Enter Y or N.")


def credentials(section: str | None = None) -> None:
    if section is None:
        print("Choose a group: " + ", ".join(CREDENTIAL_GROUPS))
        print("Passwords and API keys appear only when you choose a group.")
        return
    if not ENV_FILE.exists():
        raise StackError("Run 'python media_stack.py setup' first.")
    values = read_env_values(ENV_FILE)
    fields = CREDENTIAL_GROUPS[section]
    if section == "vpn":
        tailscale_keys = {"TAILSCALE_EXIT_NODE", "TAILSCALE_AUTH_KEY"}
        uses_tailscale = values.get("VPN_GATEWAY_SERVICE") == "tailscale-vpn"
        fields = tuple(
            field for field in fields
            if field[1] == "VPN_GATEWAY_SERVICE"
            or (field[1] in tailscale_keys) == uses_tailscale
        )
    if not any(values.get(key) for _label, key, _kind in fields):
        print(f"No saved values for {section}.")
        return
    for label, key, _kind in fields:
        if values.get(key):
            print(f"{label}: {values[key]}")
    editable = any(kind is not None and values.get(key) for _label, key, kind in fields)
    if not editable or not confirm_change():
        return
    changed = set()
    for label, key, kind in fields:
        if kind is None or not values.get(key):
            continue
        new_value = prompt_new_value(label, kind)
        if new_value is not None and new_value != values[key]:
            values[key] = new_value
            changed.add(key)
    if not changed:
        print("No changes.")
        return
    write_env(ENV_FILE, values)
    print("Saved.")
    if changed & STACK_USED_KEYS:
        print("The stack uses the changed values after its next start.")


def configure_logging(log_file: str | None) -> None:
    if not log_file:
        return
    path = Path(log_file)
    path.parent.mkdir(parents=True, exist_ok=True)
    handle = path.open("a", encoding="utf-8", buffering=1)
    sys.stdout = handle
    sys.stderr = handle
    print(f"\n[{time.strftime('%Y-%m-%d %H:%M:%S')}] media-stack start")


def parse_arguments(arguments: Sequence[str] | None = None) -> argparse.Namespace:
    arguments = list(arguments) if arguments is not None else sys.argv[1:]
    if not arguments or arguments == ["help"]:
        arguments = ["-h"]

    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", metavar="command", required=True)

    setup_parser = commands.add_parser(
        "setup",
        help="Configure credentials, services, Recyclarr, and automatic startup.",
        description="Configure credentials, start the services, apply Recyclarr profiles, and install automatic startup.",
    )
    setup_parser.set_defaults(handler=setup)

    start_parser = commands.add_parser(
        "start",
        help="Start the services and synchronize Recyclarr.",
        description="Start the media services, wait for Sonarr and Radarr, then synchronize Recyclarr.",
    )
    start_parser.add_argument("--log-file", help=argparse.SUPPRESS)
    start_parser.set_defaults(handler=start)

    stop_parser = commands.add_parser(
        "stop",
        help="Stop and remove the stack containers.",
        description="Stop and remove the media stack containers and network while preserving configuration and media files.",
    )
    stop_parser.set_defaults(handler=stop)

    status_parser = commands.add_parser(
        "status",
        help="Show the current service status.",
        description="Show the current Docker Compose status for every media stack service.",
    )
    status_parser.set_defaults(handler=status)

    vpn_status_parser = commands.add_parser(
        "vpn-status",
        help="Show the selected VPN gateway and its connection status.",
        description="Check the selected VPN gateway health or Tailscale exit-node availability.",
    )
    vpn_status_parser.set_defaults(handler=vpn_status)

    credentials_parser = commands.add_parser(
        "credentials",
        help="List or change the saved values for one service.",
        description=(
            "Show the saved values for a service, vpn, or access. Answer y to type a new "
            "value for any credential, pressing Enter to keep a value. Changes are saved to "
            ".env only; change the password in the application itself as well."
        ),
    )
    credentials_parser.add_argument(
        "section", nargs="?", choices=tuple(CREDENTIAL_GROUPS),
        help="Choose a service, vpn, or access. Omit to list available groups.",
    )
    credentials_parser.set_defaults(handler=credentials)

    return parser.parse_args(arguments)


def main(arguments: Sequence[str] | None = None) -> int:
    options = parse_arguments(arguments)
    configure_logging(getattr(options, "log_file", None))
    try:
        if options.command == "credentials":
            options.handler(options.section)
        else:
            options.handler()
    except KeyboardInterrupt:
        print("\nCommand cancelled.", file=sys.stderr)
        return 130
    except (StackError, subprocess.CalledProcessError, OSError) as error:
        print(f"Error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
