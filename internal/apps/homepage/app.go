// Package homepage is the dashboard, with the container status reader and
// the system monitor it displays.
package homepage

import (
	"cmp"
	"embed"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/files"
)

//go:embed compose.yaml
var compose embed.FS

//go:embed files
var embedded embed.FS

// groupOrder matches the layout in files/settings.yaml.
var groupOrder = []string{"Media", "Admin"}

var App = &app.App{
	Name:       "homepage",
	Compose:    compose,
	Services:   func(app.Values) []string { return []string{"dockerproxy", "homepage", "glances"} },
	ConfigDirs: []string{"homepage"},
	Prepare: func(e *app.Env) ([]string, error) {
		return nil, WriteConfig(e.ConfigPath("homepage"), e.Tiles)
	},
	Routes: func(app.Values) []app.Route {
		return []app.Route{{Upstream: "homepage:3000", Fallback: true}}
	},
}

// WriteConfig writes Homepage's settings and a services list built from every app's tiles.
func WriteConfig(dir string, tiles []app.Tile) error {
	entries, err := embedded.ReadDir("files")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		content, err := embedded.ReadFile("files/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err := files.WriteIfChanged(filepath.Join(dir, entry.Name()), content); err != nil {
			return err
		}
	}
	_, err = files.WriteIfChanged(filepath.Join(dir, "services.yaml"), []byte(Services(tiles)))
	return err
}

// Services lists the tiles under their groups, each group ordered by position.
func Services(tiles []app.Tile) string {
	order := slices.Clone(groupOrder)
	groups := map[string][]app.Tile{}
	for _, tile := range tiles {
		if !slices.Contains(order, tile.Group) {
			order = append(order, tile.Group)
		}
		groups[tile.Group] = append(groups[tile.Group], tile)
	}
	var out strings.Builder
	out.WriteString("---\n")
	for _, group := range order {
		if len(groups[group]) == 0 {
			continue
		}
		slices.SortStableFunc(groups[group], func(a, b app.Tile) int { return cmp.Compare(a.Position, b.Position) })
		out.WriteString("- " + group + ":\n")
		for _, tile := range groups[group] {
			out.WriteString(tile.YAML)
		}
	}
	return out.String()
}
