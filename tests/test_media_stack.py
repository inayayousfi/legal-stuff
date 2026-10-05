import contextlib
import io
import tempfile
import unittest
from pathlib import Path
from unittest.mock import DEFAULT, Mock, patch

import media_stack


class EnvironmentTests(unittest.TestCase):
    def test_write_and_read_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            values = {
                "MEDIA_DIR": "/media path/$disk",
                "CONFIG_DIR": "/config",
                "PUID": "1000",
                "PGID": "1000",
                "TZ": "Europe/Paris",
                "QBT_LEGAL_NOTICE": "confirm",
                "ADMIN_USER": "admin",
                "ADMIN_PASS": "admin-secret$",
                "ACCESS_MODE": "tailscale",
                "ADMIN_ACCESS_HOST": "media.example.ts.net",
                "JELLYFIN_ADMIN_USER": "jellyfin-admin",
                "JELLYFIN_ADMIN_PASS": "jellyfin-secret",
                "SONARR_API_KEY": "0" * 32,
                "RADARR_API_KEY": "1" * 32,
                "VPN_SERVICE_PROVIDER": "nordvpn",
                "VPN_GATEWAY_SERVICE": "gluetun",
                "VPN_TYPE": "openvpn",
                "VPN_OPENVPN_USER": "vpn-user",
                "VPN_OPENVPN_PASSWORD": "vpn-secret",
            }
            media_stack.write_env(path, values)
            self.assertEqual(media_stack.read_env(path), values)

    def test_environment_write_is_private_on_unix(self):
        if media_stack.os.name == "nt":
            self.skipTest("Unix permissions are unavailable on Windows")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            media_stack.write_env(path, {"KEY": "value"})
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_restore_removes_new_file(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            path.write_text("partial", encoding="utf-8")
            media_stack.restore_file(path, None)
            self.assertFalse(path.exists())

    def test_restore_replaces_existing_file(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            path.write_text("partial", encoding="utf-8")
            media_stack.restore_file(path, b"original")
            self.assertEqual(path.read_bytes(), b"original")

    def test_missing_required_value_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            media_stack.write_env(path, {"MEDIA_DIR": "/media"})
            with self.assertRaisesRegex(media_stack.StackError, "Missing required"):
                media_stack.read_env(path)

    def test_partial_environment_can_be_loaded_for_setup_resume(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            media_stack.write_env(path, {"MEDIA_DIR": "/media", "ADMIN_USER": "admin"})
            self.assertEqual(
                media_stack.read_env_values(path),
                {"MEDIA_DIR": "/media", "ADMIN_USER": "admin"},
            )

    def test_missing_resume_environment_is_empty(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            self.assertEqual(media_stack.read_env_values(path), {})


class SetupStateTests(unittest.TestCase):
    def test_setup_progress_round_trip(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config" / "setup-state.json"
            media_stack.write_setup_state(path, {"sonarr", "qbittorrent"})
            self.assertEqual(
                media_stack.read_setup_state(path), {"sonarr", "qbittorrent"}
            )

    def test_completing_step_preserves_previous_progress(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "setup-state.json"
            completed = {"qbittorrent"}
            media_stack.complete_setup_step(path, completed, "sonarr")
            self.assertEqual(completed, {"qbittorrent", "sonarr"})
            self.assertEqual(
                media_stack.read_setup_state(path), {"qbittorrent", "sonarr"}
            )

    def test_invalid_setup_progress_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "setup-state.json"
            path.write_text('{"sonarr": true}', encoding="utf-8")
            with self.assertRaisesRegex(media_stack.StackError, "Invalid setup progress"):
                media_stack.read_setup_state(path)


class HomepageTests(unittest.TestCase):
    def test_homepage_config_is_copied(self):
        with tempfile.TemporaryDirectory() as directory:
            config_dir = Path(directory)
            media_stack.copy_homepage_config(
                config_dir,
                {
                    "VPN_GATEWAY_SERVICE": "gluetun",
                    "GLUETUN_API_KEY": "panel-key",
                    "GLUETUN_CONTROL_KEY": "control-key",
                },
            )
            homepage_dir = config_dir / "homepage"
            for filename in ("settings.yaml", "widgets.yaml", "docker.yaml", "bookmarks.yaml"):
                self.assertEqual(
                    (homepage_dir / filename).read_bytes(),
                    (media_stack.HOMEPAGE_TEMPLATE_DIR / filename).read_bytes(),
                )
            services = (homepage_dir / "services.yaml").read_text()
            media, admin = services.split("\n- Admin:\n")
            self.assertIn("container: seerr", media)
            self.assertNotIn("container: qbittorrent", media)
            for container in ("qbittorrent", "sonarr", "radarr", "prowlarr", "gluetun"):
                self.assertIn(f"container: {container}", admin)
            settings = (homepage_dir / "settings.yaml").read_text()
            self.assertIn("  Admin:\n    style: row\n    columns: 3\n    initiallyCollapsed: true\n", settings)
            self.assertIn("type: gluetun", services)
            self.assertIn('key: "{{HOMEPAGE_VAR_GLUETUN_API_KEY}}"', services)
            self.assertIn('href: "{{HOMEPAGE_VAR_URL}}/vpn-country/"', admin)
            auth = (config_dir / "gluetun" / "auth" / "config.toml").read_text()
            homepage_role, country_role = auth.split('name = "vpn-country"')
            self.assertIn('routes = ["GET /v1/publicip/ip"]', homepage_role)
            self.assertIn('apikey = "panel-key"', homepage_role)
            self.assertIn(
                'routes = ["GET /v1/publicip/ip", "GET /v1/vpn/settings", "PUT /v1/vpn/settings"]',
                country_role,
            )
            self.assertIn('apikey = "control-key"', country_role)

    def test_tailscale_homepage_has_status_without_gluetun_panel(self):
        with tempfile.TemporaryDirectory() as directory:
            config_dir = Path(directory)
            media_stack.copy_homepage_config(
                config_dir, {"VPN_GATEWAY_SERVICE": "tailscale-vpn"}
            )
            services = (config_dir / "homepage" / "services.yaml").read_text()
            self.assertIn("container: tailscale-vpn", services)
            self.assertNotIn("type: gluetun", services)
            self.assertFalse((config_dir / "gluetun").exists())

    def test_dashboard_links_use_the_access_address_and_service_paths(self):
        services = (media_stack.HOMEPAGE_TEMPLATE_DIR / "services.yaml").read_text(
            encoding="utf-8"
        )
        self.assertEqual(services.count('href: "{{HOMEPAGE_VAR_URL}}/'), 6)
        for path in ("/jellyfin", "/seerr", "/qbittorrent/", "/sonarr", "/radarr", "/prowlarr"):
            # Quoted: a YAML value starting with "{" would be read as a mapping.
            self.assertIn(f'href: "{{{{HOMEPAGE_VAR_URL}}}}{path}"\n', services)

    def test_dashboard_metrics_exclude_network(self):
        widgets = (media_stack.HOMEPAGE_TEMPLATE_DIR / "widgets.yaml").read_text(
            encoding="utf-8"
        )
        self.assertIn("cpu: true", widgets)
        self.assertIn("mem: true", widgets)
        self.assertIn("disk: /media", widgets)
        self.assertNotIn("network", widgets)


class ValidationTests(unittest.TestCase):
    def test_valid_api_key_is_normalized(self):
        key = "ABCDEF0123456789ABCDEF0123456789"
        self.assertEqual(media_stack.validate_api_key(key), key.lower())

    def test_invalid_api_key_is_rejected(self):
        with self.assertRaises(media_stack.StackError):
            media_stack.validate_api_key("not-a-key")

    def test_line_breaks_are_rejected(self):
        with self.assertRaises(media_stack.StackError):
            media_stack.dotenv_value("first\nsecond")

    def test_path_input_is_trimmed(self):
        self.assertEqual(media_stack.normalized_path("  /media/library  "), Path("/media/library"))

    def test_empty_path_is_rejected(self):
        with self.assertRaisesRegex(media_stack.StackError, "cannot be empty"):
            media_stack.normalized_path("   ")

    @patch("builtins.input", return_value="")
    def test_qbittorrent_notice_defaults_to_yes(self, _input):
        media_stack.confirm_qbittorrent_notice()

    @patch("builtins.input", return_value="n")
    def test_qbittorrent_notice_can_be_rejected(self, _input):
        with self.assertRaisesRegex(media_stack.StackError, "not accepted"):
            media_stack.confirm_qbittorrent_notice()

    @patch("builtins.input", return_value="")
    def test_username_defaults_to_admin(self, _input):
        self.assertEqual(media_stack.prompt_username("Sonarr"), "admin")

    @patch("media_stack.masked_password", side_effect=("secret", "secret"))
    def test_password_requires_confirmation(self, _masked_password):
        self.assertEqual(media_stack.prompt_password("Sonarr"), "secret")

    def test_masked_password_shows_stars_and_handles_backspace(self):
        characters = iter("secx\bret\r")
        output = io.StringIO()
        password = media_stack.read_masked_password(
            "Password: ", lambda: next(characters), output.write
        )
        self.assertEqual(password, "secret")
        self.assertEqual(output.getvalue(), "Password: ****\b \b***\n")

    def test_masked_password_handles_control_c(self):
        characters = iter("sec\x03")
        output = io.StringIO()
        with self.assertRaises(KeyboardInterrupt):
            media_stack.read_masked_password(
                "Password: ", lambda: next(characters), output.write
            )
        self.assertTrue(output.getvalue().endswith("\n"))

    @patch("media_stack.time.sleep")
    def test_character_polling_yields_to_control_c(self, sleep):
        checks = iter((False, False, True))
        self.assertEqual(
            media_stack.poll_for_character(lambda: next(checks), lambda: "x"),
            "x",
        )
        self.assertEqual(sleep.call_count, 2)

    def test_console_restore_is_a_noop_outside_windows(self):
        if media_stack.os.name == "nt":
            self.skipTest("This assertion covers non-Windows hosts")
        media_stack.restore_windows_console_input()

    def test_api_key_prompt_only_requests_the_value(self):
        output = io.StringIO()
        with patch("builtins.input", return_value="a" * 32):
            with contextlib.redirect_stdout(output):
                media_stack.prompt_api_key("Sonarr")
        self.assertEqual(output.getvalue(), "")


class SetupGuideTests(unittest.TestCase):
    def guide_output(self, printer, *arguments):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            printer(*arguments)
        return output.getvalue()

    def test_seerr_guide_prints_saved_login_keys_and_internal_hosts(self):
        values = {
            "JELLYFIN_ADMIN_USER": "jelly",
            "JELLYFIN_ADMIN_PASS": "jelly-secret",
            "RADARR_API_KEY": "b" * 32,
            "SONARR_API_KEY": "a" * 32,
        }
        output = self.guide_output(
            media_stack.print_seerr_guide, "https://media.example.ts.net", values
        )
        self.assertIn("https://media.example.ts.net:5055\n", output)
        for line in (
            "Jellyfin URL: jellyfin\n",
            "Username: jelly\n",
            "Password: jelly-secret\n",
            "Hostname or IP Address: radarr\n",
            f"API Key: {'b' * 32}\n",
            "Root Folder: /media/Movies\n",
            "Hostname or IP Address: sonarr\n",
            f"API Key: {'a' * 32}\n",
            "Root Folder: /media/Series\n",
            "Quality Profile: 4K Progressive\n",
        ):
            self.assertIn(line, output)
        self.assertNotIn("JELLYFIN_", output)
        self.assertNotIn("_API_KEY", output)

    def test_jellyfin_notifications_guide_prints_key_hosts_and_triggers(self):
        output = self.guide_output(
            media_stack.print_jellyfin_notifications_guide, "https://media.example.ts.net", "c" * 32
        )
        self.assertIn("https://media.example.ts.net/radarr\n", output)
        self.assertIn("https://media.example.ts.net/sonarr\n", output)
        self.assertEqual(output.count(f"API Key: {'c' * 32}\n"), 2)
        self.assertEqual(output.count("Host: jellyfin\n"), 2)
        self.assertEqual(output.count("select Emby / Jellyfin."), 2)
        self.assertIn("On Movie File Delete For Upgrade", output)
        self.assertIn("On Import Complete", output)
        self.assertNotIn("JELLYFIN_API_KEY", output)

    def test_jellyfin_guide_asks_for_generated_api_key(self):
        output = self.guide_output(media_stack.print_jellyfin_guide, "https://media.example.ts.net")
        self.assertIn("click New API Key, set App name to Radarr and Sonarr, then click Create.", output)
        self.assertIn("Jellyfin generated automatically. A terminal prompt after these steps will ask for it.", output)

    def test_qbittorrent_guide_prints_admin_login_link_and_download_paths(self):
        output = self.guide_output(
            media_stack.print_qbittorrent_guide, "https://media.example.ts.net", "boss", "admin-secret"
        )
        self.assertEqual(output.count("https://media.example.ts.net/qbittorrent/\n"), 1)
        self.assertIn("Username: boss\n", output)
        self.assertIn("Password: admin-secret\n", output)
        self.assertIn("Tools > Options > Downloads", output)
        self.assertIn("Default Save Path to /media/Downloads\n", output)
        self.assertIn("Keep incomplete torrents in to /media/Downloads/incomplete\n", output)
        self.assertNotIn("Temporary", output)
        self.assertNotIn("ADMIN_", output)

    def test_service_guides_contain_paths_and_media_folders(self):
        guides = (
            (media_stack.print_sonarr_guide, "https://media.example.ts.net/sonarr\n", "/media/Series"),
            (media_stack.print_radarr_guide, "https://media.example.ts.net/radarr\n", "/media/Movies"),
            (media_stack.print_prowlarr_guide, "https://media.example.ts.net/prowlarr\n", "http://sonarr:8989"),
            (media_stack.print_jellyfin_guide, "https://media.example.ts.net/jellyfin\n", "/media/Movies"),
        )
        for printer, url, path in guides:
            with self.subTest(url=url):
                output = self.guide_output(printer, "https://media.example.ts.net")
                self.assertEqual(output.count(url), 1)
                self.assertIn(path, output)
                self.assertNotIn("first-run authentication", output)

    def test_jellyfin_guide_explains_login_and_missing_media(self):
        output = self.guide_output(media_stack.print_jellyfin_guide, "https://media.example.ts.net")
        self.assertIn("administrator account", output)
        self.assertIn("username and password requested after these steps", output)
        self.assertIn("Dashboard > Users > your user > Parental Control", output)
        self.assertIn("items with no or unrecognized rating", output)
        self.assertIn("Dashboard > Scheduled Tasks", output)
        self.assertIn("Scan Library", output)

    def test_service_guides_do_not_expose_environment_names(self):
        printers = (
            media_stack.print_sonarr_guide,
            media_stack.print_radarr_guide,
            media_stack.print_prowlarr_guide,
            media_stack.print_jellyfin_guide,
        )
        for printer in printers:
            with self.subTest(printer=printer.__name__):
                output = self.guide_output(printer, "https://media.example.ts.net")
                self.assertNotIn("_USER", output)
                self.assertNotIn("_PASS", output)
                self.assertNotIn("_API_KEY", output)

    def test_sonarr_and_radarr_connect_to_qbittorrent_without_a_login(self):
        for printer in (
            media_stack.print_sonarr_guide,
            media_stack.print_radarr_guide,
        ):
            with self.subTest(printer=printer.__name__):
                output = self.guide_output(printer, "https://media.example.ts.net")
                self.assertIn(
                    "Set Host to qbittorrent and Port to 8080. Leave Username and Password empty.\n",
                    output,
                )
                self.assertIn("generated automatically", output)

    def test_prowlarr_guide_prints_saved_api_keys(self):
        output = self.guide_output(
            media_stack.print_prowlarr_guide,
            "https://media.example.ts.net",
            "a" * 32,
            "b" * 32,
        )
        self.assertIn(f"API Key: {'a' * 32}", output)
        self.assertIn(f"API Key: {'b' * 32}", output)
        for address in (
            "http://prowlarr:9696",
            "http://sonarr:8989",
            "http://radarr:7878",
        ):
            self.assertIn(address, output)
            self.assertNotIn(f"{address}.", output)

    @patch("media_stack.prompt_password", return_value="secret")
    @patch("media_stack.prompt_username", return_value="admin")
    def test_credentials_can_be_requested_one_service_at_a_time(
        self, prompt_username, prompt_password
    ):
        values = {}
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.ensure_credentials(values, ("ADMIN",))
        self.assertEqual(values, {"ADMIN_USER": "admin", "ADMIN_PASS": "secret"})
        self.assertEqual(prompt_username.call_count, 1)
        self.assertEqual(prompt_password.call_count, 1)
        self.assertEqual(output.getvalue(), "")
        self.assertNotIn("Sonarr credentials", output.getvalue())

    @patch("media_stack.prompt_password", return_value="jellyfin-secret")
    @patch("media_stack.prompt_username", return_value="jellyfin-admin")
    def test_jellyfin_credentials_are_collected_only_if_missing(
        self, prompt_username, prompt_password
    ):
        values = {}
        media_stack.ensure_credentials(values, ("JELLYFIN_ADMIN",))
        self.assertEqual(
            values,
            {"JELLYFIN_ADMIN_USER": "jellyfin-admin", "JELLYFIN_ADMIN_PASS": "jellyfin-secret"},
        )
        media_stack.ensure_credentials(values, ("JELLYFIN_ADMIN",))
        prompt_username.assert_called_once_with("Jellyfin administrator")
        prompt_password.assert_called_once_with("Jellyfin administrator")

    def test_nordvpn_guide_identifies_service_credentials(self):
        output = self.guide_output(media_stack.print_vpn_guide, "nordvpn")
        self.assertIn("Set up NordVPN manually > Service credentials", output)
        self.assertIn("generates", output)
        self.assertIn("following prompts", output)
        self.assertNotIn("VPN_OPENVPN", output)

    @patch("media_stack.prompt_existing_password", return_value="vpn-password")
    @patch("builtins.input", side_effect=("1", "vpn-user"))
    def test_vpn_guide_precedes_credentials(self, _input, _password):
        values = {}
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.ensure_vpn_config(values)
        self.assertLess(
            output.getvalue().index("VPN provider"),
            output.getvalue().index("VPN kill-switch setup"),
        )
        self.assertEqual(values["VPN_SERVICE_PROVIDER"], "nordvpn")
        self.assertEqual(values["VPN_GATEWAY_SERVICE"], "gluetun")
        self.assertEqual(values["VPN_TYPE"], "openvpn")
        self.assertEqual(values["VPN_SERVER_COUNTRIES"], "")
        self.assertEqual(values["VPN_OPENVPN_USER"], "vpn-user")
        self.assertEqual(values["VPN_OPENVPN_PASSWORD"], "vpn-password")

    def test_saved_vpn_credentials_do_not_repeat_the_guide(self):
        values = {
            "VPN_GATEWAY_SERVICE": "gluetun",
            "VPN_SERVICE_PROVIDER": "nordvpn",
            "VPN_TYPE": "openvpn",
            "VPN_OPENVPN_USER": "vpn-user",
            "VPN_OPENVPN_PASSWORD": "vpn-password",
        }
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.ensure_vpn_config(values)
        self.assertEqual(output.getvalue(), "")

    def test_provider_menu_maps_every_choice(self):
        for number, identifier, _label in media_stack.VPN_PROVIDER_OPTIONS:
            with self.subTest(identifier=identifier):
                with patch("builtins.input", return_value=number):
                    with contextlib.redirect_stdout(io.StringIO()):
                        self.assertEqual(media_stack.prompt_vpn_provider(), identifier)

    @patch("media_stack.prompt_existing_password", return_value="tskey-secret")
    @patch("builtins.input", side_effect=("5", "exit-node.example.ts.net"))
    def test_tailscale_guide_precedes_its_prompts(self, _input, _password):
        values = {}
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.ensure_vpn_config(values)
        self.assertIn("Tailscale exit-node setup", output.getvalue())
        self.assertEqual(values["VPN_GATEWAY_SERVICE"], "tailscale-vpn")
        self.assertEqual(values["TAILSCALE_AUTH_KEY"], "tskey-secret")
        self.assertEqual(
            values["TAILSCALE_EXIT_NODE"], "exit-node.example.ts.net"
        )

    def test_vpn_validation_is_conditional(self):
        media_stack.validate_vpn_config(
            {
                "VPN_GATEWAY_SERVICE": "gluetun",
                "VPN_SERVICE_PROVIDER": "surfshark",
                "VPN_TYPE": "openvpn",
                "VPN_OPENVPN_USER": "user",
                "VPN_OPENVPN_PASSWORD": "password",
            }
        )
        media_stack.validate_vpn_config(
            {
                "VPN_GATEWAY_SERVICE": "tailscale-vpn",
                "TAILSCALE_AUTH_KEY": "tskey-secret",
                "TAILSCALE_EXIT_NODE": "exit-node",
            }
        )
        with self.assertRaisesRegex(media_stack.StackError, "TAILSCALE_EXIT_NODE"):
            media_stack.validate_vpn_config(
                {
                    "VPN_GATEWAY_SERVICE": "tailscale-vpn",
                    "TAILSCALE_AUTH_KEY": "tskey-secret",
                }
            )


class JellyfinSetupTests(unittest.TestCase):
    def test_setup_saves_jellyfin_credentials_before_waiting(self):
        with tempfile.TemporaryDirectory() as directory:
            env_path = Path(directory) / ".env"
            existing = {
                "MEDIA_DIR": directory,
                "QBT_LEGAL_NOTICE": "confirm",
                "ACCESS_MODE": "tailscale",
                "ADMIN_ACCESS_HOST": "media.example.ts.net",
                "ADMIN_USER": "boss",
                "ADMIN_PASS": "admin-secret",
                "SONARR_API_KEY": "a" * 32,
                "RADARR_API_KEY": "b" * 32,
            }
            completed = {"qbittorrent", "sonarr", "radarr", "prowlarr", "seerr", "jellyfin-notifications", "tailscale-operator"}

            def check_saved(service):
                self.assertEqual(service, "Jellyfin")
                self.assertIn('JELLYFIN_ADMIN_PASS="secret"', env_path.read_text())

            with patch.object(media_stack, "ENV_FILE", env_path), \
                 patch("media_stack.read_env_values", return_value=existing), \
                 patch.multiple(
                     "media_stack",
                     restore_windows_console_input=DEFAULT,
                     wait_for_docker=DEFAULT,
                     grant_tailscale_operator=DEFAULT,
                     recreate_network_if_needed=DEFAULT,
                     ensure_tinyauth_users=Mock(return_value=False),
                     prepare_proxy=Mock(return_value=False),
                 ), \
                 patch("media_stack.user_ids", return_value=("1000", "1000")), \
                 patch("media_stack.ensure_vpn_config"), \
                 patch("media_stack.create_directories"), \
                 patch("media_stack.copy_homepage_config"), \
                 patch("media_stack.read_setup_state", return_value=completed), \
                 patch("media_stack.compose"), \
                 patch("media_stack.start_core_services"), \
                 patch("media_stack.prompt_username", return_value="admin"), \
                 patch("media_stack.prompt_password", return_value="secret"), \
                 patch("media_stack.wait_for_step", side_effect=check_saved), \
                 patch("media_stack.prompt_api_key", return_value="d" * 32), \
                 patch("media_stack.complete_setup_step"), \
                 patch("media_stack.sync_recyclarr"), \
                 patch("media_stack.install_autostart"), \
                 contextlib.redirect_stdout(output := io.StringIO()):
                media_stack.setup()
            self.assertIn('JELLYFIN_ADMIN_USER="admin"', env_path.read_text())
            self.assertIn(f'JELLYFIN_API_KEY="{"d" * 32}"', env_path.read_text())
            self.assertTrue(output.getvalue().endswith("Open Homepage:\nhttps://media.example.ts.net\n"))

    def test_jellyfin_key_is_saved_before_notifications_guide_uses_it(self):
        with tempfile.TemporaryDirectory() as directory:
            env_path = Path(directory) / ".env"
            events = []
            existing = {
                "MEDIA_DIR": directory,
                "QBT_LEGAL_NOTICE": "confirm",
                "ACCESS_MODE": "tailscale",
                "ADMIN_ACCESS_HOST": "media.example.ts.net",
                "ADMIN_USER": "boss",
                "ADMIN_PASS": "admin-secret",
                "SONARR_API_KEY": "a" * 32,
                "RADARR_API_KEY": "b" * 32,
                "JELLYFIN_ADMIN_USER": "jelly",
                "JELLYFIN_ADMIN_PASS": "jelly-secret",
            }
            completed = {"qbittorrent", "sonarr", "radarr", "prowlarr", "seerr", "tailscale-operator"}

            def prompt_key(service):
                events.append(f"key:{service}")
                return "d" * 32

            def notifications_guide(_host, key):
                self.assertIn(f'JELLYFIN_API_KEY="{key}"', env_path.read_text())
                events.append("notifications")

            with patch.object(media_stack, "ENV_FILE", env_path), \
                 patch("media_stack.read_env_values", return_value=existing), \
                 patch.multiple(
                     "media_stack",
                     restore_windows_console_input=DEFAULT,
                     wait_for_docker=DEFAULT,
                     grant_tailscale_operator=DEFAULT,
                     recreate_network_if_needed=DEFAULT,
                     ensure_tinyauth_users=Mock(return_value=False),
                     prepare_proxy=Mock(return_value=False),
                 ), \
                 patch("media_stack.user_ids", return_value=("1000", "1000")), \
                 patch("media_stack.ensure_vpn_config"), \
                 patch("media_stack.create_directories"), \
                 patch("media_stack.copy_homepage_config"), \
                 patch("media_stack.read_setup_state", return_value=completed), \
                 patch("media_stack.compose"), \
                 patch("media_stack.start_core_services"), \
                 patch("media_stack.print_jellyfin_guide"), \
                 patch("media_stack.prompt_api_key", side_effect=prompt_key), \
                 patch("media_stack.print_jellyfin_notifications_guide", side_effect=notifications_guide), \
                 patch("media_stack.wait_for_step", side_effect=events.append), \
                 patch("media_stack.complete_setup_step", side_effect=lambda _path, _steps, step: events.append(f"done:{step}")), \
                 patch("media_stack.sync_recyclarr"), \
                 patch("media_stack.install_autostart"), \
                 contextlib.redirect_stdout(io.StringIO()):
                media_stack.setup()
            self.assertEqual(events, [
                "Jellyfin", "key:Jellyfin", "done:jellyfin",
                "notifications", "Jellyfin notification", "done:jellyfin-notifications",
            ])

    def test_seerr_guide_follows_recyclarr_sync(self):
        with tempfile.TemporaryDirectory() as directory:
            events = []
            existing = {
                "MEDIA_DIR": directory,
                "QBT_LEGAL_NOTICE": "confirm",
                "ACCESS_MODE": "tailscale",
                "ADMIN_ACCESS_HOST": "media.example.ts.net",
                "ADMIN_USER": "boss",
                "ADMIN_PASS": "admin-secret",
                "SONARR_API_KEY": "a" * 32,
                "RADARR_API_KEY": "b" * 32,
                "JELLYFIN_ADMIN_USER": "jelly",
                "JELLYFIN_ADMIN_PASS": "jelly-secret",
            }
            existing["JELLYFIN_API_KEY"] = "c" * 32
            completed = {"qbittorrent", "sonarr", "radarr", "prowlarr", "jellyfin", "jellyfin-notifications", "tailscale-operator"}
            with patch.object(media_stack, "ENV_FILE", Path(directory) / ".env"), \
                 patch("media_stack.read_env_values", return_value=existing), \
                 patch.multiple(
                     "media_stack",
                     restore_windows_console_input=DEFAULT,
                     wait_for_docker=DEFAULT,
                     grant_tailscale_operator=DEFAULT,
                     recreate_network_if_needed=DEFAULT,
                     ensure_tinyauth_users=Mock(return_value=False),
                     prepare_proxy=Mock(return_value=False),
                 ), \
                 patch("media_stack.user_ids", return_value=("1000", "1000")), \
                 patch("media_stack.ensure_vpn_config"), \
                 patch("media_stack.create_directories"), \
                 patch("media_stack.copy_homepage_config"), \
                 patch("media_stack.read_setup_state", return_value=completed), \
                 patch("media_stack.compose"), \
                 patch("media_stack.start_core_services"), \
                 patch("media_stack.sync_recyclarr", side_effect=lambda _values: events.append("recyclarr")), \
                 patch("media_stack.print_seerr_guide", side_effect=lambda *_args: events.append("guide")), \
                 patch("media_stack.wait_for_step", side_effect=events.append), \
                 patch("media_stack.complete_setup_step", side_effect=lambda _path, _steps, step: events.append(f"done:{step}")), \
                 patch("media_stack.install_autostart"), \
                 contextlib.redirect_stdout(io.StringIO()):
                media_stack.setup()
            self.assertEqual(events, ["recyclarr", "guide", "Seerr", "done:seerr"])


class ComposeSecurityTests(unittest.TestCase):
    def test_qbittorrent_uses_only_selected_gateway_network(self):
        compose = (media_stack.ROOT / "compose.yaml").read_text(encoding="utf-8")
        qbittorrent = compose.split("  qbittorrent:", 1)[1].split(
            "\n  prowlarr:", 1
        )[0]
        gluetun = compose.split("  gluetun:", 1)[1].split("\n  tailscale-vpn:", 1)[0]
        tailscale = compose.split("  tailscale-vpn:", 1)[1].split(
            "\n  homepage:", 1
        )[0]
        self.assertIn(
            'network_mode: "service:${VPN_GATEWAY_SERVICE}"', qbittorrent
        )
        self.assertNotIn("ports:", qbittorrent)
        prowlarr = compose.split("  prowlarr:", 1)[1].split("\n  sonarr-app:", 1)[0]
        self.assertIn('network_mode: "service:${VPN_GATEWAY_SERVICE}"', prowlarr)
        self.assertNotIn("ports:", prowlarr)
        for gateway in (gluetun, tailscale):
            self.assertIn('ports:\n      - "6881:6881/tcp"\n      - "6881:6881/udp"\n', gateway)
            self.assertNotIn('"9696:9696/tcp"', gateway)
            self.assertNotIn('"8080:8080/tcp"', gateway)
            self.assertTrue(gateway.rstrip().endswith("aliases:\n          - qbittorrent"))
        self.assertIn('FIREWALL_INPUT_PORTS: "8080,6881,8000,9696"', gluetun)
        self.assertIn("--exit-node=${TAILSCALE_EXIT_NODE:-}", tailscale)

    def test_proxy_owns_internal_names_and_only_published_web_ports(self):
        compose = (media_stack.ROOT / "compose.yaml").read_text(encoding="utf-8")
        caddy = compose.split("  caddy:", 1)[1].split("\n  tinyauth:", 1)[0]
        for alias in ("sonarr", "radarr", "prowlarr", "jellyfin"):
            self.assertIn(f"          - {alias}\n", caddy)
        for service in ("sonarr", "radarr", "jellyfin"):
            self.assertNotIn(f"\n  {service}:\n", compose)
        for port in ("80", "443", "5055", "9091"):
            self.assertIn(f'"{port}:{port}/tcp"', caddy)
        published = [line.strip() for line in compose.splitlines() if line.strip().startswith('- "') and ":" in line and "/" in line and "${" not in line and "./" not in line]
        self.assertEqual(sorted(set(published)), sorted({
            '- "6881:6881/tcp"', '- "6881:6881/udp"', '- "7359:7359/udp"',
            '- "80:80/tcp"', '- "443:443/tcp"', '- "5055:5055/tcp"', '- "9091:9091/tcp"',
        }))

    def test_admin_apps_use_their_path_and_proxy_sign_in(self):
        compose = (media_stack.ROOT / "compose.yaml").read_text(encoding="utf-8")
        for app in ("SONARR", "RADARR", "PROWLARR"):
            with self.subTest(app=app):
                self.assertIn(f"{app}__SERVER__URLBASE: /{app.lower()}\n", compose)
                self.assertIn(f"{app}__AUTH__METHOD: External\n", compose)
        self.assertIn('JELLYFIN_PublishedServerUrl: "${ACCESS_URL}/jellyfin"', compose)
        self.assertIn(f"- subnet: {media_stack.NETWORK_SUBNET}\n", compose)

    def test_homepage_reads_docker_only_through_read_only_proxy(self):
        compose = (media_stack.ROOT / "compose.yaml").read_text(encoding="utf-8")
        homepage = compose.split("  homepage:", 1)[1].split("\n  dockerproxy:", 1)[0]
        proxy = compose.split("  dockerproxy:", 1)[1].split("\n  glances:", 1)[0]
        self.assertNotIn("docker.sock", homepage)
        self.assertIn("/var/run/docker.sock:/var/run/docker.sock:ro", proxy)
        self.assertIn('POST: "0"', proxy)
        self.assertNotIn("ports:", proxy)

    def run_start_core_services(self, values, **options):
        events = []
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        values = {"CONFIG_DIR": directory.name, **values}
        with patch("media_stack.compose", side_effect=lambda *args: events.append(args)), \
             patch("media_stack.write_qbittorrent_login_bypass", side_effect=lambda _dir: events.append("qbittorrent-bypass")), \
             patch("media_stack.jellyfin_base_url_is_set", return_value=True), \
             patch("media_stack.save_vpn_country_list", side_effect=lambda _values: events.append("countries")), \
             patch("media_stack.wait_for_vpn_gateway", side_effect=lambda _values: events.append("wait")):
            media_stack.start_core_services(values, **options)
        return events

    def test_other_services_start_before_vpn_services_wait_for_gateway(self):
        events = self.run_start_core_services({"VPN_GATEWAY_SERVICE": "gluetun"})
        self.assertEqual(events[0], ("stop", "qbittorrent", "prowlarr", "tailscale-vpn"))
        self.assertEqual(events[1], "qbittorrent-bypass")
        self.assertEqual(events[2][:4], ("up", "-d", "--no-deps", "--remove-orphans"))
        self.assertIn("gluetun", events[2])
        for service in ("dockerproxy", "homepage", "caddy", "tinyauth", "jellyfin-app", "sonarr-app", "radarr-app", "vpn-country"):
            self.assertIn(service, events[2])
        self.assertNotIn("qbittorrent", events[2])
        self.assertNotIn("prowlarr", events[2])
        self.assertEqual(events[3:5], ["countries", "wait"])
        self.assertEqual(events[5], ("up", "-d", "--no-deps", "qbittorrent", "prowlarr"))

    def test_changed_gateway_access_and_proxy_restart_before_vpn_services(self):
        events = self.run_start_core_services(
            {"VPN_GATEWAY_SERVICE": "gluetun"}, restart_gateway=True, restart_proxy=True
        )
        self.assertEqual(events[3:5], [("restart", "gluetun"), ("restart", "caddy")])
        self.assertEqual(events[-1], ("up", "-d", "--no-deps", "qbittorrent", "prowlarr"))

    def test_only_a_network_with_another_address_range_is_recreated(self):
        for subnet, expected in (
            ("172.18.0.0/16", [("down", "--remove-orphans")]),
            (media_stack.NETWORK_SUBNET, []),
            (None, []),
        ):
            with self.subTest(subnet=subnet):
                events = []
                with patch("media_stack.stack_network_subnet", return_value=subnet), \
                     patch("media_stack.compose", side_effect=lambda *args: events.append(args)):
                    media_stack.recreate_network_if_needed()
                self.assertEqual(events, expected)

    def test_missing_jellyfin_base_url_is_written_while_jellyfin_is_stopped(self):
        events = []
        with tempfile.TemporaryDirectory() as directory, \
             patch("media_stack.compose", side_effect=lambda *args: events.append(args)), \
             patch("media_stack.save_vpn_country_list"), \
             patch("media_stack.wait_for_vpn_gateway"):
            media_stack.start_core_services({"CONFIG_DIR": directory, "VPN_GATEWAY_SERVICE": "gluetun"})
            self.assertTrue(media_stack.jellyfin_base_url_is_set(Path(directory)))
        self.assertEqual(events[1], ("stop", "jellyfin-app"))
        self.assertEqual(events[2][0], "up")

    def test_gluetun_access_file_reports_only_real_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            values = {"GLUETUN_API_KEY": "panel-key", "GLUETUN_CONTROL_KEY": "control-key"}
            self.assertTrue(media_stack.write_gluetun_auth(Path(directory), values))
            self.assertFalse(media_stack.write_gluetun_auth(Path(directory), values))
            values["GLUETUN_CONTROL_KEY"] = "new-key"
            self.assertTrue(media_stack.write_gluetun_auth(Path(directory), values))

    def test_tailscale_stops_and_skips_vpn_country_page(self):
        with patch("media_stack.save_vpn_country_list") as save_list:
            events = self.run_start_core_services({"VPN_GATEWAY_SERVICE": "tailscale-vpn"})
        self.assertEqual(events[0], ("stop", "qbittorrent", "prowlarr", "gluetun", "vpn-country"))
        self.assertNotIn("vpn-country", events[2])
        save_list.assert_not_called()

    def test_vpn_country_page_cannot_read_environment_file(self):
        compose = (media_stack.ROOT / "compose.yaml").read_text(encoding="utf-8")
        page = compose.split("  vpn-country:", 1)[1].split("\n  qbittorrent:", 1)[0]
        self.assertNotIn(".env", page)
        self.assertNotIn("env_file", page)
        self.assertIn('"./vpn_country.py:/app/vpn_country.py:ro"', page)
        self.assertIn('"${CONFIG_DIR}/vpn-country:/data"', page)

    def test_country_list_keeps_unique_openvpn_countries(self):
        servers = [
            {"vpn": "openvpn", "country": "France"},
            {"vpn": "wireguard", "country": "Japan"},
            {"vpn": "openvpn", "country": "Belgium"},
            {"vpn": "openvpn", "country": "France"},
        ]
        self.assertEqual(media_stack.vpn_countries(servers), ["Belgium", "France"])

    def test_saved_vpn_country_is_copied_into_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            selection = Path(directory) / "vpn-country" / "selection.json"
            selection.parent.mkdir()
            selection.write_text('{"countries": ["France"]}')
            values = {"CONFIG_DIR": directory, "VPN_GATEWAY_SERVICE": "gluetun", "VPN_SERVER_COUNTRIES": ""}
            self.assertTrue(media_stack.apply_saved_vpn_country(values))
            self.assertEqual(values["VPN_SERVER_COUNTRIES"], "France")
            self.assertFalse(media_stack.apply_saved_vpn_country(values))
            selection.write_text('{"countries": []}')
            self.assertTrue(media_stack.apply_saved_vpn_country(values))
            self.assertEqual(values["VPN_SERVER_COUNTRIES"], "")

    @patch("media_stack.time.sleep")
    def test_vpn_wait_has_no_time_limit_and_reports_once(self, _sleep):
        checks = [False] * 500 + [True]
        output = io.StringIO()
        with patch("media_stack.vpn_gateway_ready", side_effect=checks) as ready, \
             contextlib.redirect_stdout(output):
            media_stack.wait_for_vpn_gateway({"VPN_GATEWAY_SERVICE": "gluetun"})
        self.assertEqual(ready.call_count, 501)
        self.assertEqual(
            output.getvalue(),
            "Waiting for the VPN connection. qBittorrent and Prowlarr will start when it connects.\n",
        )

    @patch("media_stack.vpn_gateway_ready", return_value=True)
    def test_ready_vpn_prints_nothing(self, _ready):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.wait_for_vpn_gateway({"VPN_GATEWAY_SERVICE": "gluetun"})
        self.assertEqual(output.getvalue(), "")

    @patch("media_stack.shutil.which", return_value="C:/Tailscale/tailscale.exe")
    @patch("media_stack.run")
    def test_tailscale_dns_name_is_read_without_trailing_dot(self, run, _which):
        run.return_value.returncode = 0
        run.return_value.stdout = '{"Self":{"DNSName":"media.example.ts.net."}}'
        self.assertEqual(
            media_stack.tailscale_dns_name(), "media.example.ts.net"
        )


class StartupTests(unittest.TestCase):
    def test_user_systemd_unit_has_no_root_user(self):
        unit = media_stack.systemd_unit(
            Path("/repo/media_stack.py"), Path("/usr/bin/python")
        )
        self.assertIn("WantedBy=default.target", unit)
        self.assertNotIn("User=", unit)

    def test_system_systemd_unit_uses_selected_user(self):
        unit = media_stack.systemd_unit(
            Path("/repo/media_stack.py"), Path("/usr/bin/python"), "alice"
        )
        self.assertIn("User=alice", unit)
        self.assertIn("Requires=docker.service", unit)
        self.assertIn("WantedBy=multi-user.target", unit)
        self.assertIn("TimeoutStartSec=infinity", unit)

    def test_systemd_unit_quotes_paths(self):
        unit = media_stack.systemd_unit(
            Path("/repo with spaces/media_stack.py"), Path("/usr/bin/python")
        )
        self.assertIn("'/repo with spaces/media_stack.py'", unit)

    def test_windows_launcher_is_hidden(self):
        launcher = media_stack.windows_launcher(
            Path("C:/stack/media_stack.py"),
            Path("C:/Python/pythonw.exe"),
            Path("C:/stack/config/startup.log"),
        )
        self.assertIn(", 0, False", launcher)
        self.assertIn("media_stack.py", launcher)


class DockerTests(unittest.TestCase):
    @patch("media_stack.shutil.which", return_value=None)
    def test_missing_docker_is_reported(self, _which):
        with self.assertRaisesRegex(media_stack.StackError, "not installed"):
            media_stack.wait_for_docker(timeout=0)

    @patch("media_stack.restore_file")
    @patch("media_stack.compose", side_effect=OSError("docker disappeared"))
    def test_rollback_restores_environment_when_docker_cleanup_fails(
        self, _compose, restore_file
    ):
        with contextlib.redirect_stderr(io.StringIO()):
            media_stack.rollback_setup(b"old environment", stop_stack=True)
        restore_file.assert_called_once_with(
            media_stack.ENV_FILE, b"old environment"
        )

    @patch("media_stack.restore_file")
    @patch("media_stack.compose")
    def test_rollback_preserves_preexisting_stack(self, compose, restore_file):
        media_stack.rollback_setup(b"old environment", stop_stack=False)
        compose.assert_not_called()
        restore_file.assert_called_once_with(
            media_stack.ENV_FILE, b"old environment"
        )


class VpnStatusTests(unittest.TestCase):
    @patch("media_stack.wait_for_docker")
    @patch("media_stack.read_env", return_value={"VPN_GATEWAY_SERVICE": "gluetun", "VPN_SERVER_COUNTRIES": "France"})
    @patch("media_stack.run")
    def test_gluetun_status_reports_health_and_country(self, run, _env, _docker):
        run.return_value.returncode = 0
        run.return_value.stdout = "healthy\n"
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.vpn_status()
        self.assertEqual(output.getvalue(), "Gluetun VPN: healthy\nSelected countries:\nFrance\n")
        run.assert_called_once_with(
            ("docker", "inspect", "gluetun", "--format", "{{.State.Health.Status}}"),
            check=False, capture_output=True,
        )

    @patch("media_stack.wait_for_docker")
    @patch("media_stack.read_env", return_value={"VPN_GATEWAY_SERVICE": "tailscale-vpn", "TAILSCALE_EXIT_NODE": "node-one"})
    @patch("media_stack.run")
    def test_tailscale_status_reports_offline_exit_node(self, run, _env, _docker):
        run.return_value.returncode = 0
        run.return_value.stdout = '{"ExitNodeStatus":{"Online":false}}'
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.vpn_status()
        self.assertEqual(output.getvalue(), "Tailscale exit node: not online\nSelected exit node:\nnode-one\n")


class CredentialsTests(unittest.TestCase):
    def run_credentials(self, saved, section, inputs=(), passwords=()):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        path = Path(directory.name) / ".env"
        media_stack.write_env(path, saved)
        output = io.StringIO()
        with patch.object(media_stack, "ENV_FILE", path), \
             patch("builtins.input", side_effect=list(inputs)), \
             patch("media_stack.masked_password", side_effect=list(passwords)), \
             contextlib.redirect_stdout(output):
            code = media_stack.main(["credentials", section])
        return code, output.getvalue(), media_stack.read_env_values(path)

    def test_no_group_lists_choices_without_reading_credentials(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.credentials()
        self.assertIn("jellyfin", output.getvalue())
        self.assertIn("vpn", output.getvalue())
        self.assertNotIn("secret", output.getvalue())

    def test_listing_puts_each_value_on_its_label_line_and_no_keeps_everything(self):
        saved = {"JELLYFIN_ADMIN_USER": "admin", "JELLYFIN_ADMIN_PASS": "secret$!"}
        for answer in ("", "n"):
            with self.subTest(answer=answer):
                code, output, values = self.run_credentials(saved, "jellyfin", [answer])
                self.assertEqual(code, 0)
                self.assertEqual(
                    output,
                    "Administrator username: admin\nAdministrator password: secret$!\n",
                )
                self.assertEqual(values, saved)

    def test_yes_with_enter_everywhere_changes_nothing(self):
        saved = {"JELLYFIN_ADMIN_USER": "admin", "JELLYFIN_ADMIN_PASS": "secret$!"}
        _code, output, values = self.run_credentials(saved, "jellyfin", ["maybe", "y", ""], [""])
        self.assertIn("Enter Y or N.\n", output)
        self.assertTrue(output.endswith("No changes.\n"))
        self.assertEqual(values, saved)

    def test_group_without_changeable_values_does_not_ask(self):
        saved = {"ACCESS_MODE": "local", "ACCESS_URL": "http://media.local", "MEDIA_DIR": "/media"}
        _code, output, _values = self.run_credentials(saved, "access")
        self.assertEqual(output, "Mode: local\nAddress: http://media.local\nMedia directory: /media\n")

    def test_changed_password_is_confirmed_and_saved(self):
        saved = {"ADMIN_USER": "admin", "ADMIN_PASS": "old"}
        code, output, values = self.run_credentials(
            saved, "admin", ["y", ""], ["new", "typo", "new", "new"]
        )
        self.assertEqual(code, 0)
        self.assertIn("Values do not match.\n", output)
        self.assertTrue(output.endswith("Saved.\nThe stack uses the changed values after its next start.\n"))
        self.assertEqual(values, {"ADMIN_USER": "admin", "ADMIN_PASS": "new"})

    def test_api_key_is_validated_and_change_applies_at_next_start(self):
        saved = {"SONARR_API_KEY": "a" * 32}
        code, output, values = self.run_credentials(
            saved, "sonarr", ["y", "not-a-key", "B" * 32]
        )
        self.assertEqual(code, 0)
        self.assertIn("Error: API keys must contain exactly 32 hexadecimal characters.", output)
        self.assertEqual(values["SONARR_API_KEY"], "b" * 32)
        self.assertTrue(output.endswith("Saved.\nThe stack uses the changed values after its next start.\n"))

    def test_vpn_shows_fixed_settings_without_prompting_for_them(self):
        saved = {
            "VPN_GATEWAY_SERVICE": "gluetun",
            "VPN_SERVICE_PROVIDER": "nordvpn",
            "VPN_SERVER_COUNTRIES": "Belgium",
            "VPN_OPENVPN_USER": "vpn-user",
            "VPN_OPENVPN_PASSWORD": "vpn-secret",
            "TAILSCALE_AUTH_KEY": "old-tailscale-key",
        }
        code, output, values = self.run_credentials(saved, "vpn", ["y", ""], [""])
        self.assertEqual(code, 0)
        self.assertIn("Server countries: Belgium\n", output)
        self.assertNotIn("old-tailscale-key", output)
        self.assertNotIn("new provider", output.lower())
        self.assertEqual(values, saved)

    def test_tailscale_vpn_shows_only_tailscale_values(self):
        saved = {
            "VPN_GATEWAY_SERVICE": "tailscale-vpn",
            "TAILSCALE_EXIT_NODE": "node-one",
            "TAILSCALE_AUTH_KEY": "tskey-secret",
            "VPN_OPENVPN_PASSWORD": "old-vpn-password",
        }
        _code, output, _values = self.run_credentials(saved, "vpn", ["n"])
        self.assertIn("node-one", output)
        self.assertIn("tskey-secret", output)
        self.assertNotIn("old-vpn-password", output)

    def test_unconfigured_service_reports_no_saved_values(self):
        _code, output, _values = self.run_credentials({"MEDIA_DIR": "/media"}, "jellyfin")
        self.assertEqual(output, "No saved values for jellyfin.\n")

    def test_missing_setup_returns_error(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(media_stack, "ENV_FILE", Path(directory) / ".env"), \
                 contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(media_stack.main(["credentials", "jellyfin"]), 1)


class JellyfinRenameTests(unittest.TestCase):
    def test_old_jellyfin_names_are_moved_and_saved(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            path.write_text('MEDIA_DIR="/media"\nJELLYFIN_USER="admin"\nJELLYFIN_PASS="secret"\n')
            values = media_stack.read_env_values(path)
            self.assertEqual(
                values,
                {"MEDIA_DIR": "/media", "JELLYFIN_ADMIN_USER": "admin", "JELLYFIN_ADMIN_PASS": "secret"},
            )
            self.assertEqual(
                path.read_text(),
                'MEDIA_DIR="/media"\nJELLYFIN_ADMIN_USER="admin"\nJELLYFIN_ADMIN_PASS="secret"\n',
            )

    @patch("media_stack.prompt_password", return_value="secret")
    @patch("builtins.input", return_value="")
    def test_jellyfin_prompts_name_the_administrator(self, prompt_input, _password):
        values = {}
        media_stack.ensure_credentials(values, ("JELLYFIN_ADMIN",))
        prompt_input.assert_called_once_with("Jellyfin administrator username [admin]: ")
        self.assertEqual(values, {"JELLYFIN_ADMIN_USER": "admin", "JELLYFIN_ADMIN_PASS": "secret"})


class ProxyConfigTests(unittest.TestCase):
    def access_values(self, mode, host, gateway="gluetun"):
        values = {"ACCESS_MODE": mode, "ADMIN_ACCESS_HOST": host, "VPN_GATEWAY_SERVICE": gateway}
        media_stack.apply_access_values(values)
        return values

    def test_qbittorrent_bypass_keeps_other_settings_and_is_set_once(self):
        with tempfile.TemporaryDirectory() as directory:
            config_dir = Path(directory)
            path = media_stack.qbittorrent_config_file(config_dir)
            path.parent.mkdir(parents=True)
            path.write_text(
                "[BitTorrent]\nSession\\DefaultSavePath=/media/Downloads\n"
                "[Preferences]\nWebUI\\Port=8080\nWebUI\\AuthSubnetWhitelist=10.0.0.0/8\n"
                "[Meta]\nMigrationVersion=9999\n",
                encoding="utf-8",
            )
            self.assertTrue(media_stack.write_qbittorrent_login_bypass(config_dir))
            self.assertEqual(
                path.read_text(encoding="utf-8"),
                "[BitTorrent]\nSession\\DefaultSavePath=/media/Downloads\n"
                "[Preferences]\nWebUI\\Port=8080\nWebUI\\AuthSubnetWhitelistEnabled=true\n"
                f"WebUI\\AuthSubnetWhitelist={media_stack.NETWORK_SUBNET}\n"
                "[Meta]\nMigrationVersion=9999\n",
            )
            self.assertFalse(media_stack.write_qbittorrent_login_bypass(config_dir))

    def test_qbittorrent_bypass_creates_missing_config(self):
        with tempfile.TemporaryDirectory() as directory:
            config_dir = Path(directory)
            self.assertTrue(media_stack.write_qbittorrent_login_bypass(config_dir))
            self.assertEqual(
                media_stack.qbittorrent_config_file(config_dir).read_text(encoding="utf-8"),
                "[Preferences]\nWebUI\\AuthSubnetWhitelistEnabled=true\n"
                f"WebUI\\AuthSubnetWhitelist={media_stack.NETWORK_SUBNET}\n",
            )

    def test_jellyfin_base_url_keeps_other_network_settings(self):
        with tempfile.TemporaryDirectory() as directory:
            config_dir = Path(directory)
            path = media_stack.jellyfin_network_file(config_dir)
            path.parent.mkdir(parents=True)
            path.write_text(
                '<?xml version="1.0" encoding="utf-8"?>\n'
                '<NetworkConfiguration xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" '
                'xmlns:xsd="http://www.w3.org/2001/XMLSchema">\n'
                "  <BaseUrl />\n  <EnableHttps>false</EnableHttps>\n  <InternalHttpPort>8096</InternalHttpPort>\n"
                "</NetworkConfiguration>\n",
                encoding="utf-8",
            )
            self.assertFalse(media_stack.jellyfin_base_url_is_set(config_dir))
            media_stack.write_jellyfin_base_url(config_dir)
            self.assertTrue(media_stack.jellyfin_base_url_is_set(config_dir))
            root = media_stack.ElementTree.parse(path).getroot()
            self.assertEqual(root.findtext("EnableHttps"), "false")
            self.assertEqual(root.findtext("InternalHttpPort"), "8096")

    def test_jellyfin_base_url_creates_missing_network_file(self):
        with tempfile.TemporaryDirectory() as directory:
            config_dir = Path(directory)
            media_stack.write_jellyfin_base_url(config_dir)
            root = media_stack.ElementTree.parse(media_stack.jellyfin_network_file(config_dir)).getroot()
            self.assertEqual(root.tag, "NetworkConfiguration")
            self.assertEqual(root.findtext("BaseUrl"), "/jellyfin")

    def test_jellyfin_stays_outside_the_sign_in_and_admin_pages_inside(self):
        text = media_stack.caddyfile(self.access_values("tailscale", "media.example.ts.net"))
        site = text.split("\nmedia.example.ts.net:5055 {", 1)[0]
        public, protected = site.split("\thandle {\n\t\tforward_auth tinyauth:3000 {", 1)
        self.assertIn("handle @jellyfin {\n\t\treverse_proxy jellyfin-app:8096", public)
        self.assertIn("redir @seerr https://media.example.ts.net:5055/", public)
        for upstream in ("sonarr-app:8989", "radarr-app:7878", "gluetun:9696", "gluetun:8080", "vpn-country:8090", "homepage:3000"):
            self.assertNotIn(upstream, public)
            self.assertIn(f"reverse_proxy {upstream}", protected)

    def test_internal_addresses_add_the_path_only_when_missing(self):
        text = media_stack.caddyfile(self.access_values("local", "media.local", "tailscale-vpn"))
        for port, path, upstream in (
            (8989, "/sonarr", "sonarr-app"),
            (7878, "/radarr", "radarr-app"),
            (9696, "/prowlarr", "tailscale-vpn"),
            (8096, "/jellyfin", "jellyfin-app"),
        ):
            self.assertIn(
                f"http://:{port} {{\n\t@missing not path {path} {path}/*\n"
                f"\trewrite @missing {path}{{uri}}\n\treverse_proxy {upstream}:{port}\n}}",
                text,
            )
        self.assertNotIn("vpn-country", text)

    def test_each_access_mode_sets_its_address_scheme_and_certificate(self):
        tls = "\ttls /certs/cert.pem /certs/key.pem\n"
        cases = (
            ("tailscale", "media.example.ts.net", "media.example.ts.net {\n" + tls, 3),
            ("domain", "media.example.com", "media.example.com {\n", 0),
            ("local", "media.local", "http://media.local {\n", 0),
        )
        for mode, host, site, tls_count in cases:
            with self.subTest(mode=mode):
                text = media_stack.caddyfile(self.access_values(mode, host))
                self.assertIn("\n" + site, text)
                self.assertEqual(text.count(tls), tls_count)
                prefix = "http://" if mode == "local" else ""
                self.assertIn(f"\n{prefix}{host}:{media_stack.SEERR_PORT} {{", text)
                self.assertIn(f"\n{prefix}{host}:{media_stack.SIGN_IN_PORT} {{", text)

    def test_certificate_change_is_reported_only_when_files_change(self):
        with tempfile.TemporaryDirectory() as directory:
            values = {"ACCESS_MODE": "tailscale", "ADMIN_ACCESS_HOST": "media.example.ts.net", "CONFIG_DIR": directory}
            issued = {"content": b"first"}

            def fake_run(command, **_options):
                Path(command[command.index("--cert-file") + 1]).write_bytes(issued["content"])
                Path(command[command.index("--key-file") + 1]).write_bytes(issued["content"])
                return media_stack.subprocess.CompletedProcess(command, 0, "", "")

            with patch("media_stack.shutil.which", return_value="/usr/bin/tailscale"), \
                 patch("media_stack.tailscale_serve_uses_https_port", return_value=False), \
                 patch("media_stack.run", side_effect=fake_run):
                self.assertTrue(media_stack.renew_tailscale_certificate(values))
                self.assertFalse(media_stack.renew_tailscale_certificate(values))
                issued["content"] = b"renewed"
                self.assertTrue(media_stack.renew_tailscale_certificate(values))

    def test_tailscale_serve_on_https_port_blocks_tailscale_mode(self):
        serve_status = '{"TCP": {"443": {"HTTPS": true}}, "Web": {"media.example.ts.net:443": {}}}'
        result = media_stack.subprocess.CompletedProcess((), 0, serve_status, "")
        values = {"ACCESS_MODE": "tailscale", "ADMIN_ACCESS_HOST": "media.example.ts.net", "CONFIG_DIR": "/unused"}
        with patch("media_stack.shutil.which", return_value="/usr/bin/tailscale"), \
             patch("media_stack.run", return_value=result):
            with self.assertRaisesRegex(media_stack.StackError, "tailscale serve reset"):
                media_stack.renew_tailscale_certificate(values)
        output = io.StringIO()
        with patch("media_stack.tailscale_dns_name", return_value="media.example.ts.net"), \
             patch("media_stack.tailscale_serve_uses_https_port", return_value=True), \
             patch("media_stack.socket.gethostname", return_value="media-box"), \
             patch("builtins.input", side_effect=("1", "3", "")), \
             contextlib.redirect_stdout(output):
            media_stack.ensure_access_config(values := {})
        self.assertIn("\ntailscale serve reset\n", output.getvalue())
        self.assertEqual(values["ACCESS_MODE"], "local")

    def test_other_access_modes_do_not_request_a_tailscale_certificate(self):
        with patch("media_stack.run") as run:
            self.assertFalse(media_stack.renew_tailscale_certificate({"ACCESS_MODE": "domain"}))
        run.assert_not_called()


class SignInTests(unittest.TestCase):
    HASH = "$2a$10$blxk23tgU0Z6eUuBbooKXesaHLxgm0N2biNh5nsDWU70Bs.R2ihtq"

    def test_admin_login_is_hashed_from_either_tinyauth_output_format(self):
        outputs = (
            f"\x1b[38;5;75mauth\x1b[m:\n    users:\n        - boss:{self.HASH}\n",
            f"Created user 'boss'.\n\nEnvironment variable:\n\nTINYAUTH_AUTH_USERS=boss:{self.HASH}\n",
        )
        for output in outputs:
            with self.subTest(output=output[:20]):
                values = {"ADMIN_USER": "boss", "ADMIN_PASS": "secret"}
                result = media_stack.subprocess.CompletedProcess((), 0, output, "")
                with patch("media_stack.compose", return_value=result) as compose:
                    self.assertTrue(media_stack.ensure_tinyauth_users(values))
                self.assertEqual(values["TINYAUTH_USERS"], f"boss:{self.HASH}")
                self.assertIn("secret", compose.call_args.args)

    def test_hash_is_only_recreated_when_the_admin_login_changes(self):
        values = {"ADMIN_USER": "boss", "ADMIN_PASS": "secret"}
        result = media_stack.subprocess.CompletedProcess((), 0, f"boss:{self.HASH}\n", "")
        with patch("media_stack.compose", return_value=result) as compose:
            self.assertTrue(media_stack.ensure_tinyauth_users(values))
            self.assertFalse(media_stack.ensure_tinyauth_users(values))
            values["ADMIN_PASS"] = "changed"
            self.assertTrue(media_stack.ensure_tinyauth_users(values))
        self.assertEqual(compose.call_count, 2)

    def test_failed_hashing_is_reported(self):
        result = media_stack.subprocess.CompletedProcess((), 1, "", "pull access denied")
        with patch("media_stack.compose", return_value=result):
            with self.assertRaisesRegex(media_stack.StackError, "admin sign-in"):
                media_stack.ensure_tinyauth_users({"ADMIN_USER": "boss", "ADMIN_PASS": "secret"})


class AccessModeTests(unittest.TestCase):
    def test_local_mode_proposes_machine_name_and_rejects_dotless_names(self):
        values = {}
        output = io.StringIO()
        with patch("media_stack.socket.gethostname", return_value="media-box"), \
             patch("builtins.input", side_effect=("3", "media-box", "")) as prompt, \
             contextlib.redirect_stdout(output):
            media_stack.ensure_access_config(values)
        self.assertEqual(prompt.call_args.args[0], "Local network name [media-box.local]: ")
        self.assertIn("Error: The address must be a name containing a dot", output.getvalue())
        self.assertEqual(values["ACCESS_MODE"], "local")
        self.assertEqual(values["ADMIN_ACCESS_HOST"], "media-box.local")
        self.assertEqual(values["ACCESS_URL"], "http://media-box.local")
        self.assertEqual(values["TINYAUTH_SECURE_COOKIE"], "false")

    def test_mode_guide_and_domain_guide_precede_their_prompts(self):
        values = {}
        events = []

        def answer(prompt):
            events.append(("prompt", prompt, output.getvalue()))
            return {"Select an access mode [1]: ": "2", "Domain name: ": "Media.Example.com"}[prompt]

        output = io.StringIO()
        with patch("builtins.input", side_effect=answer), contextlib.redirect_stdout(output):
            media_stack.ensure_access_config(values)
        self.assertIn("Pros:", events[0][2])
        self.assertIn("Cons:", events[0][2])
        self.assertIn("forward TCP ports 80, 443, 5055, and 9091", events[1][2])
        self.assertEqual(values["ADMIN_ACCESS_HOST"], "media.example.com")
        self.assertEqual(values["ACCESS_URL"], "https://media.example.com")
        self.assertEqual(values["TINYAUTH_SECURE_COOKIE"], "true")

    def test_tailscale_mode_requires_a_signed_in_tailscale(self):
        values = {}
        output = io.StringIO()
        with patch("media_stack.tailscale_dns_name", return_value=""), \
             patch("builtins.input", side_effect=("1", "3", "")), \
             patch("media_stack.socket.gethostname", return_value="media-box"), \
             contextlib.redirect_stdout(output):
            media_stack.ensure_access_config(values)
        self.assertIn("Tailscale is not installed or not signed in on this computer. Choose another mode.", output.getvalue())
        self.assertEqual(values["ACCESS_MODE"], "local")


class CommandHelpTests(unittest.TestCase):
    def help_output(self, arguments):
        output = io.StringIO()
        with self.assertRaises(SystemExit) as exit_context:
            with contextlib.redirect_stdout(output):
                media_stack.parse_arguments(arguments)
        self.assertEqual(exit_context.exception.code, 0)
        return output.getvalue()

    def test_help_argument_matches_help_flag(self):
        self.assertEqual(self.help_output(["help"]), self.help_output(["-h"]))

    def test_general_help_describes_every_command(self):
        output = " ".join(self.help_output(["help"]).split())
        for description in (
            "Configure credentials, services, Recyclarr, and automatic startup.",
            "Start the services and synchronize Recyclarr.",
            "Stop and remove the stack containers.",
            "Show the current service status.",
            "Show the selected VPN gateway and its connection status.",
            "List or change the saved values for one service.",
        ):
            self.assertIn(description, output)

    def test_empty_arguments_show_general_help(self):
        self.assertEqual(self.help_output([]), self.help_output(["help"]))

    def test_each_command_has_detailed_help(self):
        expected = {
            "setup": "Configure credentials, start the services",
            "start": "Start the media services",
            "stop": "Stop and remove the media stack containers",
            "status": "Show the current Docker Compose status",
            "vpn-status": "Check the selected VPN gateway health",
            "credentials": "Show the saved values for a service",
        }
        for command, description in expected.items():
            with self.subTest(command=command):
                self.assertIn(description, self.help_output([command, "-h"]))


if __name__ == "__main__":
    unittest.main()
