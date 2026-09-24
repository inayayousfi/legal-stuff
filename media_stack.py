#!/usr/bin/env python3
"""Set up and operate the local media stack without external dependencies."""

from __future__ import annotations

import argparse
import getpass
import json
import os
import re
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Callable, Mapping, Sequence


ROOT = Path(__file__).resolve().parent
ENV_FILE = ROOT / ".env"
CONFIG_TEMPLATE = ROOT / "recyclarr" / "recyclarr.yml"
HOMEPAGE_TEMPLATE_DIR = ROOT / "homepage"
CORE_SERVICES = (
    "homepage",
    "glances",
    "jellyfin",
    "qbittorrent",
    "prowlarr",
    "sonarr",
    "radarr",
)
API_KEY_PATTERN = re.compile(r"^[0-9a-fA-F]{32}$")
QBIT_TEMP_PASSWORD_PATTERN = re.compile(
    r"temporary password is provided for this session:\s*(\S+)", re.IGNORECASE
)
DOCKER_WAIT_SECONDS = 300
VPN_WAIT_SECONDS = 180
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
        "QBIT_USER",
        "QBIT_PASS",
        "SONARR_USER",
        "SONARR_PASS",
        "SONARR_API_KEY",
        "RADARR_USER",
        "RADARR_PASS",
        "RADARR_API_KEY",
        "PROWLARR_USER",
        "PROWLARR_PASS",
        "VPN_GATEWAY_SERVICE",
    }
    missing = sorted(key for key in required if not values.get(key))
    if missing:
        raise StackError(f"Missing required .env values: {', '.join(missing)}")
    for key in ("MEDIA_DIR", "CONFIG_DIR"):
        if not Path(values[key]).is_absolute():
            raise StackError(f"{key} must be an absolute path.")
    validate_api_key(values["SONARR_API_KEY"])
    validate_api_key(values["RADARR_API_KEY"])
    validate_vpn_config(values)
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


def discover_access_host() -> str:
    tailscale = shutil.which("tailscale")
    if tailscale:
        result = run(
            (tailscale, "status", "--self", "--json"),
            check=False,
            capture_output=True,
        )
        if result.returncode == 0:
            try:
                dns_name = json.loads(result.stdout)["Self"]["DNSName"].rstrip(".")
            except (json.JSONDecodeError, KeyError, TypeError, AttributeError):
                dns_name = ""
            if dns_name:
                return dns_name
    return socket.getfqdn() or socket.gethostname()


def service_url(host: str, port: int) -> str:
    formatted_host = f"[{host}]" if ":" in host else host
    return f"http://{formatted_host}:{port}"


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


def prompt_credentials() -> dict[str, str]:
    credentials: dict[str, str] = {}
    for key, service in (
        ("QBIT", "qBittorrent"),
        ("SONARR", "Sonarr"),
        ("RADARR", "Radarr"),
        ("PROWLARR", "Prowlarr"),
    ):
        credentials[f"{key}_USER"] = prompt_username(service)
        credentials[f"{key}_PASS"] = prompt_password(service)
    return credentials


def ensure_credentials(
    values: dict[str, str],
    service_keys: Sequence[str] | None = None,
) -> None:
    selected_keys = set(service_keys) if service_keys is not None else None
    for key, service in (
        ("QBIT", "qBittorrent"),
        ("SONARR", "Sonarr"),
        ("RADARR", "Radarr"),
        ("PROWLARR", "Prowlarr"),
        ("JELLYFIN", "Jellyfin"),
    ):
        if selected_keys is not None and key not in selected_keys:
            continue
        user_key = f"{key}_USER"
        password_key = f"{key}_PASS"
        if not values.get(user_key):
            values[user_key] = prompt_username(service)
        if not values.get(password_key):
            values[password_key] = prompt_password(service)


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
    ):
        (config_dir / directory).mkdir(parents=True, exist_ok=True)


def copy_recyclarr_config(config_dir: Path) -> None:
    destination = config_dir / "recyclarr" / "recyclarr.yml"
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(CONFIG_TEMPLATE, destination)


def copy_homepage_config(config_dir: Path) -> None:
    destination = config_dir / "homepage"
    destination.mkdir(parents=True, exist_ok=True)
    for filename in ("services.yaml", "settings.yaml", "widgets.yaml"):
        shutil.copyfile(HOMEPAGE_TEMPLATE_DIR / filename, destination / filename)


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


def wait_for_vpn_gateway(values: Mapping[str, str], timeout: int = VPN_WAIT_SECONDS) -> None:
    deadline = time.monotonic() + timeout
    gateway = values["VPN_GATEWAY_SERVICE"]
    while time.monotonic() < deadline:
        if gateway == "gluetun":
            result = run(
                ("docker", "inspect", "gluetun", "--format", "{{.State.Health.Status}}"),
                check=False,
                capture_output=True,
            )
            if result.returncode == 0 and result.stdout.strip() == "healthy":
                return
        else:
            result = run(
                ("docker", "exec", "tailscale-vpn", "tailscale", "status", "--json"),
                check=False,
                capture_output=True,
            )
            if result.returncode == 0:
                try:
                    status = json.loads(result.stdout)
                except json.JSONDecodeError:
                    status = {}
                if status.get("ExitNodeStatus", {}).get("Online") is True:
                    return
        time.sleep(2)
    raise StackError(f"{gateway} did not establish the selected VPN route within three minutes.")


def start_core_services(values: Mapping[str, str]) -> None:
    gateway = values["VPN_GATEWAY_SERVICE"]
    inactive_gateway = "tailscale-vpn" if gateway == "gluetun" else "gluetun"
    compose("stop", "qbittorrent", inactive_gateway)
    compose("up", "-d", gateway)
    wait_for_vpn_gateway(values)
    compose("up", "-d", *CORE_SERVICES)


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


def temporary_qbittorrent_password(logs: str) -> str:
    match = QBIT_TEMP_PASSWORD_PATTERN.search(logs)
    if not match:
        raise StackError("qBittorrent did not publish a temporary Web UI password.")
    return match.group(1)


def wait_for_qbittorrent_password(timeout: int = 60) -> str:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = compose("logs", "--no-color", "qbittorrent", capture_output=True)
        try:
            return temporary_qbittorrent_password(result.stdout + result.stderr)
        except StackError:
            time.sleep(2)
    raise StackError(
        "qBittorrent did not publish a temporary Web UI password within one minute."
    )


def print_qbittorrent_guide(
    temporary_password: str | None, access_host: str = "localhost"
) -> None:
    login = (
        "Temporary username: admin\n"
        f"Temporary password: {temporary_password}"
        if temporary_password
        else "Use the existing qBittorrent login stored in .env."
    )
    sign_in = (
        "Sign in to the Web UI with the temporary login shown above."
        if temporary_password
        else "Sign in to the Web UI with the existing login."
    )
    print(
        f"""
qBittorrent setup

Open this link:
{service_url(access_host, 8080)}

{login}

1. {sign_in}
2. Open Tools > Options > Web UI.
3. Under Authentication, replace the temporary login with the username and password requested after these steps.
4. Open the Downloads section.
5. Set Saving Management > Default Save Path to /media/Downloads.
6. Click Apply, then OK.
""".strip()
    )


def print_sonarr_guide(
    access_host: str = "localhost",
    qbit_username: str = "<qBittorrent username>",
    qbit_password: str = "<qBittorrent password>",
) -> None:
    print(
        f"""
Sonarr setup

Open this link:
{service_url(access_host, 8989)}

1. Complete first-run authentication with the username and password requested after these steps.
2. Open Settings > Media Management.
3. Under Root Folders, click Add Root Folder.
4. Select /media/Series and save it.
5. Open Settings > Download Clients.
6. Click Add, then select qBittorrent.
7. Set Host to qbittorrent and Port to 8080.
8. Username: {qbit_username}
9. Password: {qbit_password}
10. Set Category to sonarr.
11. Click Test, then Save.
12. Open Settings > General > Security.
13. Copy the API Key that Sonarr generated automatically. The next terminal prompt will ask for it.
""".strip()
    )


def print_radarr_guide(
    access_host: str = "localhost",
    qbit_username: str = "<qBittorrent username>",
    qbit_password: str = "<qBittorrent password>",
) -> None:
    print(
        f"""
Radarr setup

Open this link:
{service_url(access_host, 7878)}

1. Complete first-run authentication with the username and password requested after these steps.
2. Open Settings > Media Management.
3. Under Root Folders, click Add Root Folder.
4. Select /media/Movies and save it.
5. Open Settings > Download Clients.
6. Click Add, then select qBittorrent.
7. Set Host to qbittorrent and Port to 8080.
8. Username: {qbit_username}
9. Password: {qbit_password}
10. Set Category to radarr.
11. Click Test, then Save.
12. Open Settings > General > Security.
13. Copy the API Key that Radarr generated automatically. The next terminal prompt will ask for it.
""".strip()
    )


def print_prowlarr_guide(
    access_host: str = "localhost",
    sonarr_api_key: str = "<Sonarr API key>",
    radarr_api_key: str = "<Radarr API key>",
) -> None:
    print(
        f"""
Prowlarr setup

Open this link:
{service_url(access_host, 9696)}

1. Complete first-run authentication with the username and password requested after these steps.
2. Open Settings > Apps.
3. Click Add, then select Sonarr.
4. Set Sync Level to Full Sync.
5. Prowlarr Server: http://prowlarr:9696
6. Sonarr Server: http://sonarr:8989
7. API Key: {sonarr_api_key}
8. Click Test, then Save.
9. Click Add, then select Radarr.
10. Set Sync Level to Full Sync.
11. Prowlarr Server: http://prowlarr:9696
12. Radarr Server: http://radarr:7878
13. API Key: {radarr_api_key}
14. Click Test, then Save.
15. Open Indexers, add your indexers, then test each one.
""".strip()
    )


def print_jellyfin_guide(access_host: str = "localhost") -> None:
    print(
        f"""
Jellyfin setup

Open this link:
{service_url(access_host, 8096)}

1. Select the display language.
2. Create the Jellyfin administrator account with the username and password requested after these steps. Choose a different password from the other applications.
3. Add a Movies library using /media/Movies.
4. Add a Shows library using /media/Series.
5. Complete the remaining setup wizard pages.
6. If some media do not appear, open Dashboard > Users > your user > Parental Control. Check the maximum allowed rating and whether items with no or unrecognized rating are blocked, then save any changes.
7. Open Dashboard > Scheduled Tasks and run Scan Library to refresh the libraries.
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


def wait_for_api(name: str, url: str, api_key: str, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    request = urllib.request.Request(url, headers={"X-Api-Key": api_key})
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                if 200 <= response.status < 300:
                    return
        except (urllib.error.URLError, TimeoutError):
            pass
        time.sleep(2)
    raise StackError(f"{name} did not become ready within three minutes.")


def wait_for_apps(values: Mapping[str, str]) -> None:
    wait_for_api(
        "Sonarr",
        "http://127.0.0.1:8989/api/v3/system/status",
        values["SONARR_API_KEY"],
    )
    wait_for_api(
        "Radarr",
        "http://127.0.0.1:7878/api/v3/system/status",
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
    access_host = existing_values.get("ADMIN_ACCESS_HOST") or discover_access_host()
    values = dict(existing_values)
    values.update({
        "MEDIA_DIR": compose_path(media_dir),
        "CONFIG_DIR": compose_path(config_dir),
        "PUID": puid,
        "PGID": pgid,
        "TZ": existing_values.get("TZ", "Europe/Paris"),
        "ADMIN_ACCESS_HOST": access_host,
        "QBT_LEGAL_NOTICE": "confirm",
        "SONARR_API_KEY": existing_values.get("SONARR_API_KEY", ""),
        "RADARR_API_KEY": existing_values.get("RADARR_API_KEY", ""),
        "VPN_SERVER_COUNTRIES": existing_values.get("VPN_SERVER_COUNTRIES", ""),
    })
    ensure_vpn_config(values)
    create_directories(media_dir, config_dir)
    copy_homepage_config(config_dir)
    qbittorrent_was_configured = any(
        (config_dir / "qbittorrent").rglob("qBittorrent.conf")
    )
    state_path = config_dir / "setup-state.json"
    completed_steps = read_setup_state(state_path)
    write_env(ENV_FILE, values)

    compose("config", "--quiet")
    start_core_services(values)

    if "qbittorrent" not in completed_steps:
        if qbittorrent_was_configured:
            try:
                temporary_password = wait_for_qbittorrent_password()
            except StackError:
                temporary_password = None
        else:
            temporary_password = wait_for_qbittorrent_password()
        print_qbittorrent_guide(temporary_password, access_host)
        ensure_credentials(values, ("QBIT",))
        write_env(ENV_FILE, values)
        wait_for_step("qBittorrent")
        complete_setup_step(state_path, completed_steps, "qbittorrent")

    if "sonarr" not in completed_steps or not values["SONARR_API_KEY"]:
        print_sonarr_guide(
            access_host, values["QBIT_USER"], values["QBIT_PASS"]
        )
        ensure_credentials(values, ("SONARR",))
        write_env(ENV_FILE, values)
        wait_for_step("Sonarr")
        values["SONARR_API_KEY"] = prompt_api_key("Sonarr")
        write_env(ENV_FILE, values)
        complete_setup_step(state_path, completed_steps, "sonarr")

    if "radarr" not in completed_steps or not values["RADARR_API_KEY"]:
        print_radarr_guide(
            access_host, values["QBIT_USER"], values["QBIT_PASS"]
        )
        ensure_credentials(values, ("RADARR",))
        write_env(ENV_FILE, values)
        wait_for_step("Radarr")
        values["RADARR_API_KEY"] = prompt_api_key("Radarr")
        write_env(ENV_FILE, values)
        complete_setup_step(state_path, completed_steps, "radarr")

    if "prowlarr" not in completed_steps:
        print_prowlarr_guide(
            access_host, values["SONARR_API_KEY"], values["RADARR_API_KEY"]
        )
        ensure_credentials(values, ("PROWLARR",))
        write_env(ENV_FILE, values)
        wait_for_step("Prowlarr")
        complete_setup_step(state_path, completed_steps, "prowlarr")

    if "jellyfin" not in completed_steps:
        print_jellyfin_guide(access_host)
        ensure_credentials(values, ("JELLYFIN",))
        write_env(ENV_FILE, values)
        wait_for_step("Jellyfin")
        complete_setup_step(state_path, completed_steps, "jellyfin")
    elif not values.get("JELLYFIN_USER") or not values.get("JELLYFIN_PASS"):
        print("Record the existing Jellyfin administrator username and password in the following prompts.")
        ensure_credentials(values, ("JELLYFIN",))
        write_env(ENV_FILE, values)

    sync_recyclarr(values)
    install_autostart()
    print("\nSetup complete. Use 'python media_stack.py status' to inspect it.")
    print(f"Open Homepage:\n{service_url(access_host, 3000)}")


def start() -> None:
    values = read_env()
    wait_for_docker()
    copy_homepage_config(Path(values["CONFIG_DIR"]))
    start_core_services(values)
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
        result = run(
            ("docker", "inspect", "gluetun", "--format", "{{.State.Health.Status}}"),
            check=False,
            capture_output=True,
        )
        state = "healthy" if result.returncode == 0 and result.stdout.strip() == "healthy" else "not healthy"
        print(f"Gluetun VPN: {state}")
        if values.get("VPN_SERVER_COUNTRIES"):
            print(f"Selected countries:\n{values['VPN_SERVER_COUNTRIES']}")
    elif gateway == "tailscale-vpn":
        result = run(
            ("docker", "exec", "tailscale-vpn", "tailscale", "status", "--json"),
            check=False,
            capture_output=True,
        )
        try:
            online = result.returncode == 0 and json.loads(result.stdout).get("ExitNodeStatus", {}).get("Online") is True
        except (ValueError, AttributeError):
            online = False
        print(f"Tailscale exit node: {'online' if online else 'not online'}")
        print(f"Selected exit node:\n{values['TAILSCALE_EXIT_NODE']}")
    else:
        raise StackError("Unknown VPN gateway.")


def show_saved(section: str | None = None) -> None:
    sections = {
        "qbittorrent": (("Username", "QBIT_USER"), ("Password", "QBIT_PASS")),
        "sonarr": (("Username", "SONARR_USER"), ("Password", "SONARR_PASS"), ("API key", "SONARR_API_KEY")),
        "radarr": (("Username", "RADARR_USER"), ("Password", "RADARR_PASS"), ("API key", "RADARR_API_KEY")),
        "prowlarr": (("Username", "PROWLARR_USER"), ("Password", "PROWLARR_PASS")),
        "jellyfin": (("Username", "JELLYFIN_USER"), ("Password", "JELLYFIN_PASS")),
        "vpn": (("Provider", "VPN_SERVICE_PROVIDER"), ("Gateway", "VPN_GATEWAY_SERVICE"),
                ("Server countries", "VPN_SERVER_COUNTRIES"), ("Service username", "VPN_OPENVPN_USER"),
                ("Service password", "VPN_OPENVPN_PASSWORD"), ("Exit node", "TAILSCALE_EXIT_NODE"),
                ("Auth key", "TAILSCALE_AUTH_KEY")),
        "access": (("Host", "ADMIN_ACCESS_HOST"), ("Media directory", "MEDIA_DIR"),
                   ("Configuration directory", "CONFIG_DIR")),
    }
    if section is None:
        print("Choose what to show: " + ", ".join(sections))
        print("Passwords and API keys appear only when you choose a section.")
        return
    if not ENV_FILE.exists():
        raise StackError("Run 'python media_stack.py setup' first.")
    values = read_env_values(ENV_FILE)
    fields = sections[section]
    if section == "vpn":
        fields = (
            (("Gateway", "VPN_GATEWAY_SERVICE"), ("Exit node", "TAILSCALE_EXIT_NODE"),
             ("Auth key", "TAILSCALE_AUTH_KEY"))
            if values.get("VPN_GATEWAY_SERVICE") == "tailscale-vpn"
            else fields[:5]
        )
    shown = False
    for label, key in fields:
        if values.get(key):
            print(f"{label}:\n{values[key]}")
            shown = True
    if not shown:
        print(f"No saved values for {section}.")


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

    show_parser = commands.add_parser(
        "show",
        help="Show saved settings for one service.",
        description="Show saved settings and credentials for a selected service without opening .env.",
    )
    show_parser.add_argument(
        "section", nargs="?",
        choices=("qbittorrent", "sonarr", "radarr", "prowlarr", "jellyfin", "vpn", "access"),
        help="Choose a service, vpn, or access. Omit to list available sections.",
    )
    show_parser.set_defaults(handler=show_saved)

    return parser.parse_args(arguments)


def main(arguments: Sequence[str] | None = None) -> int:
    options = parse_arguments(arguments)
    configure_logging(getattr(options, "log_file", None))
    try:
        if options.command == "show":
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
