import tempfile
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request
from http.server import ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

import vpn_country


class VpnCountryPageTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        data = Path(self.directory.name)
        (data / "countries.json").write_text('["Belgium", "France"]')
        self.patches = [
            patch.object(vpn_country, "DATA_DIR", data),
            patch.object(vpn_country, "COUNTRIES_FILE", data / "countries.json"),
            patch.object(vpn_country, "SELECTION_FILE", data / "selection.json"),
        ]
        for item in self.patches:
            item.start()
        self.gluetun_calls = []

        def fake_gluetun(method, path, body=None):
            self.gluetun_calls.append((method, path, body))
            if path == "/v1/publicip/ip":
                return {"public_ip": "203.0.113.5", "country": "Belgium"}
            return {"outcome": "ok"}

        self.gluetun_patch = patch.object(vpn_country, "gluetun", side_effect=fake_gluetun)
        self.gluetun_patch.start()
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), vpn_country.Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}/"

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.gluetun_patch.stop()
        for item in self.patches:
            item.stop()
        self.directory.cleanup()

    def post(self, country):
        data = urllib.parse.urlencode({"country": country}).encode()
        return urllib.request.urlopen(urllib.request.Request(self.url, data=data), timeout=5)

    def test_page_lists_countries_and_current_address(self):
        page = urllib.request.urlopen(self.url, timeout=5).read().decode()
        self.assertIn('<option value="">Any country</option>', page)
        self.assertIn('<option value="France">France</option>', page)
        self.assertIn("203.0.113.5 (Belgium)", page)

    def test_missing_address_is_shown_as_reconnecting(self):
        with patch.object(vpn_country, "gluetun", return_value={"public_ip": ""}):
            self.assertIn("Reconnecting", vpn_country.render_page())

    def test_selected_country_is_applied_then_saved(self):
        with self.post("France") as response:
            self.assertEqual(response.status, 200)
        self.assertIn(
            ("PUT", "/v1/vpn/settings", {"provider": {"server_selection": {"countries": ["France"]}}}),
            self.gluetun_calls,
        )
        self.assertEqual(vpn_country.saved_countries(), ["France"])
        self.assertTrue(vpn_country.SELECTION_FILE.exists())

    def test_any_country_clears_the_selection(self):
        with self.post(""):
            pass
        self.assertIn(
            ("PUT", "/v1/vpn/settings", {"provider": {"server_selection": {"countries": []}}}),
            self.gluetun_calls,
        )
        self.assertEqual(vpn_country.saved_countries(), [])

    def test_unknown_country_changes_nothing(self):
        with self.assertRaises(urllib.error.HTTPError) as error:
            self.post("Atlantis")
        self.assertEqual(error.exception.code, 400)
        self.assertFalse(any(call[0] == "PUT" for call in self.gluetun_calls))
        self.assertFalse(vpn_country.SELECTION_FILE.exists())


class ReapplyTests(unittest.TestCase):
    def test_saved_country_is_reapplied_only_when_gluetun_differs(self):
        with tempfile.TemporaryDirectory() as directory:
            selection = Path(directory) / "selection.json"
            with patch.object(vpn_country, "SELECTION_FILE", selection), \
                 patch.object(vpn_country, "apply_countries") as apply:
                with patch.object(vpn_country, "current_countries", return_value=[]):
                    vpn_country.reapply_saved_selection()
                apply.assert_not_called()

                selection.write_text('{"countries": ["France"]}')
                with patch.object(vpn_country, "current_countries", return_value=["France"]):
                    vpn_country.reapply_saved_selection()
                apply.assert_not_called()

                with patch.object(vpn_country, "current_countries", return_value=[]):
                    vpn_country.reapply_saved_selection()
                apply.assert_called_once_with(["France"])


if __name__ == "__main__":
    unittest.main()
