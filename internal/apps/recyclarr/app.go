// Package recyclarr applies quality profiles to Sonarr and Radarr. It runs
// once per start instead of staying up.
package recyclarr

import (
	"embed"
	"errors"
	"os"
	"time"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/apps/radarr"
	"github.com/inayayousfi/legal-stuff/internal/apps/sonarr"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

//go:embed compose.yaml
var compose embed.FS

//go:embed recyclarr.yml
var config []byte

var App = &app.App{
	Name:       "recyclarr",
	Compose:    compose,
	ConfigDirs: []string{"recyclarr", "recyclarr-data"},
	Setup:      Sync,
	AfterStart: Sync,
}

// Sync writes the profile configuration, waits for Sonarr and Radarr, and applies it.
func Sync(e *app.Env) error {
	if err := os.WriteFile(e.ConfigPath("recyclarr", "recyclarr.yml"), config, 0o644); err != nil {
		return err
	}
	if err := waitForAPI(e.Shell, "sonarr-app", "Sonarr", "http://localhost:8989/sonarr/api/v3/system/status", e.Values.Get(sonarr.KeyName)); err != nil {
		return err
	}
	if err := waitForAPI(e.Shell, "radarr-app", "Radarr", "http://localhost:7878/radarr/api/v3/system/status", e.Values.Get(radarr.KeyName)); err != nil {
		return err
	}
	return e.Compose("run", "--rm", "recyclarr", "sync")
}

// Sleep is replaced by tests.
var Sleep = time.Sleep

func waitForAPI(s shell.Shell, service, name, url, apiKey string) error {
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		result, err := shell.ComposeCapture(s, "exec", "-T", service, "curl", "-fsS", "-o", "/dev/null", "-H", "X-Api-Key: "+apiKey, url)
		if err != nil {
			return err
		}
		if result.Code == 0 {
			return nil
		}
		Sleep(2 * time.Second)
	}
	return errors.New(name + " did not become ready within three minutes.")
}
