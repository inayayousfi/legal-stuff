// Package vpn is the gateway that carries qBittorrent and Prowlarr traffic:
// Gluetun with a VPN provider, or a Tailscale exit node. With Gluetun it also
// runs the VPN country page.
package vpn

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/files"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

//go:embed compose.yaml countrypage/Dockerfile countrypage/main.go
var compose embed.FS

const (
	Gluetun      = "gluetun"
	Tailscale    = "tailscale-vpn"
	gatewayKey   = "VPN_GATEWAY_SERVICE"
	countriesKey = "VPN_SERVER_COUNTRIES"
	countryPage  = "vpn-country"
)

// Gateway returns the selected gateway service, whose network qBittorrent and Prowlarr share.
func Gateway(v app.Values) string { return v.Get(gatewayKey) }

var App = &app.App{
	Name: "vpn",
	Settings: []app.Setting{
		{Key: gatewayKey, Example: Gluetun, Required: true},
		{Key: "VPN_SERVICE_PROVIDER", Example: "nordvpn"},
		{Key: "VPN_TYPE", Example: "openvpn"},
		{Key: countriesKey},
		{Key: "VPN_OPENVPN_USER", Example: "replace_with_your_vpn_service_username"},
		{Key: "VPN_OPENVPN_PASSWORD", Example: "replace_with_your_vpn_service_password"},
		{Key: "GLUETUN_API_KEY", Example: "generated_automatically_by_setup"},
		{Key: "GLUETUN_CONTROL_KEY", Example: "generated_automatically_by_setup"},
		{Key: "TAILSCALE_AUTH_KEY"},
		{Key: "TAILSCALE_EXIT_NODE"},
	},
	Compose: compose,
	Services: func(v app.Values) []string {
		if Gateway(v) == Gluetun {
			return []string{Gluetun, countryPage}
		}
		return []string{Tailscale}
	},
	Optional:   []string{Gluetun, Tailscale, countryPage},
	ConfigDirs: []string{"gluetun", "tailscale", countryPage},
	Validate:   validate,
	Configure:  configure,
	Derive:     derive,
	Prepare:    prepare,
	Started: func(e *app.Env) error {
		if Gateway(e.Values) == Gluetun {
			SaveCountryList(e.Shell, e.Values, e.ConfigDir())
		}
		return WaitUntilReady(e)
	},
	Status: Status,
	Routes: func(v app.Values) []app.Route {
		if Gateway(v) != Gluetun {
			return nil
		}
		return []app.Route{{Name: "VPN country page", Path: "/vpn-country", Upstream: "vpn-country:8090", StripPrefix: true}}
	},
	Tiles: func(v app.Values) []app.Tile {
		if Gateway(v) == Gluetun {
			return []app.Tile{{Group: "Admin", Position: 5, YAML: `    - Gluetun:
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
		return []app.Tile{{Group: "Admin", Position: 5, YAML: `    - Tailscale:
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
			{Label: "Server countries", Key: countriesKey},
			{Label: "Service username", Key: "VPN_OPENVPN_USER", Kind: app.PlainText, UsedByStack: true},
			{Label: "Service password", Key: "VPN_OPENVPN_PASSWORD", Kind: app.Password, UsedByStack: true},
			{Label: "Exit node", Key: "TAILSCALE_EXIT_NODE", Kind: app.PlainText, UsedByStack: true},
			{Label: "Auth key", Key: "TAILSCALE_AUTH_KEY", Kind: app.Password, UsedByStack: true},
		},
		Visible: func(v app.Values, f app.CredentialField) bool {
			tailscaleField := f.Key == "TAILSCALE_EXIT_NODE" || f.Key == "TAILSCALE_AUTH_KEY"
			return f.Key == gatewayKey || tailscaleField == (Gateway(v) == Tailscale)
		},
	},
}

func validate(v app.Values) error {
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
	v.SetDefault(countriesKey, "")
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

// derive generates Gluetun's API keys.
func derive(v app.Values) {
	if Gateway(v) != Gluetun {
		return
	}
	for _, key := range []string{"GLUETUN_API_KEY", "GLUETUN_CONTROL_KEY"} {
		if !v.Has(key) {
			v.Set(key, app.RandomToken(32))
		}
	}
}

// prepare copies the country chosen on the VPN country page into the values
// Gluetun starts with, and writes Gluetun's API access file.
func prepare(e *app.Env) ([]string, error) {
	if Gateway(e.Values) != Gluetun {
		return nil, nil
	}
	if ApplySavedCountry(e.Values, e.ConfigDir()) {
		if err := e.SaveValues(); err != nil {
			return nil, err
		}
	}
	changed, err := WriteGluetunAuth(e.ConfigDir(), e.Values)
	if err != nil || !changed {
		return nil, err
	}
	return []string{Gluetun}, nil
}

// ApplySavedCountry copies the VPN country page's saved selection into
// VPN_SERVER_COUNTRIES. It reports whether the value changed.
func ApplySavedCountry(v app.Values, configDir string) bool {
	if Gateway(v) != Gluetun {
		return false
	}
	content, err := os.ReadFile(filepath.Join(configDir, countryPage, "selection.json"))
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
	if v.Get(countriesKey) == selected {
		return false
	}
	v.Set(countriesKey, selected)
	return true
}

// WriteGluetunAuth gives Homepage read access and the country page control
// access to Gluetun's API. It reports whether the file changed.
func WriteGluetunAuth(configDir string, v app.Values) (bool, error) {
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
	return files.WriteIfChanged(filepath.Join(configDir, "gluetun", "auth", "config.toml"), []byte(strings.Join(blocks, "\n")))
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
func SaveCountryList(s shell.Shell, v app.Values, configDir string) {
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
	files.WriteIfChanged(filepath.Join(configDir, countryPage, "countries.json"), content)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// Ready reports whether the gateway carries traffic: Gluetun is healthy, or
// the Tailscale exit node is online. It fails only when a command cannot run.
func Ready(s shell.Shell, gateway string) (bool, error) {
	if gateway == Gluetun {
		result, err := shell.Capture(s, "docker", "inspect", Gluetun, "--format", "{{.State.Health.Status}}")
		return err == nil && result.Code == 0 && strings.TrimSpace(result.Stdout) == "healthy", err
	}
	result, err := shell.Capture(s, "docker", "exec", Tailscale, "tailscale", "status", "--json")
	if err != nil || result.Code != 0 {
		return false, err
	}
	var status struct {
		ExitNodeStatus struct{ Online bool }
	}
	return json.Unmarshal([]byte(result.Stdout), &status) == nil && status.ExitNodeStatus.Online, nil
}

// WaitUntilReady waits without a time limit, reporting the wait once. It
// stops when a command cannot run, such as after a cancellation.
func WaitUntilReady(e *app.Env) error {
	gateway := Gateway(e.Values)
	ready, err := Ready(e.Shell, gateway)
	if err != nil || ready {
		return err
	}
	e.UI.Say("Waiting for the VPN connection. qBittorrent and Prowlarr will start when it connects.")
	for {
		e.Sleep(2 * time.Second)
		if ready, err := Ready(e.Shell, gateway); err != nil || ready {
			return err
		}
	}
}

// Status describes the gateway's connection.
func Status(e *app.Env) (string, error) {
	v := e.Values
	switch Gateway(v) {
	case Gluetun:
		ready, err := Ready(e.Shell, Gluetun)
		if err != nil {
			return "", err
		}
		state := "not healthy"
		if ready {
			state = "healthy"
		}
		text := "Gluetun VPN: " + state
		if v.Has(countriesKey) {
			text += "\nSelected countries:\n" + v.Get(countriesKey)
		}
		return text, nil
	case Tailscale:
		ready, err := Ready(e.Shell, Tailscale)
		if err != nil {
			return "", err
		}
		state := "not online"
		if ready {
			state = "online"
		}
		return "Tailscale exit node: " + state + "\nSelected exit node:\n" + v.Get("TAILSCALE_EXIT_NODE"), nil
	}
	return "", errors.New("Unknown VPN gateway.")
}
