// Package homepage is the dashboard, with the container status reader and
// the system monitor it displays.
package homepage

import (
	"embed"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

//go:embed compose.yaml
var compose embed.FS

//go:embed files
var files embed.FS

// groupOrder matches the layout in files/settings.yaml.
var groupOrder = []string{"Media", "Admin"}

var App = &app.App{
	Name:       "homepage",
	Compose:    compose,
	Services:   func(*settings.Values) []string { return []string{"dockerproxy", "homepage", "glances"} },
	ConfigDirs: []string{"homepage"},
	Prepare: func(e *app.Env) ([]string, error) {
		return nil, WriteConfig(e.ConfigPath("homepage"), e.Apps, e.Values)
	},
	Routes: func(*settings.Values) []app.Route {
		return []app.Route{{Upstream: "homepage:3000", Fallback: true}}
	},
}

// WriteConfig writes Homepage's settings and a services list built from every app's tiles.
func WriteConfig(dir string, apps []*app.App, v *settings.Values) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := files.ReadDir("files")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		content, err := files.ReadFile("files/" + entry.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), content, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "services.yaml"), []byte(Services(apps, v)), 0o644)
}

// Services lists every app's tiles under their groups.
func Services(apps []*app.App, v *settings.Values) string {
	groups := map[string][]string{}
	order := slices.Clone(groupOrder)
	for _, a := range apps {
		if a.Tiles == nil {
			continue
		}
		for _, tile := range a.Tiles(v) {
			if !slices.Contains(order, tile.Group) {
				order = append(order, tile.Group)
			}
			groups[tile.Group] = append(groups[tile.Group], tile.YAML)
		}
	}
	var out strings.Builder
	out.WriteString("---\n")
	for _, group := range order {
		if len(groups[group]) == 0 {
			continue
		}
		out.WriteString("- " + group + ":\n")
		for _, tile := range groups[group] {
			out.WriteString(tile)
		}
	}
	return out.String()
}
