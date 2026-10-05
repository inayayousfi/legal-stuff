// Package sonarr manages series.
package sonarr

import (
	"embed"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/apps/caddy"
	"github.com/inayayousfi/legal-stuff/internal/flow"
)

//go:embed compose.yaml
var compose embed.FS

const keyName = "SONARR_API_KEY"

var App = &app.App{
	Name:       "sonarr",
	Settings:   []app.Setting{{Key: keyName, Example: "replace_with_your_32_character_api_key", Required: true}},
	Compose:    compose,
	Services:   func(app.Values) []string { return []string{"sonarr-app"} },
	ConfigDirs: []string{"sonarr"},
	MediaDirs:  []string{"Series"},
	Validate: func(v app.Values) error {
		_, err := flow.ValidAPIKey(v.Get(keyName))
		return err
	},
	Setup: func(e *app.Env) error {
		return e.Step("sonarr", !e.Values.Has(keyName), func() error {
			answers, err := e.UI.Ask(Guide(caddy.URL(e.Values)))
			if err != nil {
				return err
			}
			e.Values.Set(keyName, answers[keyName])
			return e.SaveValues()
		})
	},
	Routes: func(app.Values) []app.Route {
		return []app.Route{{
			Name:     "Sonarr",
			Path:     "/sonarr",
			Upstream: "sonarr-app:8989",
			Internal: &app.Internal{Name: "sonarr", Port: 8989},
		}}
	},
	Tiles: func(app.Values) []app.Tile {
		return []app.Tile{{Group: "Admin", Position: 2, YAML: `    - Sonarr:
        icon: sonarr.png
        server: media-stack
        container: sonarr-app
        href: "{{HOMEPAGE_VAR_URL}}/sonarr"
        description: Manage series
`}}
	},
	Credentials: &app.CredentialGroup{Name: "sonarr", Fields: []app.CredentialField{
		{Label: "API key", Key: keyName, Kind: app.APIKey, UsedByStack: true},
	}},
}

// APIKey is the key Sonarr generated, which other apps use to reach it.
func APIKey(v app.Values) string { return v.Get(keyName) }

// Guide connects Sonarr to the media folder and qBittorrent, then asks for its API key.
func Guide(accessURL string) flow.Screen {
	return flow.Screen{
		Title: "Sonarr setup",
		Body: `Open this link:
` + accessURL + `/sonarr

1. Open Settings > Media Management.
2. Under Root Folders, click Add Root Folder.
3. Select /media/Series and save it.
4. Open Settings > Download Clients.
5. Click Add, then select qBittorrent.
6. Set Host to qbittorrent and Port to 8080. Leave Username and Password empty.
7. Set Category to sonarr.
8. Click Test, then Save.
9. Open Settings > General > Security.
10. Copy the API Key that Sonarr generated automatically. The next terminal prompt will ask for it.`,
		Wait:   "Sonarr",
		Fields: []flow.Field{flow.APIKeyField(keyName, "Sonarr")},
	}
}
