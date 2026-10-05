package homepage_test

import (
	"strings"
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/apps"
	"github.com/inayayousfi/selfnook/internal/apps/homepage"
	"github.com/inayayousfi/selfnook/internal/settings"
)

// Tiles follow their own positions, not the order of the registry.
func TestTilesFollowTheirPositions(t *testing.T) {
	saved := settings.NewValues()
	saved.Set("VPN_GATEWAY_SERVICE", "gluetun")
	var tiles []app.Tile
	for i := len(apps.All) - 1; i >= 0; i-- {
		if a := apps.All[i]; a.Tiles != nil {
			tiles = append(tiles, a.Tiles(app.ValuesFor(saved, a))...)
		}
	}
	var names []string
	for _, line := range strings.Split(homepage.Services(tiles), "\n") {
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "    - ") {
			names = append(names, strings.TrimSpace(strings.TrimSuffix(line, ":")))
		}
	}
	want := "- Media - Jellyfin - Seerr - Admin - qBittorrent - Sonarr - Radarr - Prowlarr - Gluetun - Authentik"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("order = %s", got)
	}
}
