// Package vpn is the gateway that carries qBittorrent and Prowlarr traffic:
// Gluetun with a VPN provider, or a Tailscale exit node. With Gluetun it also
// runs the VPN country page.
package vpn

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/settings"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

//go:embed compose.yaml countrypage/Dockerfile countrypage/main.go
var compose embed.FS

const (
	Gluetun     = "gluetun"
	Tailscale   = "tailscale-vpn"
	gatewayKey  = "VPN_GATEWAY_SERVICE"
	countryPage = "vpn-country"
)

// Gateway returns the selected gateway service.
func Gateway(v *settings.Values) string { return v.Get(gatewayKey) }

var App = &app.App{
	Name:    "vpn",
	Compose: compose,
	Services: func(v *settings.Values) []string {
		if Gateway(v) == Gluetun {
			return []string{Gluetun, countryPage}
		}
		return []string{Tailscale}
	},
	Optional:   []string{Gluetun, Tailscale, countryPage},
	ConfigDirs: []string{"gluetun", "tailscale", countryPage},
	Required:   []string{gatewayKey},
	Validate:   validate,
	Configure:  configure,
	Derive:     derive,
	Prepare: func(e *app.Env) ([]string, error) {
		if Gateway(e.Values) != Gluetun {
			return nil, nil
		}
		changed, err := WriteGluetunAuth(e.ConfigDir(), e.Values)
		if err != nil || !changed {
			return nil, err
		}
		return []string{Gluetun}, nil
	},
	Started: func(e *app.Env) error {
		if Gateway(e.Values) == Gluetun {
			SaveCountryList(e.Shell, e.Values)
		}
		return nil
	},
	Routes: func(v *settings.Values) []app.Route {
		if Gateway(v) != Gluetun {
			return nil
		}
		return []app.Route{{Path: "/vpn-country", Upstream: "vpn-country:8090", StripPrefix: true}}
	},
	Tiles: func(v *settings.Values) []app.Tile {
		if Gateway(v) == Gluetun {
			return []app.Tile{{Group: "Admin", YAML: `    - Gluetun:
        icon: gluetun.png
        description: qBittorrent and Prowlarr VPN connection
        server: media-stack
        container: gluetun
        href: "{{HOMEPAGE_VAR_URL}}/vpn-country/"
        widget:
          type: gluetun
          url: http://gluetun:8000
          key: "{{HOMEPAGE_VAR_GLUETUN_API_KEY}}"
`}}
		}
		return []app.Tile{{Group: "Admin", YAML: `    - Tailscale:
        icon: tailscale.png
        description: qBittorrent and Prowlarr VPN connection
        server: media-stack
        container: tailscale-vpn
`}}
	},
	Credentials: &app.CredentialGroup{
		Name: "vpn",
		Fields: []app.CredentialField{
			{Label: "Provider", Key: "VPN_SERVICE_PROVIDER"},
			{Label: "Gateway", Key: gatewayKey},
			{Label: "Server countries", Key: "VPN_SERVER_COUNTRIES"},
			{Label: "Service username", Key: "VPN_OPENVPN_USER", Kind: app.PlainText, UsedByStack: true},
			{Label: "Service password", Key: "VPN_OPENVPN_PASSWORD", Kind: app.Password, UsedByStack: true},
			{Label: "Exit node", Key: "TAILSCALE_EXIT_NODE", Kind: app.PlainText, UsedByStack: true},
			{Label: "Auth key", Key: "TAILSCALE_AUTH_KEY", Kind: app.Password, UsedByStack: true},
		},
		Visible: func(v *settings.Values, f app.CredentialField) bool {
			tailscaleField := f.Key == "TAILSCALE_EXIT_NODE" || f.Key == "TAILSCALE_AUTH_KEY"
			return f.Key == gatewayKey || tailscaleField == (Gateway(v) == Tailscale)
		},
	},
}

func validate(v *settings.Values) error {
	var required []string
	switch Gateway(v) {
	case Gluetun:
		required = []string{"VPN_SERVICE_PROVIDER", "VPN_TYPE", "VPN_OPENVPN_USER", "VPN_OPENVPN_PASSWORD"}
		if v.Get("VPN_TYPE") != "openvpn" {
			return errors.New("The guided Gluetun providers currently use OpenVPN only.")
		}
	case Tailscale:
		required = []string{"TAILSCALE_AUTH_KEY", "TAILSCALE_EXIT_NODE"}
	default:
		return errors.New("VPN gateway must be gluetun or tailscale-vpn.")
	}
	var missing []string
	for _, key := range required {
		if !v.Has(key) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return errors.New("Missing required VPN values: " + strings.Join(missing, ", "))
	}
	return nil
}

var providers = []flow.Option{
	{Value: "nordvpn", Label: "NordVPN"},
	{Value: "protonvpn", Label: "Proton VPN"},
	{Value: "surfshark", Label: "Surfshark"},
	{Value: "private internet access", Label: "Private Internet Access"},
	{Value: "tailscale", Label: "Tailscale exit node"},
	{Value: "other", Label: "Other Gluetun OpenVPN provider"},
}

func configure(e *app.Env) error {
	v := e.Values
	gateway := Gateway(v)
	provider := strings.ToLower(strings.TrimSpace(v.Get("VPN_SERVICE_PROVIDER")))
	if gateway == "" && provider != "" {
		gateway = Gluetun
	}
	if gateway == "" {
		answers, err := e.UI.Ask(flow.Screen{
			Title:  "VPN provider",
			Fields: []flow.Field{{Key: "provider", Prompt: "Select a VPN provider", Kind: flow.Choice, Default: "nordvpn", Options: providers}},
		})
		if err != nil {
			return err
		}
		provider = answers["provider"]
		gateway = Gluetun
		if provider == "tailscale" {
			gateway = Tailscale
		}
		if provider == "other" {
			answers, err := e.UI.Ask(flow.Screen{Fields: []flow.Field{{
				Key: "provider", Prompt: "Gluetun provider identifier", Check: flow.Required("Provider identifier cannot be empty."),
			}}})
			if err != nil {
				return err
			}
			provider = strings.ToLower(answers["provider"])
		}
	}

	v.Set(gatewayKey, gateway)
	if gateway == Tailscale {
		if v.Has("TAILSCALE_AUTH_KEY") && v.Has("TAILSCALE_EXIT_NODE") {
			return nil
		}
		var fields []flow.Field
		if !v.Has("TAILSCALE_AUTH_KEY") {
			fields = append(fields, flow.Field{Key: "TAILSCALE_AUTH_KEY", Prompt: "Tailscale auth key", Kind: flow.Secret, Check: flow.Required("Password cannot be empty.")})
		}
		if !v.Has("TAILSCALE_EXIT_NODE") {
			fields = append(fields, flow.Field{Key: "TAILSCALE_EXIT_NODE", Prompt: "Tailscale exit-node name or IP", Check: flow.Required("Exit-node identifier cannot be empty.")})
		}
		return askInto(e, tailscaleGuide(fields))
	}

	if provider == "" {
		provider = "nordvpn"
	}
	v.Set("VPN_SERVICE_PROVIDER", provider)
	v.Set("VPN_TYPE", "openvpn")
	v.SetDefault("VPN_SERVER_COUNTRIES", "")
	if v.Has("VPN_OPENVPN_USER") && v.Has("VPN_OPENVPN_PASSWORD") {
		return nil
	}
	var fields []flow.Field
	if !v.Has("VPN_OPENVPN_USER") {
		fields = append(fields, flow.Field{Key: "VPN_OPENVPN_USER", Prompt: "VPN service username", Check: flow.Required("Username cannot be empty.")})
	}
	if !v.Has("VPN_OPENVPN_PASSWORD") {
		fields = append(fields, flow.Field{Key: "VPN_OPENVPN_PASSWORD", Prompt: "VPN service password", Kind: flow.Secret, Check: flow.Required("Password cannot be empty.")})
	}
	return askInto(e, providerGuide(provider, fields))
}

func askInto(e *app.Env, screen flow.Screen) error {
	answers, err := e.UI.Ask(screen)
	if err != nil {
		return err
	}
	for key, value := range answers {
		e.Values.Set(key, value)
	}
	return nil
}

type providerHelp struct{ source, location, warning string }

var providerGuides = map[string]providerHelp{
	"nordvpn": {
		"NordVPN generates the OpenVPN service username and password automatically.",
		"Nord Account > NordVPN > Advanced Settings > Set up NordVPN manually > Service credentials",
		"Do not use the email address and password used to sign in to the NordVPN application.",
	},
	"protonvpn": {
		"Proton VPN generates a separate OpenVPN username and password automatically.",
		"Proton Account > VPN > OpenVPN / IKEv2 username",
		"Do not use the email address and password used to sign in to Proton.",
	},
	"surfshark": {
		"Surfshark generates the OpenVPN service username and password automatically.",
		"Surfshark Account > VPN > Manual setup > Desktop or mobile > OpenVPN > Credentials",
		"Do not use the email address and password used to sign in to Surfshark.",
	},
	"private internet access": {
		"Private Internet Access assigns a service username beginning with p and a service password.",
		"The purchase email or Private Internet Access Client Control Panel",
		"Use the VPN service credentials, not unrelated device or operating-system credentials.",
	},
}

var otherProviderGuide = providerHelp{
	"Your provider supplies the OpenVPN service username and password.",
	"The provider's manual OpenVPN setup documentation",
	"Use service credentials intended for manual OpenVPN connections.",
}

func providerGuide(provider string, fields []flow.Field) flow.Screen {
	help, ok := providerGuides[provider]
	if !ok {
		help = otherProviderGuide
	}
	return flow.Screen{
		Title: "VPN kill-switch setup",
		Body: `qBittorrent will use Gluetun's VPN connection. If the VPN disconnects, Gluetun's firewall blocks qBittorrent network traffic.

` + help.source + `

1. Open ` + help.location + `
2. Keep the generated service username and password available.
3. The following prompts will request those 2 values.

` + help.warning,
		Fields: fields,
	}
}

func tailscaleGuide(fields []flow.Field) flow.Screen {
	return flow.Screen{
		Title: "Tailscale exit-node setup",
		Body: `qBittorrent will share a separate Tailscale container and use one fixed exit node. Setup starts qBittorrent only after that exit node reports online.

1. Open the Tailscale admin console Keys page.
https://login.tailscale.com/admin/settings/keys
2. Generate a one-off, non-ephemeral auth key. Enable Pre-approved if device approval is enabled.
3. Open the Machines page and identify the exact machine name or Tailscale IP of an available exit node.
https://login.tailscale.com/admin/machines
4. The following prompts will request the auth key and exit-node identifier.

If the Tailscale gateway later restarts, qBittorrent remains blocked but may need a stack restart to recover its network connection.`,
		Fields: fields,
	}
}

// derive generates Gluetun's API keys and copies the country chosen on the
// VPN country page into the values Gluetun starts with.
func derive(v *settings.Values) {
	if Gateway(v) != Gluetun {
		return
	}
	for _, key := range []string{"GLUETUN_API_KEY", "GLUETUN_CONTROL_KEY"} {
		if !v.Has(key) {
			v.Set(key, randomToken())
		}
	}
	ApplySavedCountry(v)
}

func randomToken() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// ApplySavedCountry copies the VPN country page's saved selection into
// VPN_SERVER_COUNTRIES. It reports whether the value changed.
func ApplySavedCountry(v *settings.Values) bool {
	if Gateway(v) != Gluetun {
		return false
	}
	content, err := os.ReadFile(filepath.Join(v.Get("CONFIG_DIR"), countryPage, "selection.json"))
	if err != nil {
		return false
	}
	var selection struct {
		Countries []any `json:"countries"`
	}
	if json.Unmarshal(content, &selection) != nil || selection.Countries == nil {
		return false
	}
	var countries []string
	for _, country := range selection.Countries {
		if name, ok := country.(string); ok {
			countries = append(countries, name)
		}
	}
	selected := strings.Join(countries, ",")
	if v.Get("VPN_SERVER_COUNTRIES") == selected {
		return false
	}
	v.Set("VPN_SERVER_COUNTRIES", selected)
	return true
}

// WriteGluetunAuth gives Homepage read access and the country page control
// access to Gluetun's API. It reports whether the file changed.
func WriteGluetunAuth(configDir string, v *settings.Values) (bool, error) {
	roles := []struct {
		name   string
		routes []string
		key    string
	}{
		{"homepage", []string{"GET /v1/publicip/ip"}, v.Get("GLUETUN_API_KEY")},
		{countryPage, []string{"GET /v1/publicip/ip", "GET /v1/vpn/settings", "PUT /v1/vpn/settings"}, v.Get("GLUETUN_CONTROL_KEY")},
	}
	var blocks []string
	for _, role := range roles {
		name, _ := json.Marshal(role.name)
		routes, _ := json.Marshal(role.routes)
		key, _ := json.Marshal(role.key)
		blocks = append(blocks, fmt.Sprintf("[[roles]]\nname = %s\nroutes = %s\nauth = \"apikey\"\napikey = %s\n", name, strings.ReplaceAll(string(routes), `","`, `", "`), key))
	}
	return settings.WriteIfChanged(filepath.Join(configDir, "gluetun", "auth", "config.toml"), []byte(strings.Join(blocks, "\n")))
}

// Countries returns the sorted, unique countries that offer OpenVPN servers.
func Countries(servers []map[string]any) []string {
	seen := map[string]bool{}
	var countries []string
	for _, server := range servers {
		country, _ := server["country"].(string)
		if server["vpn"] == "openvpn" && country != "" && !seen[country] {
			seen[country] = true
			countries = append(countries, country)
		}
	}
	slices.Sort(countries)
	return countries
}

// SaveCountryList asks Gluetun for its server list and saves the countries
// the VPN country page offers. Failures leave the previous list in place.
func SaveCountryList(s shell.Shell, v *settings.Values) {
	flag := "-" + strings.ReplaceAll(v.Get("VPN_SERVICE_PROVIDER"), " ", "-")
	result, err := shell.Capture(s, "docker", "exec", Gluetun, "sh", "-c",
		"/gluetun-entrypoint format-servers "+shellQuote(flag)+" -format json -output /tmp/servers.json >/dev/null && cat /tmp/servers.json")
	if err != nil || result.Code != 0 {
		return
	}
	var servers []map[string]any
	if json.Unmarshal([]byte(result.Stdout), &servers) != nil {
		return
	}
	content, _ := json.Marshal(Countries(servers))
	path := filepath.Join(v.Get("CONFIG_DIR"), countryPage, "countries.json")
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		os.WriteFile(path, content, 0o644)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// Ready reports whether the gateway carries traffic: Gluetun is healthy, or
// the Tailscale exit node is online.
func Ready(s shell.Shell, gateway string) bool {
	if gateway == Gluetun {
		result, err := shell.Capture(s, "docker", "inspect", Gluetun, "--format", "{{.State.Health.Status}}")
		return err == nil && result.Code == 0 && strings.TrimSpace(result.Stdout) == "healthy"
	}
	result, err := shell.Capture(s, "docker", "exec", Tailscale, "tailscale", "status", "--json")
	if err != nil || result.Code != 0 {
		return false
	}
	var status struct {
		ExitNodeStatus struct{ Online bool }
	}
	return json.Unmarshal([]byte(result.Stdout), &status) == nil && status.ExitNodeStatus.Online
}

// Sleep is replaced by tests.
var Sleep = time.Sleep

// WaitUntilReady waits without a time limit, reporting the wait once.
func WaitUntilReady(s shell.Shell, v *settings.Values, say func(string)) {
	gateway := Gateway(v)
	if Ready(s, gateway) {
		return
	}
	say("Waiting for the VPN connection. qBittorrent and Prowlarr will start when it connects.")
	for !Ready(s, gateway) {
		Sleep(2 * time.Second)
	}
}

// Status describes the gateway's connection.
func Status(s shell.Shell, v *settings.Values) (string, error) {
	switch Gateway(v) {
	case Gluetun:
		state := "not healthy"
		if Ready(s, Gluetun) {
			state = "healthy"
		}
		text := "Gluetun VPN: " + state
		if v.Has("VPN_SERVER_COUNTRIES") {
			text += "\nSelected countries:\n" + v.Get("VPN_SERVER_COUNTRIES")
		}
		return text, nil
	case Tailscale:
		state := "not online"
		if Ready(s, Tailscale) {
			state = "online"
		}
		return "Tailscale exit node: " + state + "\nSelected exit node:\n" + v.Get("TAILSCALE_EXIT_NODE"), nil
	}
	return "", errors.New("Unknown VPN gateway.")
}
