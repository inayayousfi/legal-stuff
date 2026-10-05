// Package seerr takes movie and series requests and sends them to Radarr and
// Sonarr. It keeps its own login and is served on its own port.
package seerr

import (
	"embed"
	"strconv"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/apps/radarr"
	"github.com/inayayousfi/legal-stuff/internal/apps/sonarr"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

//go:embed compose.yaml
var compose embed.FS

// Port is where Seerr is served, because it does not support a path prefix.
const Port = 5055

var App = &app.App{
	Name:       "seerr",
	Compose:    compose,
	Services:   func(*settings.Values) []string { return []string{"seerr"} },
	ConfigDirs: []string{"seerr"},
	Setup: func(e *app.Env) error {
		return e.Step("seerr", false, func() error {
			return flow.Show(e.UI, Guide(e.Values))
		})
	},
	Routes: func(*settings.Values) []app.Route {
		return []app.Route{{Path: "/seerr", Upstream: "seerr:5055", Public: true, Port: Port}}
	},
	Tiles: func(*settings.Values) []app.Tile {
		return []app.Tile{{Group: "Media", YAML: `    - Seerr:
        icon: seerr.png
        server: media-stack
        container: seerr
        href: "{{HOMEPAGE_VAR_URL}}/seerr"
        description: Request movies and series
`}}
	},
}

// Guide connects Seerr to Jellyfin, Radarr, and Sonarr with the saved logins and keys.
func Guide(v *settings.Values) flow.Screen {
	return flow.Screen{
		Title: "Seerr setup",
		Body: `Open this link:
` + v.Get("ACCESS_URL") + ":" + strconv.Itoa(Port) + `

1. Choose Jellyfin as the server type.
2. Jellyfin URL: jellyfin
3. Port: 8096
4. Email Address: enter an email address of your choice. Seerr uses it for notifications and its own sign-in.
5. Username: ` + v.Get("JELLYFIN_ADMIN_USER") + `
6. Password: ` + v.Get("JELLYFIN_ADMIN_PASS") + `
7. Click Sign In.
8. Click Sync Libraries, enable the Movies and Shows libraries, then click Continue.
9. Click Add Radarr Server and check Default Server.
10. Server Name: Radarr
11. Hostname or IP Address: radarr
12. Port: 7878
13. API Key: ` + v.Get(radarr.KeyName) + `
14. Click Test.
15. Quality Profile: 4K Progressive
16. Root Folder: /media/Movies
17. Select a Minimum Availability, then click Add Server.
18. Click Add Sonarr Server and check Default Server.
19. Server Name: Sonarr
20. Hostname or IP Address: sonarr
21. Port: 8989
22. API Key: ` + v.Get(sonarr.KeyName) + `
23. Click Test.
24. Quality Profile: 4K Progressive
25. Root Folder: /media/Series
26. Check Season Folders, then click Add Server.
27. Click Finish Setup.`,
		Wait: "Seerr",
	}
}
