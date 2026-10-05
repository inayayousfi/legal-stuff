// Package caddy is the reverse proxy: the single address that serves every
// web page, with its HTTPS certificate and the admin sign-in check.
package caddy

import (
	"embed"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/platform"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

//go:embed compose.yaml
var compose embed.FS

const (
	modeKey = "ACCESS_MODE"
	hostKey = "ADMIN_ACCESS_HOST"

	Tailscale = "tailscale"
	Domain    = "domain"
	Local     = "local"
)

var App = &app.App{
	Name:       "caddy",
	Compose:    compose,
	Services:   func(*settings.Values) []string { return []string{"caddy"} },
	ConfigDirs: []string{"caddy/certs", "caddy/data", "caddy/config"},
	Required:   []string{modeKey, hostKey},
	Validate:   validate,
	Derive: func(v *settings.Values) {
		if v.Has(modeKey) {
			applyAccessValues(v)
		}
	},
	Configure: configure,
	Prepare:   prepare,
	Credentials: &app.CredentialGroup{Name: "access", Fields: []app.CredentialField{
		{Label: "Mode", Key: modeKey},
		{Label: "Address", Key: "ACCESS_URL"},
		{Label: "Media directory", Key: "MEDIA_DIR"},
		{Label: "Configuration directory", Key: "CONFIG_DIR"},
	}},
}

var modes = []flow.Option{
	{
		Value:  Tailscale,
		Label:  "Tailscale HTTPS certificate",
		Detail: "Pros: every device trusts the certificate, nothing to buy, works on Windows and Linux.\nCons: the pages are reachable only from devices signed in to your Tailscale network. Tailscale must be installed on this computer.",
	},
	{
		Value:  Domain,
		Label:  "Own domain with a Let's Encrypt certificate",
		Detail: "Pros: every device trusts the certificate, and the pages are reachable from anywhere.\nCons: you need a domain name and a router that forwards ports to this computer. The pages are reachable from the whole internet, protected by the admin sign-in.",
	},
	{
		Value:  Local,
		Label:  "Local network name without encryption",
		Detail: "Pros: nothing to buy or configure outside this computer.\nCons: passwords and pages cross your network unencrypted. Devices that cannot resolve .local names, including some Android phones, cannot open the pages.",
	},
}

var hostPattern = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)
var numericHost = regexp.MustCompile(`^[0-9.]+$`)

// ValidHost normalizes an address name and rejects bare names and IP addresses.
func ValidHost(host string) (string, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if !hostPattern.MatchString(host) || numericHost.MatchString(host) {
		return "", errors.New("The address must be a name containing a dot, such as media.example.com.")
	}
	return host, nil
}

func validate(v *settings.Values) error {
	switch v.Get(modeKey) {
	case Tailscale, Domain, Local:
	default:
		return errors.New("ACCESS_MODE must be tailscale, domain, or local.")
	}
	_, err := ValidHost(v.Get(hostKey))
	return err
}

func applyAccessValues(v *settings.Values) {
	scheme := "https"
	if v.Get(modeKey) == Local {
		scheme = "http"
	}
	v.Set("ACCESS_URL", scheme+"://"+v.Get(hostKey))
	if scheme == "https" {
		v.Set("TINYAUTH_SECURE_COOKIE", "true")
	} else {
		v.Set("TINYAUTH_SECURE_COOKIE", "false")
	}
}

func modeField(listed bool) flow.Field {
	return flow.Field{Key: "mode", Prompt: "Select an access mode", Kind: flow.Choice, Default: Tailscale, Options: modes, Listed: listed}
}

func hostField(prompt, fallback string) flow.Field {
	return flow.Field{Key: "host", Prompt: prompt, Default: fallback, Check: func(value string) (string, error) {
		host, err := ValidHost(value)
		if err != nil {
			return "", errors.New("Error: " + err.Error())
		}
		return host, nil
	}}
}

func configure(e *app.Env) error {
	v := e.Values
	if v.Has(modeKey) {
		if err := validate(v); err != nil {
			return err
		}
		applyAccessValues(v)
		return nil
	}
	screen := flow.Screen{
		Title:  "Secure access",
		Body:   "Every web page is reached through one address. Choose how that address is encrypted:",
		Fields: []flow.Field{modeField(false)},
	}
	var mode, host string
	for {
		answers, err := e.UI.Ask(screen)
		if err != nil {
			return err
		}
		mode = answers["mode"]
		if mode != Tailscale {
			break
		}
		host = platform.TailscaleDNSName(e.Shell)
		if host == "" {
			screen = flow.Screen{Body: "Tailscale is not installed or not signed in on this computer. Choose another mode.", Fields: []flow.Field{modeField(true)}}
			continue
		}
		if platform.TailscaleServeUsesHTTPS(e.Shell) {
			screen = flow.Screen{Body: platform.TailscaleServeConflict, Fields: []flow.Field{modeField(true)}}
			continue
		}
		if err := flow.Show(e.UI, flow.Screen{
			Title: "Tailscale HTTPS certificate",
			Body:  "1. Open the Tailscale admin console DNS page.\nhttps://login.tailscale.com/admin/dns\n2. Under HTTPS Certificates, click Enable HTTPS if it is not already enabled.",
			Wait:  "Tailscale HTTPS",
		}); err != nil {
			return err
		}
		break
	}
	switch mode {
	case Domain:
		answers, err := e.UI.Ask(flow.Screen{
			Title: "Own domain",
			Body: `1. At your domain provider, create an A record that points your chosen name to this network's public IP address.
2. On your router, forward TCP ports 80, 443, 5055, and 9091 to this computer.
3. The following prompt requests the name, for example media.example.com.

Let's Encrypt connects to this computer through those ports to issue the certificate. Until both steps are done, the pages do not open.`,
			Fields: []flow.Field{hostField("Domain name", "")},
		})
		if err != nil {
			return err
		}
		host = answers["host"]
	case Local:
		name, _ := os.Hostname()
		name, _, _ = strings.Cut(name, ".")
		answers, err := e.UI.Ask(flow.Screen{Fields: []flow.Field{hostField("Local network name", name+".local")}})
		if err != nil {
			return err
		}
		host = answers["host"]
	}
	v.Set(modeKey, mode)
	v.Set(hostKey, host)
	applyAccessValues(v)
	return nil
}

func prepare(e *app.Env) ([]string, error) {
	tailscale := e.Values.Get(modeKey) == Tailscale
	if tailscale && e.Progress != nil {
		err := e.Step("tailscale-operator", false, func() error {
			return platform.GrantTailscaleOperator(e.Shell, e.UI.Say)
		})
		if err != nil {
			return nil, err
		}
	}
	certChanged := false
	if tailscale {
		var err error
		certChanged, err = platform.TailscaleCert(e.Shell, e.ConfigPath("caddy", "certs", "cert.pem"), e.ConfigPath("caddy", "certs", "key.pem"), e.Values.Get(hostKey))
		if err != nil {
			return nil, err
		}
	}
	configChanged, err := settings.WriteIfChanged(e.ConfigPath("caddy", "Caddyfile"), []byte(Caddyfile(e.Values, Routes(e.Apps, e.Values))))
	if err != nil {
		return nil, err
	}
	if certChanged || configChanged {
		return []string{"caddy"}, nil
	}
	return nil, nil
}

// Routes collects every app's routes in registry order.
func Routes(apps []*app.App, v *settings.Values) []app.Route {
	var routes []app.Route
	for _, a := range apps {
		if a.Routes != nil {
			routes = append(routes, a.Routes(v)...)
		}
	}
	return routes
}

// InternalNames lists the network names Caddy answers for inside the stack network.
func InternalNames(apps []*app.App, v *settings.Values) []string {
	var names []string
	for _, route := range Routes(apps, v) {
		if route.Internal != nil {
			names = append(names, route.Internal.Name)
		}
	}
	return names
}
