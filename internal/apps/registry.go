// Package apps lists every application the stack runs.
package apps

import (
	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/apps/authentik"
	"github.com/inayayousfi/selfnook/internal/apps/caddy"
	"github.com/inayayousfi/selfnook/internal/apps/homepage"
	"github.com/inayayousfi/selfnook/internal/apps/jellyfin"
	"github.com/inayayousfi/selfnook/internal/apps/prowlarr"
	"github.com/inayayousfi/selfnook/internal/apps/qbittorrent"
	"github.com/inayayousfi/selfnook/internal/apps/radarr"
	"github.com/inayayousfi/selfnook/internal/apps/recyclarr"
	"github.com/inayayousfi/selfnook/internal/apps/seerr"
	"github.com/inayayousfi/selfnook/internal/apps/sonarr"
	"github.com/inayayousfi/selfnook/internal/apps/vpn"
)

// All is the registry. Its order is the order of setup questions, file
// preparation, guided setup steps, and credential groups. Guided steps read
// what earlier steps saved: Prowlarr and Recyclarr need the Sonarr and Radarr
// API keys, and Seerr needs the Jellyfin login. Homepage tiles carry their
// own positions.
var All = []*app.App{
	qbittorrent.App,
	sonarr.App,
	radarr.App,
	prowlarr.App,
	vpn.App,
	caddy.App,
	authentik.App,
	homepage.App,
	jellyfin.App,
	recyclarr.App,
	seerr.App,
}
