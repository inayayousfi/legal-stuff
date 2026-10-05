// Package stack runs the commands: it walks the registry to set up, start,
// stop, and inspect every app.
package stack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/settings"
	"github.com/inayayousfi/selfnook/internal/shell"
)

// Command is the program name shown in instructions.
const Command = "selfnook"

// Stack is one installation: the folder holding .env, config/, and the
// generated Compose files.
type Stack struct {
	Root  string
	Apps  []*app.App
	Shell shell.Shell
	UI    flow.UI
	// Program is the executable that automatic start runs.
	Program string
	// Sleep waits between checks. Tests replace it.
	Sleep func(time.Duration)
}

// coreSettings are the .env values the stack itself owns.
var coreSettings = []app.Setting{
	{Key: "MEDIA_DIR", Example: "/absolute/path/to/Media", Required: true},
	{Key: "CONFIG_DIR", Example: "/absolute/path/to/the/selfnook/folder/config", Required: true},
	{Key: "PUID", Example: "1000", Required: true},
	{Key: "PGID", Example: "1000", Required: true},
	{Key: "TZ", Example: "Europe/Paris", Required: true},
}

func (s *Stack) envFile() string { return filepath.Join(s.Root, ".env") }

func (s *Stack) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

// env gives one app's hook its view of the values and every app's contributions.
func (s *Stack) env(a *app.App, values *settings.Values, progress *settings.Progress) *app.Env {
	routes, tiles := s.contributions(values)
	return &app.Env{
		Root:     s.Root,
		Values:   app.ValuesFor(values, a),
		Shell:    s.Shell,
		UI:       s.UI,
		Routes:   routes,
		Tiles:    tiles,
		Progress: progress,
		SaveValues: func() error {
			return settings.Write(s.envFile(), values)
		},
		Sleep: s.sleep,
	}
}

// contributions collects every app's routes and tiles in registry order.
func (s *Stack) contributions(values *settings.Values) ([]app.Route, []app.Tile) {
	var routes []app.Route
	var tiles []app.Tile
	for _, a := range s.Apps {
		v := app.ValuesFor(values, a)
		if a.Routes != nil {
			routes = append(routes, a.Routes(v)...)
		}
		if a.Tiles != nil {
			tiles = append(tiles, a.Tiles(v)...)
		}
	}
	return routes, tiles
}

func (s *Stack) waitForDocker() error {
	return shell.WaitForDocker(s.Shell, s.sleep)
}

var errNoSetup = errors.New("Run '" + Command + " setup' first.")

// load reads .env and checks that every app has what it needs to start.
func (s *Stack) load() (*settings.Values, error) {
	values, err := s.readSaved()
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, setting := range s.settings() {
		if setting.Required && !values.Has(setting.Key) {
			missing = append(missing, setting.Key)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("Missing required .env values: %s. Run '%s setup' to add them.", strings.Join(missing, ", "), Command)
	}
	for _, key := range []string{"MEDIA_DIR", "CONFIG_DIR"} {
		if !filepath.IsAbs(filepath.FromSlash(values.Get(key))) {
			return nil, fmt.Errorf("%s must be an absolute path.", key)
		}
	}
	for _, a := range s.Apps {
		if a.Validate != nil {
			if err := a.Validate(app.ValuesFor(values, a)); err != nil {
				return nil, err
			}
		}
	}
	return values, nil
}

// settings lists the core settings, then each app's settings in registry order.
func (s *Stack) settings() []app.Setting {
	all := slices.Clone(coreSettings)
	for _, a := range s.Apps {
		all = append(all, a.Settings...)
	}
	return all
}

// readSaved reads .env without requiring a finished setup.
func (s *Stack) readSaved() (*settings.Values, error) {
	if _, err := os.Stat(s.envFile()); err != nil {
		return nil, errNoSetup
	}
	return settings.Read(s.envFile())
}

func (s *Stack) derive(values *settings.Values) {
	for _, a := range s.Apps {
		if a.Derive != nil {
			a.Derive(app.ValuesFor(values, a))
		}
	}
}

func (s *Stack) createDirectories(values *settings.Values) error {
	for _, a := range s.Apps {
		for _, dir := range a.MediaDirs {
			if err := os.MkdirAll(filepath.Join(values.Get("MEDIA_DIR"), dir), 0o755); err != nil {
				return err
			}
		}
		for _, dir := range a.ConfigDirs {
			if err := os.MkdirAll(filepath.Join(values.Get("CONFIG_DIR"), filepath.FromSlash(dir)), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

// retiredProject is the Compose project name that earlier versions used.
const retiredProject = "media-stack"

// removeRetiredProject removes the containers and network of an install
// made under the earlier project name, which would otherwise hold the ports
// and the address range. Configuration and media folders are kept.
func (s *Stack) removeRetiredProject() error {
	containers, err := shell.Capture(s.Shell, "docker", "ps", "--all", "--quiet", "--filter", "label=com.docker.compose.project="+retiredProject)
	if err != nil {
		return err
	}
	network, err := shell.Capture(s.Shell, "docker", "network", "inspect", retiredProject)
	if err != nil {
		return err
	}
	if strings.TrimSpace(containers.Stdout) == "" && network.Code != 0 {
		return nil
	}
	if err := shell.Compose(s.Shell, "--project-name", retiredProject, "down", "--remove-orphans"); err != nil {
		return err
	}
	_, err = shell.Capture(s.Shell, "docker", "image", "rm", retiredProject+"/vpn-country")
	return err
}

// recreateNetworkIfNeeded removes the stack when its network uses another
// address range, so Compose recreates it with the expected one.
func (s *Stack) recreateNetworkIfNeeded() error {
	result, err := shell.Capture(s.Shell, "docker", "network", "inspect", app.Name, "--format", "{{range .IPAM.Config}}{{.Subnet}} {{end}}")
	if err != nil {
		return err
	}
	subnet := strings.TrimSpace(result.Stdout)
	if result.Code == 0 && subnet != app.NetworkSubnet {
		return shell.Compose(s.Shell, "down", "--remove-orphans")
	}
	return nil
}

// StoppedError reports a start that failed after stopping the services
// behind the VPN. They stay stopped until the next successful start.
type StoppedError struct {
	Services []string
	Err      error
}

func (e *StoppedError) Error() string {
	return e.Err.Error() + "\nStopped until the next successful start: " + strings.Join(e.Services, ", ")
}

func (e *StoppedError) Unwrap() error { return e.Err }

// startServices stops the services behind the VPN, runs every app's Prepare
// hook, starts the other containers, runs every Started hook, then starts
// the services behind the VPN.
func (s *Stack) startServices(values *settings.Values, progress *settings.Progress) error {
	var behindVPN, active, inactive []string
	for _, a := range s.Apps {
		behindVPN = append(behindVPN, a.BehindVPN...)
		var services []string
		if a.Services != nil {
			services = a.Services(app.ValuesFor(values, a))
		}
		for _, service := range a.Optional {
			if !slices.Contains(services, service) {
				inactive = append(inactive, service)
			}
		}
		for _, service := range services {
			if !slices.Contains(a.BehindVPN, service) {
				active = append(active, service)
			}
		}
	}
	if err := shell.Compose(s.Shell, append([]string{"stop"}, append(behindVPN, inactive...)...)...); err != nil {
		return err
	}
	if err := s.startWhileVPNServicesStopped(values, progress, active); err != nil {
		return &StoppedError{Services: behindVPN, Err: err}
	}
	if err := shell.Compose(s.Shell, append([]string{"up", "-d", "--no-deps"}, behindVPN...)...); err != nil {
		return &StoppedError{Services: behindVPN, Err: err}
	}
	return nil
}

func (s *Stack) startWhileVPNServicesStopped(values *settings.Values, progress *settings.Progress, active []string) error {
	var restart []string
	for _, a := range s.Apps {
		if a.Prepare == nil {
			continue
		}
		services, err := a.Prepare(s.env(a, values, progress))
		if err != nil {
			return err
		}
		restart = append(restart, services...)
	}
	if err := shell.Compose(s.Shell, append([]string{"up", "-d", "--no-deps", "--remove-orphans"}, active...)...); err != nil {
		return err
	}
	for _, service := range restart {
		if err := shell.Compose(s.Shell, "restart", service); err != nil {
			return err
		}
	}
	for _, a := range s.Apps {
		if a.Started != nil {
			if err := a.Started(s.env(a, values, progress)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Start starts the stack with the saved values.
func (s *Stack) Start() error {
	values, err := s.load()
	if err != nil {
		return err
	}
	if err := s.migrate(values); err != nil {
		return err
	}
	s.derive(values)
	if err := settings.Write(s.envFile(), values); err != nil {
		return err
	}
	if err := s.waitForDocker(); err != nil {
		return err
	}
	if err := s.writeCompose(values); err != nil {
		return err
	}
	if err := s.removeRetiredProject(); err != nil {
		return err
	}
	if err := s.recreateNetworkIfNeeded(); err != nil {
		return err
	}
	if err := s.createDirectories(values); err != nil {
		return err
	}
	if err := s.startServices(values, nil); err != nil {
		return err
	}
	for _, a := range s.Apps {
		if a.AfterStart != nil {
			if err := a.AfterStart(s.env(a, values, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Stop removes the containers and network, keeping configuration and media.
func (s *Stack) Stop() error {
	return s.compose("down")
}

// Status shows Compose's status for every service.
func (s *Stack) Status() error {
	return s.compose("ps")
}

func (s *Stack) compose(args ...string) error {
	values, err := s.readSaved()
	if err != nil {
		return err
	}
	if err := s.waitForDocker(); err != nil {
		return err
	}
	if err := s.writeCompose(values); err != nil {
		return err
	}
	return shell.Compose(s.Shell, args...)
}

// AppStatus shows one app's own health report.
func (s *Stack) AppStatus(a *app.App) error {
	values, err := s.load()
	if err != nil {
		return err
	}
	if err := s.waitForDocker(); err != nil {
		return err
	}
	text, err := a.Status(s.env(a, values, nil))
	if err != nil {
		return err
	}
	s.UI.Say(text)
	return nil
}
