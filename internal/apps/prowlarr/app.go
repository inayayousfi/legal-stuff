// Package prowlarr manages indexers. It runs inside the VPN gateway's network.
package prowlarr

import (
	"embed"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/apps/caddy"
	"github.com/inayayousfi/legal-stuff/internal/apps/radarr"
	"github.com/inayayousfi/legal-stuff/internal/apps/sonarr"
	"github.com/inayayousfi/legal-stuff/internal/apps/vpn"
	"github.com/inayayousfi/legal-stuff/internal/flow"
)

//go:embed compose.yaml
var compose embed.FS

var App = &app.App{
	Name:       "prowlarr",
	Compose:    compose,
	Services:   func(app.Values) []string { return []string{"prowlarr"} },
	BehindVPN:  []string{"prowlarr"},
	ConfigDirs: []string{"prowlarr"},
	Setup: func(e *app.Env) error {
		return e.Step("prowlarr", false, func() error {
			return flow.Show(e.UI, Guide(caddy.URL(e.Values), sonarr.APIKey(e.Values), radarr.APIKey(e.Values)))
		})
	},
	Routes: func(v app.Values) []app.Route {
		return []app.Route{{
			Name:     "Prowlarr",
			Path:     "/prowlarr",
			Upstream: vpn.Gateway(v) + ":9696",
			Internal: &app.Internal{Name: "prowlarr", Port: 9696},
		}}
	},
	Tiles: func(app.Values) []app.Tile {
		return []app.Tile{{Group: "Admin", Position: 4, YAML: `    - Prowlarr:
        icon: prowlarr.png
        server: media-stack
        container: prowlarr
        href: "{{HOMEPAGE_VAR_URL}}/prowlarr"
        description: Manage indexers
`}}
	},
}

// Guide connects Prowlarr to Sonarr and Radarr with their saved API keys.
func Guide(accessURL, sonarrKey, radarrKey string) flow.Screen {
	return flow.Screen{
		Title: "Prowlarr setup",
		Body: `Open this link:
` + accessURL + `/prowlarr

1. Open Settings > Apps.
2. Click Add, then select Sonarr.
3. Set Sync Level to Full Sync.
4. Prowlarr Server: http://prowlarr:9696
5. Sonarr Server: http://sonarr:8989
6. API Key: ` + sonarrKey + `
7. Click Test, then Save.
8. Click Add, then select Radarr.
9. Set Sync Level to Full Sync.
10. Prowlarr Server: http://prowlarr:9696
11. Radarr Server: http://radarr:7878
12. API Key: ` + radarrKey + `
13. Click Test, then Save.
14. Open Indexers, add your indexers, then test each one.`,
		Wait: "Prowlarr",
	}
}
