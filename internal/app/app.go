// Package app defines what one application contributes to the stack.
// Each application lives in its own package under internal/apps and is
// listed once in internal/apps/registry.go.
package app

import (
	"io/fs"
	"path/filepath"

	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/settings"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

// App is everything one application adds. Every field except Name is optional.
type App struct {
	// Name identifies the app and names its Compose fragment.
	Name string

	// Compose holds compose.yaml and any build files for this app's
	// containers. The file is a text/template rendered with ComposeData.
	Compose fs.FS

	// Services lists the Compose services this app runs with the current
	// values. BehindVPN services start only once the VPN gateway is ready.
	Services  func(v *settings.Values) []string
	BehindVPN []string
	// Optional lists services that run only with some values. The stack
	// stops the ones that Services does not return.
	Optional []string

	// ConfigDirs and MediaDirs are created under CONFIG_DIR and MEDIA_DIR
	// before the stack starts.
	ConfigDirs []string
	MediaDirs  []string

	// Required lists .env keys that must be set before the stack can start.
	// Validate checks their content.
	Required []string
	Validate func(v *settings.Values) error

	// Derive fills values computed from other values or generated
	// automatically. It runs before every start.
	Derive func(v *settings.Values)

	// Configure asks the questions this app needs before the stack starts.
	// It asks nothing when the saved values are already complete.
	Configure func(e *Env) error

	// Prepare writes this app's files before the containers start. It
	// returns the services that must restart because a file they read changed.
	Prepare func(e *Env) (restart []string, err error)

	// Started runs once the containers that do not use the VPN are up.
	Started func(e *Env) error

	// AfterStart runs at the end of the start command.
	AfterStart func(e *Env) error

	// Setup is this app's guided setup, run in registry order once the
	// stack is up. It records finished steps with Env.Step.
	Setup func(e *Env) error

	// Routes are the web addresses Caddy serves for this app.
	Routes func(v *settings.Values) []Route

	// Tiles are this app's entries on the Homepage dashboard.
	Tiles func(v *settings.Values) []Tile

	// Credentials is the group of saved values that the credentials command
	// shows and changes.
	Credentials *CredentialGroup
}

// Route is one address that Caddy forwards to a container.
type Route struct {
	// Path is the address path, such as /sonarr.
	Path string
	// Upstream is the container address, such as sonarr-app:8989.
	Upstream string
	// Public routes skip the admin sign-in.
	Public bool
	// StripPrefix removes Path before forwarding and redirects Path to Path/.
	StripPrefix bool
	// Port, when set, serves Upstream on its own port; Path redirects there.
	Port int
	// Fallback receives every request that no other route matches.
	Fallback bool
	// Internal, when set, also answers inside the Docker network at
	// http://Internal.Name:Internal.Port, adding Path when it is missing.
	Internal *Internal
}

type Internal struct {
	Name string
	Port int
}

// Tile is one Homepage service entry.
type Tile struct {
	Group string
	// YAML is the entry's text, indented as a list item under its group.
	YAML string
}

// CredentialGroup lists saved values shown together by the credentials command.
type CredentialGroup struct {
	Name   string
	Fields []CredentialField
	// Visible, when set, filters fields based on other values.
	Visible func(v *settings.Values, f CredentialField) bool
}

type CredentialKind int

const (
	// Fixed values are shown but not changed through the credentials command.
	Fixed CredentialKind = iota
	PlainText
	Password
	APIKey
)

type CredentialField struct {
	Label string
	Key   string
	Kind  CredentialKind
	// UsedByStack means the containers read this value at start.
	UsedByStack bool
}

// ComposeData is passed to every Compose fragment template.
type ComposeData struct {
	// InternalNames are the container network names Caddy answers for.
	InternalNames []string
}

// Env gives a hook access to the saved values, the files, and the programs it runs.
type Env struct {
	Root string
	// Apps is the whole registry, for apps that assemble what every app
	// contributes, such as the Caddyfile and the Homepage dashboard.
	Apps   []*App
	Values *settings.Values
	Shell  shell.Shell
	UI     flow.UI
	// Progress is set during setup.
	Progress *settings.Progress
	// SaveValues writes Values to .env.
	SaveValues func() error
}

func (e *Env) ConfigDir() string { return e.Values.Get("CONFIG_DIR") }

func (e *Env) MediaDir() string { return e.Values.Get("MEDIA_DIR") }

// ConfigPath joins parts under CONFIG_DIR.
func (e *Env) ConfigPath(parts ...string) string {
	return filepath.Join(append([]string{e.ConfigDir()}, parts...)...)
}

// Compose runs docker compose with args in the stack folder.
func (e *Env) Compose(args ...string) error { return shell.Compose(e.Shell, args...) }

// Step runs a guided step once, recording it in the setup progress.
// Steps already recorded are skipped unless again reports that they must repeat.
func (e *Env) Step(name string, again bool, run func() error) error {
	if e.Progress.Done(name) && !again {
		return nil
	}
	if err := run(); err != nil {
		return err
	}
	return e.Progress.Complete(name)
}

// The Docker network every container joins. qBittorrent skips its login for
// requests from this address range.
const (
	NetworkName   = "media-stack"
	NetworkSubnet = "172.31.250.0/24"
)
