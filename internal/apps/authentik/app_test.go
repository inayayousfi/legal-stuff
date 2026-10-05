package authentik

import (
	"strings"
	"testing"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/apps/caddy"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

// A new named route appears in the sign-in text without editing Authentik.
func TestCoverageNamesProtectedAndPublicApps(t *testing.T) {
	routes := []app.Route{
		{Name: "Sonarr", Path: "/sonarr"},
		{Name: "Bazarr", Path: "/bazarr"},
		{Name: "Tool", Port: 8000},
		{Upstream: address, Public: true, SignIn: true},
		{Upstream: "homepage:3000", Fallback: true},
		{Name: "Jellyfin", Path: "/jellyfin", Public: true},
	}
	want := "They protect Sonarr, Bazarr, and Tool, which no longer ask for their own logins. Jellyfin keeps its own login."
	if got := coverage(routes); got != want {
		t.Errorf("text = %q", got)
	}
}

func blueprintFor(mode, host string) string {
	saved := settings.NewValues()
	saved.Set("ACCESS_MODE", mode)
	saved.Set("ADMIN_ACCESS_HOST", host)
	caddy.App.Derive(app.ValuesFor(saved, caddy.App))
	saved.Set(passwordKey, "secret-admin-password")
	return Blueprint(app.ValuesFor(saved, App))
}

// The blueprint file reads the admin password from the environment, so the file never holds it.
func TestBlueprintKeepsThePasswordOutOfTheFile(t *testing.T) {
	file := blueprintFor("domain", "media.example.com")
	if strings.Contains(file, "secret-admin-password") || !strings.Contains(file, "password: !Env ADMIN_PASS") {
		t.Errorf("blueprint:\n%s", file)
	}
	for _, part := range []string{`external_host: "https://media.example.com"`, `authentik_host: "https://media.example.com:9091"`} {
		if !strings.Contains(file, part) {
			t.Errorf("missing %s", part)
		}
	}
}

// On an unencrypted address, the sign-in page itself says which methods cannot work.
func TestLocalModeSignInPageNamesTheUnavailableMethods(t *testing.T) {
	if !strings.Contains(blueprintFor("local", "box.local"), "passkeys and Google sign-in are unavailable") {
		t.Error("local sign-in page does not explain the missing methods")
	}
	if strings.Contains(blueprintFor("tailscale", "box.tail.ts.net"), "unavailable") {
		t.Error("an encrypted address claims methods are unavailable")
	}
}

// A changed admin password changes the file, so Authentik applies it at once.
func TestChangedLoginChangesTheBlueprint(t *testing.T) {
	saved := settings.NewValues()
	saved.Set("ACCESS_MODE", "domain")
	saved.Set("ADMIN_ACCESS_HOST", "media.example.com")
	saved.Set("AUTHENTIK_SECRET_KEY", "key")
	saved.Set(userKey, "chief")
	saved.Set(passwordKey, "first")
	v := app.ValuesFor(saved, App)
	before := Blueprint(v)
	v.Set(passwordKey, "second")
	if Blueprint(v) == before {
		t.Error("a new password left the blueprint unchanged")
	}
}

// Renaming the admin deactivates the earlier account; renaming back reactivates it.
func TestRenamedAdminAccountIsDeactivated(t *testing.T) {
	saved := settings.NewValues()
	saved.Set("ACCESS_MODE", "domain")
	saved.Set("ADMIN_ACCESS_HOST", "media.example.com")
	v := app.ValuesFor(saved, App)
	v.Set(userKey, "chief")
	if !trackAdmin(v) || strings.Contains(Blueprint(v), "is_active: false") {
		t.Fatal("the first admin was retired")
	}
	if trackAdmin(v) {
		t.Error("an unchanged admin reported a change")
	}
	v.Set(userKey, "boss")
	trackAdmin(v)
	if !strings.Contains(Blueprint(v), "      username: \"chief\"\n    attrs:\n      is_active: false\n") {
		t.Errorf("old account not deactivated:\n%s", Blueprint(v))
	}
	v.Set(userKey, "chief")
	trackAdmin(v)
	file := Blueprint(v)
	if strings.Contains(file, "username: \"chief\"\n    attrs:\n      is_active: false") || !strings.Contains(file, "username: \"boss\"\n    attrs:\n      is_active: false") {
		t.Errorf("rename back:\n%s", file)
	}
}
