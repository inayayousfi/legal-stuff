// Package radarr manages movies.
package radarr

import (
	"embed"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

//go:embed compose.yaml
var compose embed.FS

const KeyName = "RADARR_API_KEY"

var App = &app.App{
	Name:       "radarr",
	Compose:    compose,
	Services:   func(*settings.Values) []string { return []string{"radarr-app"} },
	ConfigDirs: []string{"radarr"},
	MediaDirs:  []string{"Movies"},
	Required:   []string{KeyName},
	Validate: func(v *settings.Values) error {
		_, err := flow.ValidAPIKey(v.Get(KeyName))
		return err
	},
	Setup: func(e *app.Env) error {
		return e.Step("radarr", !e.Values.Has(KeyName), func() error {
			answers, err := e.UI.Ask(Guide(e.Values.Get("ACCESS_URL")))
			if err != nil {
				return err
			}
			e.Values.Set(KeyName, answers[KeyName])
			return e.SaveValues()
		})
	},
	Routes: func(*settings.Values) []app.Route {
		return []app.Route{{
			Path:     "/radarr",
			Upstream: "radarr-app:7878",
			Internal: &app.Internal{Name: "radarr", Port: 7878},
		}}
	},
	Tiles: func(*settings.Values) []app.Tile {
		return []app.Tile{{Group: "Admin", YAML: `    - Radarr:
        icon: radarr.png
        server: media-stack
        container: radarr-app
        href: "{{HOMEPAGE_VAR_URL}}/radarr"
        description: Manage movies
`}}
	},
	Credentials: &app.CredentialGroup{Name: "radarr", Fields: []app.CredentialField{
		{Label: "API key", Key: KeyName, Kind: app.APIKey, UsedByStack: true},
	}},
}

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
		Fields: []flow.Field{flow.APIKeyField(KeyName, "Radarr")},
	}
}
