# Media Stack

A local Docker Compose stack for Jellyfin, qBittorrent, Prowlarr, Sonarr, Radarr, Recyclarr, and a Homepage dashboard.

Every media-aware container sees the same `/media` path. This avoids remote path mappings and allows hardlinks between downloads and libraries.

The setup supports Windows with Docker Desktop and native Linux.

## What it installs

| Service | Purpose | Address |
| --- | --- | --- |
| Homepage | Links, container status, VPN status and Docker resource overview | `http://HOSTNAME:3000` |
| Docker socket proxy | Read-only container status for Homepage | No web interface |
| Gluetun or Tailscale | Selectable qBittorrent VPN gateway | No web interface |
| Jellyfin | Media server | `http://HOSTNAME:8096` |
| Seerr | Movie and series requests sent to Radarr and Sonarr | `http://HOSTNAME:5055` |
| VPN country | Gluetun country selection, linked from Homepage's VPN card | `http://HOSTNAME:8090` |
| qBittorrent | Download client | `http://HOSTNAME:8080` |
| Prowlarr | Indexer manager | `http://HOSTNAME:9696` |
| Sonarr | Series manager | `http://HOSTNAME:8989` |
| Radarr | Movie manager | `http://HOSTNAME:7878` |
| Recyclarr | Quality profile synchronization | No web interface |

Jellyfin and the administration interfaces listen on every host network connection. On a Tailscale host, setup detects its MagicDNS name and prints the complete remote addresses. Protect every account with a strong password.

The qBittorrent traffic port `6881` remains exposed for incoming torrent connections.

qBittorrent and Prowlarr have no independent container network connection. They share the selected VPN gateway, so torrent traffic and indexer searches leave through the VPN and bypass internet-provider DNS blocking. Sonarr and Radarr continue to use `qbittorrent` and `prowlarr` as host names; both names point to the active gateway's shared network location. When the VPN is down, Prowlarr is unreachable, so Sonarr and Radarr cannot search.

Setup offers NordVPN, Proton VPN, Surfshark, Private Internet Access, a Tailscale exit node, and another Gluetun OpenVPN provider. The Gluetun choices use OpenVPN UDP and no country filter by default. Gluetun selects from all matching provider servers.

NordVPN requires its generated service username and password from:

```text
Nord Account > NordVPN > Advanced Settings > Set up NordVPN manually > Service credentials
```

Proton VPN uses its separate OpenVPN username and password. Surfshark uses credentials generated under `VPN > Manual setup > Desktop or mobile > OpenVPN > Credentials`. Private Internet Access uses the assigned service username beginning with `p` and its service password. The provider-specific guide appears before either credential prompt.

The Tailscale choice requests a one-off, non-ephemeral auth key and the exact name or Tailscale IP of an existing exit node. Setup starts every other service first, then waits with no time limit until the VPN route is ready before starting qBittorrent and Prowlarr. The same applies to Gluetun. If the Tailscale gateway later restarts, qBittorrent remains without networking, but the stack may need to be restarted to reconnect qBittorrent to the replacement network namespace.

The selected gateway and its required secrets are stored in `.env`. Gluetun's firewall provides the kill switch for its providers. The Tailscale path starts qBittorrent only after the fixed exit node reports online and fails closed if its shared gateway network disappears.

NordVPN does not provide inbound port forwarding. Downloads still work, but incoming peer connectivity and seeding can be weaker than with a provider that supports a forwarded torrent port.

Homepage provides one page with links to every web interface and the running state of each container. Jellyfin and Seerr appear first; the administration interfaces and the VPN status are in a folded Admin group. It reads that state through a socket proxy that allows only read requests about containers, so Homepage cannot start, stop or create containers. With Gluetun, a VPN panel shows the public address and country through Gluetun's control server. Setup generates the key for that panel and allows it to read only the public address. The VPN country page lists the provider's OpenVPN countries from the installed Gluetun, reconnects Gluetun to the selected country without restarting qBittorrent or Prowlarr, and saves the choice in `config/vpn-country`. `start` copies that choice into `.env` before starting Gluetun, and the page reapplies it if Gluetun restarts with another country. The page has its own Gluetun key, limited to reading the public address and reading or changing VPN settings, and it cannot read `.env`. Anyone who can open the page can change the VPN country. Its Glances widget reports CPU and memory use from Docker's Linux environment, not the complete Windows host. Its media-disk figure reports the capacity and free space of the filesystem containing the selected media directory, not only the size of files inside that directory. Network usage is omitted because accurate Docker-environment network totals require broader container permissions.

## Media layout

The setup asks for one media directory and creates this structure:

```text
Media/
|-- Downloads/
|-- Movies/
`-- Series/
```

Every container uses the following paths:

```text
/media/Downloads
/media/Movies
/media/Series
```

Application state is stored in the repository's local `config/` directory. The directory is excluded from Git.

## Requirements

Install these tools before running the setup:

- Docker Engine with Docker Compose on Linux
- Docker Desktop using Linux containers on Windows
- Python 3.10 or newer

Docker must be running before setup begins.

On Windows, Docker Desktop must have access to the selected media drive.

## Download

Download the repository ZIP:

https://github.com/inayayousfi/legal-stuff/archive/refs/heads/mommy.zip

Extract the archive to its permanent location. The automatic startup configuration uses that absolute path, so moving the directory later will break startup.

## First setup

Open a terminal inside the extracted directory.

On Windows:

```powershell
py media_stack.py setup
```

On Linux:

```bash
python3 media_stack.py setup
```

The script performs these checks and actions:

1. Verifies that Docker and Docker Compose are available.
2. Asks for the media directory.
3. Presents a `Y/n` confirmation for the qBittorrent legal notice.
4. Offers the supported VPN gateways, shows the selected credential instructions, and requests only that gateway's required values.
5. Creates the media and configuration directories.
6. Writes the initial local `.env`, starts the selected VPN gateway, the Docker socket proxy, Homepage, Glances, Jellyfin, Sonarr, Radarr, and Seerr, then starts qBittorrent and Prowlarr once the VPN route is ready.
7. Displays qBittorrent's generated `admin` password and remote Web UI address.
8. Requests each permanent administration login only when its application is ready to configure.
9. Stores the chosen local administration credentials in `.env` and saves progress after each manual step.
10. Requests the Sonarr and Radarr API keys.
11. Requests the Jellyfin API key and shows the steps that make Radarr and Sonarr refresh Jellyfin after each import.
12. Applies the Recyclarr profiles.
13. Shows the Seerr connection steps with the saved Jellyfin login and the Sonarr and Radarr API keys.
14. Installs automatic startup.

Setup reuses values already present in `.env`, including credentials and API keys. Existing application configuration under `config/` remains available.

An interrupted setup keeps `.env` and completed-step checkpoints in `config/setup-state.json`. Run the same setup command again to continue. Docker Compose operations and automatic-start installation are safe to repeat.

Leading and trailing spaces are removed from the media-directory input. Password prompts display `*` characters instead of showing the password itself or leaving the input visually blank.

## Manual application setup

The Python script prints these instructions during setup.

### qBittorrent

Open this link:

```text
http://localhost:8080
```

The script reads the generated temporary password from the container logs and displays it with the `admin` username. On a fresh setup, setup stops with an error if it cannot obtain this password instead of continuing without a usable login.

The CLI stores the chosen username and password as `QBIT_USER` and `QBIT_PASS` in `.env` as a private reference. After signing in with the temporary `admin` login:

1. Open `Tools > Options > Web UI`.
2. Under `Authentication`, replace the temporary username and password with the username and password chosen in the CLI.
3. Open the `Downloads` section.
4. Set `Saving Management > Default Save Path` to `/media/Downloads`.
5. Set `Keep incomplete torrents in` to `/media/Downloads/incomplete`.
6. Click `Save`.

### Sonarr

Open:

```text
http://localhost:8989
```

1. Complete first-run authentication with the username and password requested by the CLI.
2. Open `Settings > Media Management`.
3. Under `Root Folders`, click `Add Root Folder`.
4. Select `/media/Series` and save it.
5. Open `Settings > Download Clients`.
6. Click `Add`, then select qBittorrent.
7. Set `Host` to `qbittorrent` and `Port` to `8080`.
8. Use the username and password chosen during qBittorrent setup.
9. Set `Category` to `sonarr`.
10. Click `Test`, then `Save`.

Use `qbittorrent`, not `localhost`. Inside the Sonarr container, `localhost` means Sonarr itself.

Sonarr creates its API key automatically. Copy it for the next terminal prompt:

```text
Settings > General > Security > API Key
```

### Radarr

Open:

```text
http://localhost:7878
```

1. Complete first-run authentication with the username and password requested by the CLI.
2. Open `Settings > Media Management`.
3. Under `Root Folders`, click `Add Root Folder`.
4. Select `/media/Movies` and save it.
5. Open `Settings > Download Clients`.
6. Click `Add`, then select qBittorrent.
7. Set `Host` to `qbittorrent` and `Port` to `8080`.
8. Use the username and password chosen during qBittorrent setup.
9. Set `Category` to `radarr`.
10. Click `Test`, then `Save`.

Radarr creates its API key automatically. Copy it from:

```text
Settings > General > Security > API Key
```

### Prowlarr

Open:

```text
http://localhost:9696
```

1. Complete first-run authentication with the username and password requested by the CLI.
2. Open `Settings > Apps`.
3. Add Sonarr with `Full Sync`.
4. Set `Prowlarr Server` to `http://prowlarr:9696`.
5. Set `Sonarr Server` to `http://sonarr:8989`.
6. Use the Sonarr API key copied during Sonarr setup, then test and save.
7. Add Radarr with `Full Sync`.
8. Set `Prowlarr Server` to `http://prowlarr:9696`.
9. Set `Radarr Server` to `http://radarr:7878`.
10. Use the Radarr API key copied during Radarr setup, then test and save.
11. Open `Indexers`, add your indexers, then test each one.

### Jellyfin

Open:

```text
http://localhost:8096
```

1. Select the display language.
2. Create the Jellyfin administrator account.
3. Use a different password from the other applications.
4. Add a Movies library using `/media/Movies`.
5. Add a Shows library using `/media/Series`.
6. Complete the remaining setup wizard pages.
7. Open `Dashboard > API Keys`, click `New API Key`, set `App name` to `Radarr and Sonarr`, then click `Create`.
8. Copy the generated key; setup saves it as `JELLYFIN_API_KEY`.

### Jellyfin notifications

On Windows, Docker does not pass file-change notices from a Windows drive into containers, so Jellyfin's real-time monitoring does not see new files. Radarr and Sonarr refresh Jellyfin after each import instead:

1. In Radarr, open `Settings > Connect`, click `+`, then select `Emby / Jellyfin`.
2. Set `Name` to `Jellyfin`.
3. Check `On File Import`, `On File Upgrade`, `On Rename`, `On Movie Delete`, `On Movie File Delete`, and `On Movie File Delete For Upgrade`.
4. Set `Host` to `jellyfin`, `Port` to `8096`, and `API Key` to the Jellyfin API key.
5. Keep `Update Library` checked, then click `Test` and `Save`.
6. Repeat in Sonarr, checking `On File Import`, `On File Upgrade`, `On Import Complete`, `On Rename`, `On Series Delete`, `On Episode File Delete`, and `On Episode File Delete For Upgrade`.

Files added to the media folders by hand still appear only after Jellyfin's scheduled library scan, which runs every 12 hours by default.

### Seerr

Open:

```text
http://localhost:5055
```

1. Choose Jellyfin as the server type, with `jellyfin` as the Jellyfin URL and `8096` as the port.
2. Enter an email address of your choice and the Jellyfin administrator username and password, then click `Sign In`.
3. Click `Sync Libraries`, enable the Movies and Shows libraries, then click `Continue`.
4. Add a Radarr server marked `Default Server`: host `radarr`, port `7878`, the Radarr API key, quality profile `4K Progressive`, root folder `/media/Movies`.
5. Add a Sonarr server marked `Default Server`: host `sonarr`, port `8989`, the Sonarr API key, quality profile `4K Progressive`, root folder `/media/Series`, with `Season Folders` checked.
6. Click `Finish Setup`.

## Recyclarr profiles

Recyclarr creates one profile named `4K Progressive` in Sonarr and Radarr.

The Sonarr profile uses the TRaSH Guides WEB 2160p combined profile. It accepts a lower available quality and upgrades to 4K later.

The Radarr profile uses the TRaSH Guides UHD Bluray and WEB rules. It prefers these qualities in order:

```text
Bluray 2160p
WEB 2160p
Bluray 1080p
WEB 1080p
HDTV 1080p
Bluray 720p
WEB 720p
HDTV 720p
DVD
SDTV
```

The profile upgrades downloaded movies until Bluray 2160p is available.

Both profiles favor HDR10+ while retaining HDR10 compatibility. Dolby Vision releases without an HDR fallback are rejected.

Anime uses the same progressive profiles.

The stack expects subtitles to be embedded in the media files. It does not install Bazarr or download missing subtitles.

Recyclarr runs after setup and after every `media_stack.py start`. Running `docker compose up -d` directly skips that synchronization.

## Daily commands

Show every command with a short description:

```bash
python3 media_stack.py help
```

`-h` prints the same general help. Add `-h` after any command for its detailed help:

```bash
python3 media_stack.py setup -h
python3 media_stack.py start -h
python3 media_stack.py stop -h
python3 media_stack.py status -h
python3 media_stack.py vpn-status -h
python3 media_stack.py credentials -h
```

Start the stack and synchronize Recyclarr:

```bash
python3 media_stack.py start
```

On Windows, replace `python3` with `py`.

Show container status:

```bash
python3 media_stack.py status
```

Check the selected VPN gateway (Gluetun health or Tailscale exit-node availability):

```bash
python3 media_stack.py vpn-status
```

List saved groups, or display one group (including any saved password or API key) and change its credentials:

```bash
python3 media_stack.py credentials
python3 media_stack.py credentials jellyfin
python3 media_stack.py credentials vpn
```

After the values are shown, answer `y` to replace credentials one by one; press Enter to keep a saved value. Answer `n` or press Enter to leave everything unchanged. This updates `.env` only, so change the password in the application as well. VPN credentials and the Sonarr and Radarr API keys take effect at the next `start`. The Jellyfin values belong to the administrator account, which can create other Jellyfin users and reset their passwords in Jellyfin's Dashboard. The values appear in the terminal, so use this on a private screen.

Stop the stack:

```bash
python3 media_stack.py stop
```

## Automatic startup

### Linux user service

The setup can install:

```text
~/.config/systemd/user/media-stack.service
```

It starts with the user's systemd session.

Inspect it with:

```bash
systemctl --user status media-stack.service
```

Disable and remove it with:

```bash
systemctl --user disable --now media-stack.service
rm ~/.config/systemd/user/media-stack.service
systemctl --user daemon-reload
```

### Linux system service

The setup can instead install:

```text
/etc/systemd/system/media-stack.service
```

This option requires `sudo` and starts the stack during system boot.

Inspect it with:

```bash
sudo systemctl status media-stack.service
```

Disable and remove it with:

```bash
sudo systemctl disable --now media-stack.service
sudo rm /etc/systemd/system/media-stack.service
sudo systemctl daemon-reload
```

### Windows

The setup creates a hidden launcher named `media-stack.vbs` in the current user's Startup folder.

The launcher waits up to five minutes for Docker Desktop. Startup output is written to:

```text
config/startup.log
```

Remove the launcher from:

```text
%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup
```

## Updating

Container images are pinned by multi-platform digest. A new installation therefore uses the same image builds until `compose.yaml` is updated.

After updating the digests, pull and restart the stack:

```bash
docker compose pull
python3 media_stack.py start
```

Application data remains under `config/`.

## Backups

Back up these items:

```text
.env
config/
Media/
```

The `.env` file contains the qBittorrent, Sonarr, Radarr, and Prowlarr credentials, plus the Sonarr and Radarr API keys. These stored credentials are a local reference; the containers do not use them to configure application authentication. Do not commit or share this file.

The `recyclarr/recyclarr.yml` file contains no secrets and remains tracked by Git.

## Previous stack

This repository previously used Docker named volumes. The new setup does not migrate them.

Existing named volumes remain untouched, but the new containers do not mount them. Recover or migrate their contents manually before deleting those volumes.

An older `.env` also remains accessible in the public Git history. Credentials from the previous stack must be treated as exposed and replaced. The new setup generates fresh application configuration and collects the new Sonarr and Radarr keys.

## Security notes

qBittorrent is routed through the selected gateway. Gluetun requires `NET_ADMIN`; the Tailscale gateway requires `NET_ADMIN` and `NET_RAW`. Both require access to `/dev/net/tun`. No other application service shares that gateway network.

Jellyfin is available to the local network. Protect its administrator account with a strong password.

Sonarr, Radarr, Prowlarr, and qBittorrent are accessible through every network connected to the Docker host. Use strong, distinct passwords and restrict host firewall access if a connected network is not trusted.

Jellyfin does not officially support Docker on Windows or macOS. This does not mean it cannot work, so the setup remains worth trying. Some features, particularly hardware-accelerated transcoding, may still fail on those hosts.
