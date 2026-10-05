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

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/apps/vpn"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/settings"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

// Command is the program name shown in instructions.
const Command = "selfhost"

// Stack is one installation: the folder holding .env, config/, and the
// generated Compose files.
type Stack struct {
	Root  string
	Apps  []*app.App
	Shell shell.Shell
	UI    flow.UI
	// Program is the executable that automatic start runs.
	Program string
	// Sleep is replaced by tests.
	Sleep func(time.Duration)
}

func (s *Stack) envFile() string { return filepath.Join(s.Root, ".env") }

func (s *Stack) env(values *settings.Values, progress *settings.Progress) *app.Env {
	return &app.Env{
		Root:     s.Root,
		Apps:     s.Apps,
		Values:   values,
		Shell:    s.Shell,
		UI:       s.UI,
		Progress: progress,
		SaveValues: func() error {
			return settings.Write(s.envFile(), values)
		},
	}
}

func (s *Stack) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (s *Stack) waitForDocker() error {
	return shell.WaitForDocker(s.Shell, shell.DockerWait, s.sleep)
}

var errNoSetup = errors.New("Run '" + Command + " setup' first.")

var coreRequired = []string{"MEDIA_DIR", "CONFIG_DIR", "PUID", "PGID", "TZ"}

// load reads .env and checks that every app has what it needs to start.
func (s *Stack) load() (*settings.Values, error) {
	if _, err := os.Stat(s.envFile()); err != nil {
		return nil, errNoSetup
	}
	values, err := settings.Read(s.envFile())
	if err != nil {
		return nil, err
	}
	required := slices.Clone(coreRequired)
	for _, a := range s.Apps {
		required = append(required, a.Required...)
	}
	var missing []string
	for _, key := range required {
		if !values.Has(key) && !slices.Contains(missing, key) {
			missing = append(missing, key)
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
			if err := a.Validate(values); err != nil {
				return nil, err
			}
		}
	}
	return values, nil
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
			a.Derive(values)
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

// recreateNetworkIfNeeded removes the stack when its network uses another
// address range, so Compose recreates it with the expected one.
func (s *Stack) recreateNetworkIfNeeded() error {
	result, err := shell.Capture(s.Shell, "docker", "network", "inspect", app.NetworkName, "--format", "{{range .IPAM.Config}}{{.Subnet}} {{end}}")
	if err != nil {
		return err
	}
	subnet := strings.TrimSpace(result.Stdout)
	if result.Code == 0 && subnet != app.NetworkSubnet {
		return shell.Compose(s.Shell, "down", "--remove-orphans")
	}
	return nil
}

// startServices runs every app's Prepare hook, starts the containers that do
// not use the VPN, then starts the VPN users once the gateway is ready.
func (s *Stack) startServices(e *app.Env) error {
	var behindVPN, active, inactive []string
	for _, a := range s.Apps {
		behindVPN = append(behindVPN, a.BehindVPN...)
		var services []string
		if a.Services != nil {
			services = a.Services(e.Values)
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
	if err := e.Compose(append([]string{"stop"}, append(behindVPN, inactive...)...)...); err != nil {
		return err
	}
	var restart []string
	for _, a := range s.Apps {
		if a.Prepare == nil {
			continue
		}
		services, err := a.Prepare(e)
		if err != nil {
			return err
		}
		restart = append(restart, services...)
	}
	if err := e.Compose(append([]string{"up", "-d", "--no-deps", "--remove-orphans"}, active...)...); err != nil {
		return err
	}
	for _, service := range restart {
		if err := e.Compose("restart", service); err != nil {
			return err
		}
	}
	for _, a := range s.Apps {
		if a.Started != nil {
			if err := a.Started(e); err != nil {
				return err
			}
		}
	}
	vpn.WaitUntilReady(s.Shell, e.Values, s.UI.Say)
	return e.Compose(append([]string{"up", "-d", "--no-deps"}, behindVPN...)...)
}

// Start starts the stack with the saved values.
func (s *Stack) Start() error {
	values, err := s.load()
	if err != nil {
		return err
	}
	s.derive(values)
	e := s.env(values, nil)
	if err := e.SaveValues(); err != nil {
		return err
	}
	if err := s.waitForDocker(); err != nil {
		return err
	}
	if err := s.writeCompose(values); err != nil {
		return err
	}
	if err := s.recreateNetworkIfNeeded(); err != nil {
		return err
	}
	if err := s.createDirectories(values); err != nil {
		return err
	}
	if err := s.startServices(e); err != nil {
		return err
	}
	for _, a := range s.Apps {
		if a.AfterStart != nil {
			if err := a.AfterStart(e); err != nil {
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

// VPNStatus reports whether the VPN gateway carries traffic.
func (s *Stack) VPNStatus() error {
	values, err := s.load()
	if err != nil {
		return err
	}
	if err := s.waitForDocker(); err != nil {
		return err
	}
	text, err := vpn.Status(s.Shell, values)
	if err != nil {
		return err
	}
	s.UI.Say(text)
	return nil
}
