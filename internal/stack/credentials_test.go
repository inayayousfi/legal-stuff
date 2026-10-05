package stack

import (
	"slices"
	"strings"
	"testing"

	"github.com/inayayousfi/legal-stuff/internal/fake"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

func credentialStack(t *testing.T, values map[string]string, answers map[string]string) (*Stack, *fake.UI) {
	t.Helper()
	ui := &fake.UI{Answers: answers}
	s := newTestStack(t, ui, &fake.Shell{})
	saved := settings.NewValues()
	for _, key := range []string{"ADMIN_USER", "ADMIN_PASS", "SONARR_API_KEY", "VPN_GATEWAY_SERVICE", "VPN_SERVICE_PROVIDER", "VPN_OPENVPN_USER", "VPN_OPENVPN_PASSWORD", "TAILSCALE_EXIT_NODE", "TAILSCALE_AUTH_KEY"} {
		if value, ok := values[key]; ok {
			saved.Set(key, value)
		}
	}
	if err := settings.Write(s.envFile(), saved); err != nil {
		t.Fatal(err)
	}
	return s, ui
}

func TestNoGroupListsGroupsWithoutReadingValues(t *testing.T) {
	ui := &fake.UI{}
	s := newTestStack(t, ui, &fake.Shell{})
	if err := s.Credentials(""); err != nil {
		t.Fatal(err)
	}
	want := "say: Choose a group: sonarr, radarr, vpn, access, admin, jellyfin\nPasswords and API keys appear only when you choose a group."
	if !slices.Equal(ui.Events, []string{want}) {
		t.Errorf("events = %q", ui.Events)
	}
}

func TestListingShowsEachValueAndNoKeepsEverything(t *testing.T) {
	s, ui := credentialStack(t, map[string]string{"ADMIN_USER": "admin", "ADMIN_PASS": "secret"}, map[string]string{"change": "no"})
	if err := s.Credentials("admin"); err != nil {
		t.Fatal(err)
	}
	if ui.Screens[0].Body != "Username: admin\nPassword: secret" {
		t.Errorf("listing = %q", ui.Screens[0].Body)
	}
	if len(ui.Screens) != 1 {
		t.Errorf("asked for new values after no: %v", ui.Events)
	}
}

func TestEnterEverywhereChangesNothing(t *testing.T) {
	s, ui := credentialStack(t, map[string]string{"ADMIN_USER": "admin", "ADMIN_PASS": "secret"},
		map[string]string{"change": "yes", "ADMIN_USER": "", "ADMIN_PASS": ""})
	if err := s.Credentials("admin"); err != nil {
		t.Fatal(err)
	}
	if last := ui.Events[len(ui.Events)-1]; last != "say: No changes." {
		t.Errorf("last = %q", last)
	}
}

func TestChangedAPIKeyIsValidatedSavedAndAppliesAtNextStart(t *testing.T) {
	newKey := strings.Repeat("A", 32)
	s, ui := credentialStack(t, map[string]string{"SONARR_API_KEY": strings.Repeat("0", 32)},
		map[string]string{"change": "yes", "SONARR_API_KEY": newKey})
	if err := s.Credentials("sonarr"); err != nil {
		t.Fatal(err)
	}
	if got := readEnv(t, s).Get("SONARR_API_KEY"); got != strings.ToLower(newKey) {
		t.Errorf("saved key = %q", got)
	}
	tail := ui.Events[len(ui.Events)-2:]
	if !slices.Equal(tail, []string{"say: Saved.", "say: The stack uses the changed values after its next start."}) {
		t.Errorf("messages = %q", tail)
	}
	field := ui.Screens[1].Fields[0]
	if _, err := field.Check("not-a-key"); err == nil || err.Error() != "API keys must contain exactly 32 hexadecimal characters." {
		t.Errorf("invalid key error = %v", err)
	}
}

func TestChangedPasswordIsAskedTwice(t *testing.T) {
	s, ui := credentialStack(t, map[string]string{"ADMIN_USER": "admin", "ADMIN_PASS": "secret"},
		map[string]string{"change": "yes", "ADMIN_USER": "", "ADMIN_PASS": "new-secret"})
	if err := s.Credentials("admin"); err != nil {
		t.Fatal(err)
	}
	password := ui.Screens[1].Fields[1]
	if password.Repeat != "Repeat new password" || password.Mismatch != "Values do not match." {
		t.Errorf("password field = %+v", password)
	}
	if readEnv(t, s).Get("ADMIN_PASS") != "new-secret" {
		t.Error("password not saved")
	}
}

func TestVPNShowsFixedSettingsWithoutAskingForThem(t *testing.T) {
	s, ui := credentialStack(t, map[string]string{
		"VPN_GATEWAY_SERVICE": "gluetun", "VPN_SERVICE_PROVIDER": "nordvpn",
		"VPN_OPENVPN_USER": "u", "VPN_OPENVPN_PASSWORD": "p", "TAILSCALE_EXIT_NODE": "stale",
	}, map[string]string{"change": "yes", "VPN_OPENVPN_USER": "", "VPN_OPENVPN_PASSWORD": ""})
	if err := s.Credentials("vpn"); err != nil {
		t.Fatal(err)
	}
	if ui.Screens[0].Body != "Provider: nordvpn\nGateway: gluetun\nService username: u\nService password: p" {
		t.Errorf("listing = %q", ui.Screens[0].Body)
	}
	var keys []string
	for _, field := range ui.Screens[1].Fields {
		keys = append(keys, field.Key)
	}
	if !slices.Equal(keys, []string{"VPN_OPENVPN_USER", "VPN_OPENVPN_PASSWORD"}) {
		t.Errorf("asked %v", keys)
	}
}

func TestTailscaleVPNShowsOnlyTailscaleValues(t *testing.T) {
	s, ui := credentialStack(t, map[string]string{
		"VPN_GATEWAY_SERVICE": "tailscale-vpn", "VPN_OPENVPN_USER": "stale",
		"TAILSCALE_EXIT_NODE": "exit", "TAILSCALE_AUTH_KEY": "tskey",
	}, map[string]string{"change": "no"})
	if err := s.Credentials("vpn"); err != nil {
		t.Fatal(err)
	}
	if ui.Screens[0].Body != "Gateway: tailscale-vpn\nExit node: exit\nAuth key: tskey" {
		t.Errorf("listing = %q", ui.Screens[0].Body)
	}
}

func TestUnconfiguredGroupReportsNoSavedValues(t *testing.T) {
	s, ui := credentialStack(t, map[string]string{}, nil)
	if err := s.Credentials("jellyfin"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ui.Events, []string{"say: No saved values for jellyfin."}) {
		t.Errorf("events = %q", ui.Events)
	}
}

func TestCredentialsWithoutSetupAsksForSetup(t *testing.T) {
	s := newTestStack(t, &fake.UI{}, &fake.Shell{})
	if err := s.Credentials("admin"); err == nil || err.Error() != "Run 'selfhost setup' first." {
		t.Errorf("err = %v", err)
	}
}
