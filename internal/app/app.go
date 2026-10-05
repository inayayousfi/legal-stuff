// Package app defines what one application contributes to the stack.
// Each application lives in its own package under internal/apps and is
// listed once in internal/apps/registry.go.
package app

import (
	"crypto/rand"
	"encoding/base64"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/settings"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

// App is everything one application adds. Every field except Name is optional.
type App struct {
	// Name identifies the app and names its Compose fragment.
	Name string

	// Settings are the .env values this app owns. Only this app changes them;
	// other apps read them through functions this app's package exports.
	Settings []Setting
	// Renamed maps names that earlier versions saved to this app's current
	// setting names. RetiredSettings and RetiredConfigDirs are what an app
	// this one replaced left behind under .env and CONFIG_DIR. Setup and
	// start move the renamed values and remove the retired ones.
	Renamed           map[string]string
	RetiredSettings   []string
	RetiredConfigDirs []string

	// Compose holds compose.yaml and any build files for this app's
	// containers. The file is a text/template rendered with ComposeData.
	Compose fs.FS

	// Services lists the Compose services this app runs with the current
	// values. BehindVPN services start only once every Started hook returns.
	Services  func(v Values) []string
	BehindVPN []string
	// Optional lists services that run only with some values. The stack
	// stops the ones that Services does not return.
	Optional []string

	// ConfigDirs and MediaDirs are created under CONFIG_DIR and MEDIA_DIR
	// before the stack starts.
	ConfigDirs []string
	MediaDirs  []string

	// Validate checks the content of this app's saved settings before a start.
	Validate func(v Values) error

	// Derive fills settings computed from other values or generated at
	// random. It reads nothing but the values. It runs before every start.
	Derive func(v Values)

	// Configure runs only during setup, before the stack starts. It asks the
	// questions this app needs, asking nothing when the saved values are
	// already complete, and performs this app's one-time host preparation
	// as recorded setup steps.
	Configure func(e *Env) error

	// Prepare brings this app's files and generated settings up to date
	// before its containers start, saving values it changed. It may stop
	// this app's own containers when a file can change only while they are
	// stopped. It returns the services that must restart because a file
	// they read changed.
	Prepare func(e *Env) (restart []string, err error)

	// Started runs once the containers that do not use the VPN are up. The
	// services behind the VPN start after every Started hook returns.
	Started func(e *Env) error

	// AfterStart runs at the end of the start command.
	AfterStart func(e *Env) error

	// Setup is this app's guided setup, run in registry order once the
	// stack is up. It records finished steps with Env.Step.
	Setup func(e *Env) error

	// Status describes this app's own health for a status command.
	Status func(e *Env) (string, error)

	// Routes are the web addresses Caddy serves for this app.
	Routes func(v Values) []Route

	// Tiles are this app's entries on the Homepage dashboard.
	Tiles func(v Values) []Tile

	// Credentials is the group of saved values that the credentials command
	// shows and changes.
	Credentials *CredentialGroup
}

// Setting is one .env value an app owns.
type Setting struct {
	Key string
	// Example is the value shown in .env.example.
	Example string
	// Required settings must be set before the stack can start.
	Required bool
}

// Owns reports whether key is one of this app's settings.
func (a *App) Owns(key string) bool {
	for _, setting := range a.Settings {
		if setting.Key == key {
			return true
		}
	}
	return false
}

// Values reads every saved value and changes only the settings its owner declares.
type Values struct {
	saved *settings.Values
	owner *App
}

// ValuesFor gives owner its view of saved.
func ValuesFor(saved *settings.Values, owner *App) Values {
	return Values{saved: saved, owner: owner}
}

func (v Values) Get(key string) string { return v.saved.Get(key) }

func (v Values) Has(key string) bool { return v.saved.Has(key) }

// Set changes one of the owner's settings. Setting another app's value is a
// programming error and stops the program.
func (v Values) Set(key, value string) {
	v.mustOwn(key)
	v.saved.Set(key, value)
}

// SetDefault sets one of the owner's settings only when it has no entry yet.
func (v Values) SetDefault(key, value string) {
	v.mustOwn(key)
	v.saved.SetDefault(key, value)
}

func (v Values) mustOwn(key string) {
	if !v.owner.Owns(key) {
		panic("app " + v.owner.Name + " cannot set " + key + ", which it does not own")
	}
}

// Route is one address that Caddy forwards to a container.
type Route struct {
	// Name is the application's name as people know it, such as Sonarr.
	// Named routes appear in the admin sign-in's setup text.
	Name string
	// Path is the address path, such as /sonarr.
	Path string
	// Upstream is the container address, such as sonarr-app:8989.
	Upstream string
	// Public routes skip the admin sign-in.
	Public bool
	// SignIn marks the route whose upstream checks the admin sign-in for
	// every route that is not public. Check is the path it answers on.
	SignIn bool
	Check  string
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
	// Position orders the tiles within their group, lowest first.
	Position int
	// YAML is the entry's text, indented as a list item under its group.
	YAML string
}

// CredentialGroup lists saved values shown together by the credentials command.
type CredentialGroup struct {
	Name   string
	Fields []CredentialField
	// Visible, when set, filters fields based on other values.
	Visible func(v Values, f CredentialField) bool
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
	// Routes are every app's routes with the current values.
	Routes []Route
}

// Env gives a hook its app's values, the stack's files, and the programs it runs.
type Env struct {
	Root   string
	Values Values
	Shell  shell.Shell
	UI     flow.UI
	// Routes and Tiles are every app's contributions with the current
	// values, for the apps that assemble them.
	Routes []Route
	Tiles  []Tile
	// Progress is set during setup only.
	Progress *settings.Progress
	// SaveValues writes every value to .env.
	SaveValues func() error
	// Sleep waits between checks while a hook polls a container.
	Sleep func(time.Duration)
}

func (e *Env) ConfigDir() string { return e.Values.Get("CONFIG_DIR") }

func (e *Env) MediaDir() string { return e.Values.Get("MEDIA_DIR") }

// ConfigPath joins parts under CONFIG_DIR.
func (e *Env) ConfigPath(parts ...string) string {
	return filepath.Join(append([]string{e.ConfigDir()}, parts...)...)
}

// Compose runs docker compose with args in the stack folder.
func (e *Env) Compose(args ...string) error { return shell.Compose(e.Shell, args...) }

// Step runs a guided step once, recording it in the setup progress. It is
// available during setup only. Steps already recorded are skipped unless
// again reports that they must repeat.
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

// RandomToken returns a random secret of n bytes, encoded without characters
// that .env or URLs would need to escape.
func RandomToken(n int) string {
	bytes := make([]byte, n)
	rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}
