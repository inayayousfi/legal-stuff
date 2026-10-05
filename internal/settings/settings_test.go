package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestValuesSurviveAWriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	values := NewValues()
	values.Set("MEDIA_DIR", "/media path/$disk")
	values.Set("ADMIN_PASS", `p"q\r$`)
	values.Set("NAME", "Médias ✓ 😀")
	values.Set("EMPTY", "")
	if err := Write(path, values); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), `MEDIA_DIR="/media path/$$disk"`) {
		t.Errorf("$ is not escaped for Docker Compose:\n%s", content)
	}
	read, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range values.Keys() {
		if read.Get(key) != values.Get(key) {
			t.Errorf("%s = %q, want %q", key, read.Get(key), values.Get(key))
		}
	}
	if !slices.Equal(read.Keys(), values.Keys()) {
		t.Errorf("order = %v", read.Keys())
	}
}

func TestEnvironmentFileIsPrivateOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions are unavailable on Windows")
	}
	path := filepath.Join(t.TempDir(), ".env")
	values := NewValues()
	values.Set("KEY", "value")
	Write(path, values)
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestLineBreaksAreRejected(t *testing.T) {
	values := NewValues()
	values.Set("KEY", "a\nb")
	if err := Write(filepath.Join(t.TempDir(), ".env"), values); err == nil {
		t.Error("a value with a line break was written")
	}
}

func TestMissingFileReadsAsEmpty(t *testing.T) {
	values, err := Read(filepath.Join(t.TempDir(), ".env"))
	if err != nil || len(values.Keys()) != 0 {
		t.Errorf("values = %v, err = %v", values.Keys(), err)
	}
}

func TestOldJellyfinNamesAreMovedAndSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("JELLYFIN_USER=\"jelly\"\nJELLYFIN_PASS=secret\n"), 0o600)
	values, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("JELLYFIN_ADMIN_USER") != "jelly" || values.Get("JELLYFIN_ADMIN_PASS") != "secret" {
		t.Errorf("values = %v", values.Keys())
	}
	content, _ := os.ReadFile(path)
	if strings.Contains(string(content), "JELLYFIN_USER=") {
		t.Errorf("old name still saved:\n%s", content)
	}
}

func TestProgressSurvivesAndKeepsEarlierSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "setup-state.json")
	progress, err := ReadProgress(path)
	if err != nil {
		t.Fatal(err)
	}
	progress.Complete("qbittorrent")
	progress.Complete("sonarr")
	again, err := ReadProgress(path)
	if err != nil || !again.Done("qbittorrent") || !again.Done("sonarr") || again.Done("radarr") {
		t.Errorf("progress not kept: %v", err)
	}
}

func TestInvalidProgressIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup-state.json")
	os.WriteFile(path, []byte(`{"sonarr": true}`), 0o644)
	if _, err := ReadProgress(path); err == nil || !strings.HasPrefix(err.Error(), "Invalid setup progress") {
		t.Errorf("err = %v", err)
	}
}
