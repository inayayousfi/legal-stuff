package vpn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/settings"
	"github.com/inayayousfi/selfnook/internal/shell"
)

func newValues() app.Values { return app.ValuesFor(settings.NewValues(), App) }

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
	values := newValues()
	configDir := t.TempDir()
	values.Set(gatewayKey, Gluetun)
	path := filepath.Join(configDir, countryPage, "selection.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"countries": ["Spain", 3]}`), 0o644)
	if !ApplySavedCountry(values, configDir) || values.Get("VPN_SERVER_COUNTRIES") != "Spain" {
		t.Errorf("countries = %q", values.Get("VPN_SERVER_COUNTRIES"))
	}
	if ApplySavedCountry(values, configDir) {
		t.Error("an unchanged country was reported as changed")
	}
	values.Set(gatewayKey, Tailscale)
	os.WriteFile(path, []byte(`{"countries": ["Chile"]}`), 0o644)
	if ApplySavedCountry(values, configDir) {
		t.Error("a country was applied to the Tailscale gateway")
	}
}

func TestGluetunAccessFileReportsOnlyRealChanges(t *testing.T) {
	dir := t.TempDir()
	values := newValues()
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
	values := newValues()
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
	values := newValues()
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

func waitEnv(sh shell.Shell, values app.Values, ui *fake.UI) *app.Env {
	return &app.Env{Values: values, Shell: sh, UI: ui, Sleep: func(time.Duration) {}}
}

func TestWaitHasNoTimeLimitAndReportsOnce(t *testing.T) {
	calls := 0
	sh := &fake.Shell{Respond: func([]string) shell.Result {
		calls++
		if calls < 50 {
			return shell.Result{Stdout: "starting"}
		}
		return shell.Result{Stdout: "healthy"}
	}}
	values := newValues()
	values.Set(gatewayKey, Gluetun)
	ui := &fake.UI{}
	if err := WaitUntilReady(waitEnv(sh, values, ui)); err != nil || calls != 50 || len(ui.Events) != 1 {
		t.Errorf("err = %v, calls = %d, messages = %v", err, calls, ui.Events)
	}
	ui.Events = nil
	WaitUntilReady(waitEnv(sh, values, ui))
	if len(ui.Events) != 0 {
		t.Errorf("a ready gateway printed %v", ui.Events)
	}
}

// A cancelled command ends the wait instead of polling forever.
func TestWaitStopsWhenACommandCannotRun(t *testing.T) {
	values := newValues()
	values.Set(gatewayKey, Gluetun)
	sh := failingShell{}
	if err := WaitUntilReady(waitEnv(sh, values, &fake.UI{})); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

type failingShell struct{}

func (failingShell) Run(shell.Cmd) (shell.Result, error) { return shell.Result{}, context.Canceled }

func TestTailscaleStatusReportsAnOfflineExitNode(t *testing.T) {
	values := newValues()
	values.Set(gatewayKey, Tailscale)
	values.Set("TAILSCALE_EXIT_NODE", "exit-1")
	sh := &fake.Shell{Respond: func([]string) shell.Result {
		return shell.Result{Stdout: `{"ExitNodeStatus": {"Online": false}}`}
	}}
	text, err := Status(&app.Env{Values: values, Shell: sh})
	if err != nil || text != "Tailscale exit node: not online\nSelected exit node:\nexit-1" {
		t.Errorf("text = %q, err = %v", text, err)
	}
}
