package jellyfin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/settings"
)

func TestBaseURLKeepsOtherNetworkSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.xml")
	original := `<?xml version="1.0" encoding="utf-8"?>
<NetworkConfiguration xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <BaseUrl />
  <EnableHttps>false</EnableHttps>
</NetworkConfiguration>`
	os.WriteFile(path, []byte(original), 0o644)
	if BaseURLIsSet(path) {
		t.Fatal("an empty base URL counts as set")
	}
	if err := WriteBaseURL(path); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if want := strings.Replace(original, "<BaseUrl />", "<BaseUrl>/jellyfin</BaseUrl>", 1); string(content) != want {
		t.Errorf("content =\n%s", content)
	}
	if !BaseURLIsSet(path) {
		t.Error("base URL not detected after writing")
	}
}

func TestBaseURLIsAddedWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.xml")
	os.WriteFile(path, []byte("<NetworkConfiguration>\n  <EnableHttps>false</EnableHttps>\n</NetworkConfiguration>"), 0o644)
	if err := WriteBaseURL(path); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "<EnableHttps>false</EnableHttps>") || !BaseURLIsSet(path) {
		t.Errorf("content =\n%s", content)
	}
}

func TestBaseURLCreatesAMissingNetworkFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "network.xml")
	if err := WriteBaseURL(path); err != nil {
		t.Fatal(err)
	}
	if !BaseURLIsSet(path) {
		content, _ := os.ReadFile(path)
		t.Errorf("content =\n%s", content)
	}
}

// Jellyfin rewrites its network file on exit, so it must be stopped before the edit.
func TestMissingBaseURLIsWrittenWhileJellyfinIsStopped(t *testing.T) {
	values := settings.NewValues()
	values.Set("CONFIG_DIR", t.TempDir())
	sh := &fake.Shell{}
	e := &app.Env{Values: app.ValuesFor(values, App), Shell: sh}
	path := networkFile(e.ConfigDir())
	if _, err := prepare(e); err != nil {
		t.Fatal(err)
	}
	setWhenStopped := len(sh.Commands) == 1 && strings.Join(sh.Commands[0], " ") == "docker compose stop jellyfin-app"
	if !setWhenStopped || !BaseURLIsSet(path) {
		t.Errorf("commands = %v", sh.Lines())
	}
	sh.Commands = nil
	prepare(e)
	if len(sh.Commands) != 0 {
		t.Errorf("Jellyfin stopped although its base URL was set: %v", sh.Lines())
	}
}
