// Package radarr manages movies.
package radarr

import (
	"embed"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/apps/caddy"
	"github.com/inayayousfi/selfnook/internal/flow"
)

//go:embed compose.yaml
var compose embed.FS

const keyName = "RADARR_API_KEY"

var App = &app.App{
	Name:       "radarr",
	Settings:   []app.Setting{{Key: keyName, Example: "replace_with_your_32_character_api_key", Required: true}},
	Compose:    compose,
	Services:   func(app.Values) []string { return []string{"radarr-app"} },
	ConfigDirs: []string{"radarr"},
	MediaDirs:  []string{"Movies"},
	Validate: func(v app.Values) error {
		_, err := flow.ValidAPIKey(v.Get(keyName))
		return err
	},
	Setup: func(e *app.Env) error {
		return e.Step("radarr", !e.Values.Has(keyName), func() error {
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
			Name:     "Radarr",
			Path:     "/radarr",
			Upstream: "radarr-app:7878",
			Internal: &app.Internal{Name: "radarr", Port: 7878},
		}}
	},
	Tiles: func(app.Values) []app.Tile {
		return []app.Tile{{Group: "Admin", Position: 3, YAML: `    - Radarr:
        icon: radarr.png
        server: selfnook
        container: selfnook-radarr-app
        href: "{{HOMEPAGE_VAR_URL}}/radarr"
        description: Manage movies
`}}
	},
	Credentials: &app.CredentialGroup{Name: "radarr", Fields: []app.CredentialField{
		{Label: "API key", Key: keyName, Kind: app.APIKey, UsedByStack: true},
	}},
}

// APIKey is the key Radarr generated, which other apps use to reach it.
func APIKey(v app.Values) string { return v.Get(keyName) }

// Guide connects Radarr to the media folder and qBittorrent, then asks for its API key.
func Guide(accessURL string) flow.Screen {
	return flow.Screen{
		Title: "Radarr setup",
		Body: `Open this link:
` + accessURL + `/radarr

1. Open Settings > Media Management.
2. Under Root Folders, click Add Root Folder.
3. Select /media/Movies and save it.
4. Open Settings > Download Clients.
5. Click Add, then select qBittorrent.
6. Set Host to qbittorrent and Port to 8080. Leave Username and Password empty.
7. Set Category to radarr.
8. Click Test, then Save.
9. Open Settings > General > Security.
10. Copy the API Key that Radarr generated automatically. The next terminal prompt will ask for it.`,
		Wait:   "Radarr",
		Fields: []flow.Field{flow.APIKeyField(keyName, "Radarr")},
	}
}
