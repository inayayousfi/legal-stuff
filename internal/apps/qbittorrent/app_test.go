package qbittorrent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginBypassKeepsOtherSettingsAndIsWrittenOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qBittorrent.conf")
	os.WriteFile(path, []byte("[BitTorrent]\nSession\\Port=6881\n\n[Preferences]\nWebUI\\Port=8080\nWebUI\\AuthSubnetWhitelistEnabled=false\n\n[Other]\nKey=1\n"), 0o644)
	changed, err := WriteLoginBypass(path)
	if err != nil || !changed {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	content, _ := os.ReadFile(path)
	want := "[BitTorrent]\nSession\\Port=6881\n\n[Preferences]\nWebUI\\Port=8080\n\nWebUI\\AuthSubnetWhitelistEnabled=true\nWebUI\\AuthSubnetWhitelist=172.31.250.0/24\n[Other]\nKey=1\n"
	if string(content) != want {
		t.Errorf("content =\n%s\nwant\n%s", content, want)
	}
	if changed, _ := WriteLoginBypass(path); changed {
		t.Error("an unchanged file was reported as changed")
	}
}

func TestLoginBypassCreatesAMissingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qBittorrent", "config", "qBittorrent.conf")
	if _, err := WriteLoginBypass(path); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "[Preferences]\nWebUI\\AuthSubnetWhitelistEnabled=true\nWebUI\\AuthSubnetWhitelist=172.31.250.0/24\n" {
		t.Errorf("content = %q", content)
	}
}

func TestGuidePrintsTheAdminLoginAndDownloadFolders(t *testing.T) {
	body := Guide("https://media.example.com", "admin", "s3cret").Body
	for _, line := range []string{"https://media.example.com/qbittorrent/\n", "Username: admin\n", "Password: s3cret\n", "/media/Downloads\n", "/media/Downloads/incomplete\n"} {
		if !strings.Contains(body, line) {
			t.Errorf("guide lacks %q", line)
		}
	}
}
