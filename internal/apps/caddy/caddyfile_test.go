package caddy_test

import (
	"strings"
	"testing"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/apps"
	"github.com/inayayousfi/legal-stuff/internal/apps/caddy"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

func caddyfile(t *testing.T, mode, host, gateway string) string {
	t.Helper()
	saved := settings.NewValues()
	saved.Set("ACCESS_MODE", mode)
	saved.Set("ADMIN_ACCESS_HOST", host)
	saved.Set("VPN_GATEWAY_SERVICE", gateway)
	var routes []app.Route
	for _, a := range apps.All {
		if a.Derive != nil {
			a.Derive(app.ValuesFor(saved, a))
		}
	}
	for _, a := range apps.All {
		if a.Routes != nil {
			routes = append(routes, a.Routes(app.ValuesFor(saved, a))...)
		}
	}
	file, err := caddy.Caddyfile(app.ValuesFor(saved, caddy.App), routes)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// guarded reports whether the block that forwards to upstream checks the sign-in first.
func guarded(t *testing.T, file, upstream string) bool {
	t.Helper()
	end := strings.Index(file, "reverse_proxy "+upstream+"\n")
	if end < 0 {
		t.Fatalf("no route to %s:\n%s", upstream, file)
	}
	block := file[strings.LastIndex(file[:end], "handle"):end]
	return strings.Contains(block, "forward_auth authentik-server:9000")
}

func TestJellyfinAndHomepageStayOutsideTheSignInAndAdminPagesInside(t *testing.T) {
	file := caddyfile(t, "domain", "media.example.com", "gluetun")
	for _, upstream := range []string{"jellyfin-app:8096", "homepage:3000"} {
		if guarded(t, file, upstream) {
			t.Errorf("%s is behind the sign-in", upstream)
		}
	}
	for _, upstream := range []string{"sonarr-app:8989", "radarr-app:7878", "gluetun:9696", "gluetun:8080", "vpn-country:8090"} {
		if !guarded(t, file, upstream) {
			t.Errorf("%s is not behind the sign-in", upstream)
		}
	}
}

func TestSeerrAndSignInHaveTheirOwnPorts(t *testing.T) {
	file := caddyfile(t, "domain", "media.example.com", "gluetun")
	for _, part := range []string{
		"redir @seerr https://media.example.com:5055/",
		"media.example.com:5055 {\n\treverse_proxy seerr:5055\n}",
		"media.example.com:9091 {\n\treverse_proxy authentik-server:9000\n}",
	} {
		if !strings.Contains(file, part) {
			t.Errorf("missing %q", part)
		}
	}
}

func TestQBittorrentUsesTheSelectedGatewayAndCountryPageNeedsGluetun(t *testing.T) {
	file := caddyfile(t, "local", "box.local", "tailscale-vpn")
	if !strings.Contains(file, "reverse_proxy tailscale-vpn:8080") || strings.Contains(file, "vpn-country") {
		t.Errorf("Caddyfile:\n%s", file)
	}
	if !strings.HasPrefix(strings.SplitN(file, "\n", 4)[2], "http://box.local {") {
		t.Errorf("local mode is not served over plain HTTP:\n%s", file)
	}
}

func TestTailscaleModeUsesTheTailscaleCertificate(t *testing.T) {
	file := caddyfile(t, "tailscale", "box.tail.ts.net", "gluetun")
	if strings.Count(file, "tls /certs/cert.pem /certs/key.pem") != 3 {
		t.Errorf("each site must use the certificate:\n%s", file)
	}
}

func TestInternalNamesAddTheirPathOnlyWhenMissing(t *testing.T) {
	file := caddyfile(t, "domain", "media.example.com", "gluetun")
	want := "http://:8989 {\n\t@missing not path /sonarr /sonarr/*\n\trewrite @missing /sonarr{uri}\n\treverse_proxy sonarr-app:8989\n}"
	if !strings.Contains(file, want) {
		t.Errorf("missing internal Sonarr address:\n%s", file)
	}
}

// Admin pages must not be served without a sign-in check.
func TestProtectedRoutesNeedASignInRoute(t *testing.T) {
	saved := settings.NewValues()
	saved.Set("ACCESS_MODE", "domain")
	saved.Set("ADMIN_ACCESS_HOST", "media.example.com")
	_, err := caddy.Caddyfile(app.ValuesFor(saved, caddy.App), []app.Route{{Path: "/sonarr", Upstream: "sonarr-app:8989"}})
	if err == nil {
		t.Error("a protected route was served without a sign-in check")
	}
}

func TestAccessAddressMustBeAName(t *testing.T) {
	for _, bad := range []string{"localhost", "192.168.1.10", "media example.com"} {
		if _, err := caddy.ValidHost(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if host, err := caddy.ValidHost(" Media.Example.COM. "); err != nil || host != "media.example.com" {
		t.Errorf("host = %q, err = %v", host, err)
	}
}

// An app served on its own port is still behind the sign-in unless it is public.
func TestOwnPortRoutesKeepTheSignIn(t *testing.T) {
	saved := settings.NewValues()
	saved.Set("ACCESS_MODE", "domain")
	saved.Set("ADMIN_ACCESS_HOST", "media.example.com")
	file, err := caddy.Caddyfile(app.ValuesFor(saved, caddy.App), []app.Route{
		{Upstream: "authentik-server:9000", Public: true, SignIn: true, Check: "/outpost.goauthentik.io/auth/caddy", Port: 9091},
		{Path: "/tool", Upstream: "tool:8000", Port: 8000},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "media.example.com:8000 {\n\thandle {\n\t\tforward_auth authentik-server:9000 {\n\t\t\turi /outpost.goauthentik.io/auth/caddy\n\t\t\ttrusted_proxies private_ranges\n\t\t}\n\t\treverse_proxy tool:8000\n\t}\n}"
	if !strings.Contains(file, want) {
		t.Errorf("Caddyfile:\n%s", file)
	}
}
