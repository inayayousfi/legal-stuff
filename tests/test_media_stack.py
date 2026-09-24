import contextlib
import io
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

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
                "QBIT_USER": "admin",
                "QBIT_PASS": "qbit-secret",
                "JELLYFIN_USER": "jellyfin-admin",
                "JELLYFIN_PASS": "jellyfin-secret",
                "SONARR_USER": "admin",
                "SONARR_PASS": "sonarr-secret",
                "SONARR_API_KEY": "0" * 32,
                "RADARR_USER": "admin",
                "RADARR_PASS": "radarr-secret",
                "RADARR_API_KEY": "1" * 32,
                "PROWLARR_USER": "admin",
                "PROWLARR_PASS": "prowlarr-secret",
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
            media_stack.write_env(path, {"MEDIA_DIR": "/media", "QBIT_USER": "admin"})
            self.assertEqual(
                media_stack.read_env_values(path),
                {"MEDIA_DIR": "/media", "QBIT_USER": "admin"},
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
            media_stack.copy_homepage_config(config_dir)
            homepage_dir = config_dir / "homepage"
            for filename in ("services.yaml", "settings.yaml", "widgets.yaml"):
                self.assertEqual(
                    (homepage_dir / filename).read_bytes(),
                    (media_stack.HOMEPAGE_TEMPLATE_DIR / filename).read_bytes(),
                )

    def test_dashboard_links_use_the_detected_host(self):
        services = (media_stack.HOMEPAGE_TEMPLATE_DIR / "services.yaml").read_text(
            encoding="utf-8"
        )
        self.assertEqual(services.count("{{HOMEPAGE_VAR_HOST}}"), 5)
        for port in (8096, 8080, 8989, 7878, 9696):
            self.assertIn(f":{port}", services)

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

    def test_temporary_qbittorrent_password_is_extracted(self):
        logs = (
            "The WebUI administrator password was not set. "
            "A temporary password is provided for this session: AbC123xy"
        )
        self.assertEqual(
            media_stack.temporary_qbittorrent_password(logs), "AbC123xy"
        )

    def test_missing_temporary_qbittorrent_password_is_rejected(self):
        with self.assertRaisesRegex(media_stack.StackError, "did not publish"):
            media_stack.temporary_qbittorrent_password("qBittorrent started")


class SetupGuideTests(unittest.TestCase):
    def guide_output(self, printer, *arguments):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            printer(*arguments)
        return output.getvalue()

    def test_qbittorrent_guide_contains_login_and_ui_path(self):
        output = self.guide_output(media_stack.print_qbittorrent_guide, "Temporary123")
        self.assertIn("http://localhost:8080", output)
        self.assertIn("Temporary password: Temporary123", output)
        self.assertEqual(output.count("http://localhost:8080"), 1)
        self.assertIn("Tools > Options > Web UI", output)
        self.assertIn("replace the temporary login", output)
        self.assertIn("requested after these steps", output)
        self.assertIn("/media/Downloads", output)
        self.assertIn("Authentication", output)
        self.assertNotIn("QBIT_USER", output)
        self.assertNotIn("QBIT_PASS", output)

    def test_service_guides_contain_urls_and_media_paths(self):
        guides = (
            (media_stack.print_sonarr_guide, "http://localhost:8989", "/media/Series"),
            (media_stack.print_radarr_guide, "http://localhost:7878", "/media/Movies"),
            (media_stack.print_prowlarr_guide, "http://localhost:9696", "http://sonarr:8989"),
            (media_stack.print_jellyfin_guide, "http://localhost:8096", "/media/Movies"),
        )
        for printer, url, path in guides:
            with self.subTest(url=url):
                output = self.guide_output(printer)
                self.assertIn(url, output)
                self.assertIn(path, output)

    def test_jellyfin_guide_explains_login_and_missing_media(self):
        output = self.guide_output(media_stack.print_jellyfin_guide)
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
                output = self.guide_output(printer)
                self.assertNotIn("_USER", output)
                self.assertNotIn("_PASS", output)
                self.assertNotIn("_API_KEY", output)

    def test_sonarr_and_radarr_guides_print_saved_qbittorrent_login(self):
        for printer in (
            media_stack.print_sonarr_guide,
            media_stack.print_radarr_guide,
        ):
            with self.subTest(printer=printer.__name__):
                output = self.guide_output(
                    printer, "media.example.ts.net", "saved-user", "saved-password"
                )
                self.assertIn("Username: saved-user", output)
                self.assertIn("Password: saved-password", output)
                self.assertIn("generated automatically", output)

    def test_prowlarr_guide_prints_saved_api_keys(self):
        output = self.guide_output(
            media_stack.print_prowlarr_guide,
            "media.example.ts.net",
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

    def test_service_guides_use_remote_host(self):
        host = "media-host.example.ts.net"
        output = self.guide_output(media_stack.print_qbittorrent_guide, "secret", host)
        self.assertIn(f"http://{host}:8080", output)
        self.assertEqual(output.count(f"http://{host}:8080"), 1)
        self.assertIn("Temporary username: admin", output)
        self.assertIn("Temporary password: secret", output)

    @patch("media_stack.prompt_password", return_value="secret")
    @patch("media_stack.prompt_username", return_value="admin")
    def test_credentials_can_be_requested_one_service_at_a_time(
        self, prompt_username, prompt_password
    ):
        values = {}
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.ensure_credentials(values, ("QBIT",))
        self.assertEqual(values, {"QBIT_USER": "admin", "QBIT_PASS": "secret"})
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
        media_stack.ensure_credentials(values, ("JELLYFIN",))
        self.assertEqual(
            values,
            {"JELLYFIN_USER": "jellyfin-admin", "JELLYFIN_PASS": "jellyfin-secret"},
        )
        media_stack.ensure_credentials(values, ("JELLYFIN",))
        prompt_username.assert_called_once_with("Jellyfin")
        prompt_password.assert_called_once_with("Jellyfin")

    def test_ipv6_service_url_uses_brackets(self):
        self.assertEqual(
            media_stack.service_url("fd7a:115c:a1e0::1", 8080),
            "http://[fd7a:115c:a1e0::1]:8080",
        )

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
                "ADMIN_ACCESS_HOST": "media.example.ts.net",
                "SONARR_API_KEY": "a" * 32,
                "RADARR_API_KEY": "b" * 32,
            }
            completed = {"qbittorrent", "sonarr", "radarr", "prowlarr"}

            def check_saved(service):
                self.assertEqual(service, "Jellyfin")
                self.assertIn('JELLYFIN_PASS="secret"', env_path.read_text())

            with patch.object(media_stack, "ENV_FILE", env_path), \
                 patch("media_stack.read_env_values", return_value=existing), \
                 patch("media_stack.restore_windows_console_input"), \
                 patch("media_stack.wait_for_docker"), \
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
                 patch("media_stack.complete_setup_step"), \
                 patch("media_stack.sync_recyclarr"), \
                 patch("media_stack.install_autostart"), \
                 contextlib.redirect_stdout(output := io.StringIO()):
                media_stack.setup()
            self.assertIn('JELLYFIN_USER="admin"', env_path.read_text())
            self.assertIn("Open Homepage:\nhttp://media.example.ts.net:3000\n", output.getvalue())
            self.assertEqual(output.getvalue().count("http://media.example.ts.net:3000"), 1)


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
        self.assertIn('"8080:8080/tcp"', gluetun)
        self.assertIn('FIREWALL_INPUT_PORTS: "8080,6881"', gluetun)
        self.assertIn('"8080:8080/tcp"', tailscale)
        self.assertIn("--exit-node=${TAILSCALE_EXIT_NODE:-}", tailscale)

    @patch("media_stack.wait_for_vpn_gateway")
    @patch("media_stack.compose")
    def test_gateway_is_ready_before_qbittorrent_starts(self, compose, wait):
        values = {"VPN_GATEWAY_SERVICE": "gluetun"}
        media_stack.start_core_services(values)
        self.assertEqual(
            compose.call_args_list[0].args,
            ("stop", "qbittorrent", "tailscale-vpn"),
        )
        self.assertEqual(compose.call_args_list[1].args, ("up", "-d", "gluetun"))
        wait.assert_called_once_with(values)
        self.assertEqual(compose.call_args_list[2].args[:2], ("up", "-d"))
        self.assertIn("qbittorrent", compose.call_args_list[2].args)

    @patch("media_stack.shutil.which", return_value="C:/Tailscale/tailscale.exe")
    @patch("media_stack.run")
    def test_tailscale_dns_name_is_used_for_remote_access(self, run, _which):
        run.return_value.returncode = 0
        run.return_value.stdout = '{"Self":{"DNSName":"media.example.ts.net."}}'
        self.assertEqual(
            media_stack.discover_access_host(), "media.example.ts.net"
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


class ShowSavedTests(unittest.TestCase):
    def test_no_section_lists_choices_without_reading_credentials(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            media_stack.show_saved()
        self.assertIn("jellyfin", output.getvalue())
        self.assertIn("vpn", output.getvalue())
        self.assertNotIn("secret", output.getvalue())

    def test_selected_service_prints_saved_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            media_stack.write_env(path, {"JELLYFIN_USER": "admin", "JELLYFIN_PASS": "secret$!"})
            output = io.StringIO()
            with patch.object(media_stack, "ENV_FILE", path), \
                 contextlib.redirect_stdout(output):
                self.assertEqual(media_stack.main(["show", "jellyfin"]), 0)
            self.assertEqual(output.getvalue(), "Username:\nadmin\nPassword:\nsecret$!\n")

    def test_vpn_ignores_credentials_for_inactive_gateway(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            media_stack.write_env(path, {
                "VPN_GATEWAY_SERVICE": "tailscale-vpn",
                "TAILSCALE_EXIT_NODE": "node-one",
                "TAILSCALE_AUTH_KEY": "tskey-secret",
                "VPN_OPENVPN_PASSWORD": "old-vpn-password",
            })
            output = io.StringIO()
            with patch.object(media_stack, "ENV_FILE", path), \
                 contextlib.redirect_stdout(output):
                media_stack.show_saved("vpn")
            self.assertIn("node-one", output.getvalue())
            self.assertIn("tskey-secret", output.getvalue())
            self.assertNotIn("old-vpn-password", output.getvalue())

    def test_unconfigured_service_reports_no_saved_values(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            media_stack.write_env(path, {"MEDIA_DIR": directory})
            output = io.StringIO()
            with patch.object(media_stack, "ENV_FILE", path), \
                 contextlib.redirect_stdout(output):
                media_stack.show_saved("jellyfin")
            self.assertEqual(output.getvalue(), "No saved values for jellyfin.\n")

    def test_missing_setup_returns_error(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(media_stack, "ENV_FILE", Path(directory) / ".env"), \
                 contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(media_stack.main(["show", "jellyfin"]), 1)


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
            "Show saved settings for one service.",
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
            "show": "Show saved settings and credentials",
        }
        for command, description in expected.items():
            with self.subTest(command=command):
                self.assertIn(description, self.help_output([command, "-h"]))


if __name__ == "__main__":
    unittest.main()
