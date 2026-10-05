package stack

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/platform"
	"github.com/inayayousfi/legal-stuff/internal/settings"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

// Setup asks every question, starts the stack, guides through each app's
// first configuration, and installs automatic start. Progress is saved, so
// an interrupted setup resumes where it stopped.
func (s *Stack) Setup() error {
	platform.RestoreConsoleInput()
	if err := s.waitForDocker(); err != nil {
		return err
	}
	s.UI.Say("This setup saves progress and can be rerun after an interruption.\n" +
		"It does not migrate named volumes created by the previous stack.\n" +
		"Existing named volumes are left untouched for manual recovery.")

	values, err := settings.Read(s.envFile())
	if err != nil {
		return err
	}
	var mediaDir string
	if values.Has("MEDIA_DIR") {
		if mediaDir, err = normalizedPath(values.Get("MEDIA_DIR")); err != nil {
			return err
		}
		s.UI.Say("Reusing media directory: " + mediaDir)
	} else {
		answers, err := s.UI.Ask(flow.Screen{Fields: []flow.Field{{Key: "media", Prompt: "Media directory", Check: normalizedPath}}})
		if err != nil {
			return err
		}
		mediaDir = answers["media"]
	}

	configDir := filepath.Join(s.Root, "config")
	uid, gid := userIDs()
	values.Set("MEDIA_DIR", filepath.ToSlash(mediaDir))
	values.Set("CONFIG_DIR", filepath.ToSlash(configDir))
	values.Set("PUID", uid)
	values.Set("PGID", gid)
	values.SetDefault("TZ", "Europe/Paris")

	progress, err := settings.ReadProgress(filepath.Join(configDir, "setup-state.json"))
	if err != nil {
		return err
	}
	e := s.env(values, progress)
	for _, a := range s.Apps {
		if a.Configure != nil {
			if err := a.Configure(e); err != nil {
				return err
			}
		}
	}
	s.derive(values)
	if err := s.createDirectories(values); err != nil {
		return err
	}
	if err := e.SaveValues(); err != nil {
		return err
	}

	if err := s.writeCompose(values); err != nil {
		return err
	}
	if err := shell.Compose(s.Shell, "config", "--quiet"); err != nil {
		return err
	}
	if err := s.recreateNetworkIfNeeded(); err != nil {
		return err
	}
	if err := s.startServices(e); err != nil {
		return err
	}

	for _, a := range s.Apps {
		if a.Setup != nil {
			if err := a.Setup(e); err != nil {
				return err
			}
		}
	}

	if err := platform.InstallAutostart(s.Shell, s.UI, s.Program, s.Root); err != nil {
		return err
	}
	s.UI.Say("Setup complete. Use '" + Command + " status' to inspect it.\nOpen Homepage:\n" + values.Get("ACCESS_URL"))
	return nil
}

// normalizedPath turns an entered folder into an absolute path.
func normalizedPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("Error: Path cannot be empty.")
	}
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("Error: Cannot resolve path: " + raw)
		}
		value = filepath.Join(home, value[1:])
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("Error: Cannot resolve path: " + raw)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path, nil
}

// userIDs are the IDs containers run as, so they can write the shared folders.
func userIDs() (string, string) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid < 0 || gid < 0 {
		return "1000", "1000"
	}
	return strconv.Itoa(uid), strconv.Itoa(gid)
}
