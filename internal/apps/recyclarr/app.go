// Package recyclarr applies quality profiles to Sonarr and Radarr. It runs
// once per start instead of staying up.
package recyclarr

import (
	"embed"
	"errors"
	"time"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/apps/radarr"
	"github.com/inayayousfi/selfnook/internal/apps/sonarr"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/shell"
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
	if _, err := files.WriteIfChanged(e.ConfigPath("recyclarr", "recyclarr.yml"), config); err != nil {
		return err
	}
	if err := waitForAPI(e, "sonarr-app", "Sonarr", "http://localhost:8989/sonarr/api/v3/system/status", sonarr.APIKey(e.Values)); err != nil {
		return err
	}
	if err := waitForAPI(e, "radarr-app", "Radarr", "http://localhost:7878/radarr/api/v3/system/status", radarr.APIKey(e.Values)); err != nil {
		return err
	}
	return e.Compose("run", "--rm", "recyclarr", "sync")
}

// apiChecks is how many times waitForAPI checks, 2 seconds apart: three minutes.
const apiChecks = 90

func waitForAPI(e *app.Env, service, name, url, apiKey string) error {
	for range apiChecks {
		result, err := shell.ComposeCapture(e.Shell, "exec", "-T", service, "curl", "-fsS", "-o", "/dev/null", "-H", "X-Api-Key: "+apiKey, url)
		if err != nil {
			return err
		}
		if result.Code == 0 {
			return nil
		}
		e.Sleep(2 * time.Second)
	}
	return errors.New(name + " did not become ready within three minutes.")
}
