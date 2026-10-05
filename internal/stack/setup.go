package stack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/autostart"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/settings"
	"github.com/inayayousfi/selfnook/internal/shell"
)

// Setup asks every question, starts the stack, guides through each app's
// first configuration, and installs automatic start. Progress is saved, so
// an interrupted setup resumes where it stopped.
func (s *Stack) Setup() error {
	if err := s.waitForDocker(); err != nil {
		return err
	}
	s.UI.Say("This setup saves progress and can be rerun after an interruption.")

	values, err := settings.Read(s.envFile())
	if err != nil {
		return err
	}
	if err := s.migrate(values); err != nil {
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
	for _, a := range s.Apps {
		if a.Configure != nil {
			if err := a.Configure(s.env(a, values, progress)); err != nil {
				return err
			}
		}
	}
	s.derive(values)
	if err := s.createDirectories(values); err != nil {
		return err
	}
	if err := settings.Write(s.envFile(), values); err != nil {
		return err
	}

	if err := s.writeCompose(values); err != nil {
		return err
	}
	if err := shell.Compose(s.Shell, "config", "--quiet"); err != nil {
		return err
	}
	if err := s.removeRetiredProject(); err != nil {
		return err
	}
	if err := s.recreateNetworkIfNeeded(); err != nil {
		return err
	}
	if err := s.startServices(values, progress); err != nil {
		return err
	}

	for _, a := range s.Apps {
		if a.Setup != nil {
			if err := a.Setup(s.env(a, values, progress)); err != nil {
				return err
			}
		}
	}

	if err := autostart.Install(s.Shell, s.UI, s.Program, s.Root); err != nil {
		return err
	}
	s.UI.Say("Setup complete. Use '" + Command + " status' to inspect it.\nOpen Homepage:\n" + values.Get("ACCESS_URL"))
	return nil
}

// migrate moves values saved under names that earlier versions used to each
// app's current setting names, and removes what replaced apps left behind.
func (s *Stack) migrate(values *settings.Values) error {
	for _, a := range s.Apps {
		for _, key := range a.RetiredSettings {
			values.Delete(key)
		}
		if values.Has("CONFIG_DIR") {
			for _, dir := range a.RetiredConfigDirs {
				if err := os.RemoveAll(filepath.Join(values.Get("CONFIG_DIR"), filepath.FromSlash(dir))); err != nil {
					return fmt.Errorf("Cannot remove the unused folder %s: %w", dir, err)
				}
			}
		}
		v := app.ValuesFor(values, a)
		for old, current := range a.Renamed {
			if !values.Has(old) {
				continue
			}
			if !v.Has(current) {
				v.Set(current, values.Get(old))
			}
			values.Delete(old)
		}
	}
	return nil
}

// normalizedPath turns an entered folder into an absolute path.
func normalizedPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("Path cannot be empty.")
	}
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("Cannot resolve path: " + raw)
		}
		value = filepath.Join(home, value[1:])
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("Cannot resolve path: " + raw)
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
