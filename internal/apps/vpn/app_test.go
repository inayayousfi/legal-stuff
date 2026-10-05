package vpn

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inayayousfi/legal-stuff/internal/fake"
	"github.com/inayayousfi/legal-stuff/internal/settings"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

func TestCountriesKeepUniqueOpenVPNCountries(t *testing.T) {
	servers := []map[string]any{
		{"vpn": "openvpn", "country": "Spain"},
		{"vpn": "openvpn", "country": "Austria"},
		{"vpn": "openvpn", "country": "Spain"},
		{"vpn": "wireguard", "country": "Chile"},
		{"vpn": "openvpn"},
	}
	if got := Countries(servers); !slices.Equal(got, []string{"Austria", "Spain"}) {
		t.Errorf("countries = %v", got)
	}
}

func TestSavedCountryIsCopiedIntoValues(t *testing.T) {
	values := settings.NewValues()
	values.Set("CONFIG_DIR", t.TempDir())
	values.Set(gatewayKey, Gluetun)
	path := filepath.Join(values.Get("CONFIG_DIR"), countryPage, "selection.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"countries": ["Spain", 3]}`), 0o644)
	if !ApplySavedCountry(values) || values.Get("VPN_SERVER_COUNTRIES") != "Spain" {
		t.Errorf("countries = %q", values.Get("VPN_SERVER_COUNTRIES"))
	}
	if ApplySavedCountry(values) {
		t.Error("an unchanged country was reported as changed")
	}
	values.Set(gatewayKey, Tailscale)
	os.WriteFile(path, []byte(`{"countries": ["Chile"]}`), 0o644)
	if ApplySavedCountry(values) {
		t.Error("a country was applied to the Tailscale gateway")
	}
}

func TestGluetunAccessFileReportsOnlyRealChanges(t *testing.T) {
	dir := t.TempDir()
	values := settings.NewValues()
	values.Set("GLUETUN_API_KEY", "read")
	values.Set("GLUETUN_CONTROL_KEY", "control")
	if changed, _ := WriteGluetunAuth(dir, values); !changed {
		t.Error("first write not reported")
	}
	if changed, _ := WriteGluetunAuth(dir, values); changed {
		t.Error("unchanged file reported as changed")
	}
	content, _ := os.ReadFile(filepath.Join(dir, "gluetun", "auth", "config.toml"))
	want := `[[roles]]
name = "homepage"
routes = ["GET /v1/publicip/ip"]
auth = "apikey"
apikey = "read"

[[roles]]
name = "vpn-country"
routes = ["GET /v1/publicip/ip", "GET /v1/vpn/settings", "PUT /v1/vpn/settings"]
auth = "apikey"
apikey = "control"
`
	if string(content) != want {
		t.Errorf("content =\n%s", content)
	}
}

func TestValidationDependsOnTheGateway(t *testing.T) {
	values := settings.NewValues()
	values.Set(gatewayKey, Tailscale)
	values.Set("TAILSCALE_AUTH_KEY", "key")
	if err := validate(values); err == nil || err.Error() != "Missing required VPN values: TAILSCALE_EXIT_NODE" {
		t.Errorf("err = %v", err)
	}
	values.Set("TAILSCALE_EXIT_NODE", "exit")
	if err := validate(values); err != nil {
		t.Errorf("complete Tailscale values rejected: %v", err)
	}
	values.Set(gatewayKey, Gluetun)
	values.Set("VPN_TYPE", "wireguard")
	if err := validate(values); err == nil || !strings.Contains(err.Error(), "OpenVPN only") {
		t.Errorf("err = %v", err)
	}
}

func TestTailscaleGatewayRunsWithoutTheCountryPage(t *testing.T) {
	values := settings.NewValues()
	values.Set(gatewayKey, Tailscale)
	if got := App.Services(values); !slices.Equal(got, []string{Tailscale}) {
		t.Errorf("services = %v", got)
	}
	if routes := App.Routes(values); len(routes) != 0 {
		t.Errorf("routes = %v", routes)
	}
	if tiles := App.Tiles(values); len(tiles) != 1 || strings.Contains(tiles[0].YAML, "gluetun") {
		t.Errorf("tiles = %v", tiles)
	}
}

func TestWaitHasNoTimeLimitAndReportsOnce(t *testing.T) {
	Sleep = func(time.Duration) {}
	calls := 0
	sh := &fake.Shell{Respond: func([]string) shell.Result {
		calls++
		if calls < 50 {
			return shell.Result{Stdout: "starting"}
		}
		return shell.Result{Stdout: "healthy"}
	}}
	values := settings.NewValues()
	values.Set(gatewayKey, Gluetun)
	var said []string
	WaitUntilReady(sh, values, func(s string) { said = append(said, s) })
	if calls != 50 || len(said) != 1 {
		t.Errorf("calls = %d, messages = %v", calls, said)
	}
	said = nil
	WaitUntilReady(sh, values, func(s string) { said = append(said, s) })
	if len(said) != 0 {
		t.Errorf("a ready gateway printed %v", said)
	}
}

func TestTailscaleStatusReportsAnOfflineExitNode(t *testing.T) {
	values := settings.NewValues()
	values.Set(gatewayKey, Tailscale)
	values.Set("TAILSCALE_EXIT_NODE", "exit-1")
	sh := &fake.Shell{Respond: func([]string) shell.Result {
		return shell.Result{Stdout: `{"ExitNodeStatus": {"Online": false}}`}
	}}
	text, err := Status(sh, values)
	if err != nil || text != "Tailscale exit node: not online\nSelected exit node:\nexit-1" {
		t.Errorf("text = %q, err = %v", text, err)
	}
}
