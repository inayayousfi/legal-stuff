// Package authentik is the sign-in service. It checks the admin sign-in for
// every page that is not public, and it is where people add sign-in methods
// such as Google accounts, an email server, or passkeys.
package authentik

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/apps/caddy"
	"github.com/inayayousfi/legal-stuff/internal/files"
	"github.com/inayayousfi/legal-stuff/internal/flow"
)

//go:embed compose.yaml
var compose embed.FS

const (
	userKey     = "ADMIN_USER"
	passwordKey = "ADMIN_PASS"
	// accountKey is the admin username last written into the blueprint;
	// retiredKey lists earlier admin usernames, kept deactivated.
	accountKey = "AUTHENTIK_ADMIN_ACCOUNT"
	retiredKey = "AUTHENTIK_RETIRED_ADMINS"
	// signInPort serves Authentik's own pages, including the sign-in page.
	signInPort = 9091
	// address is where Caddy asks whether a request is signed in.
	address = "authentik-server:9000"
	// outpostPath is where Authentik's built-in proxy answers on the main address.
	outpostPath = "/outpost.goauthentik.io"
)

// generated are secrets setup creates once. The bootstrap password locks
// Authentik's default akadmin account, so its public first-run page closes.
var generated = []string{"AUTHENTIK_SECRET_KEY", "AUTHENTIK_DB_PASSWORD", "AUTHENTIK_BOOTSTRAP_PASSWORD"}

var App = &app.App{
	Name: "authentik",
	Settings: []app.Setting{
		{Key: userKey, Example: "replace_with_your_admin_username", Required: true},
		{Key: passwordKey, Example: "replace_with_your_admin_password", Required: true},
		{Key: generated[0], Example: "generated_automatically_by_setup"},
		{Key: generated[1], Example: "generated_automatically_by_setup"},
		{Key: generated[2], Example: "generated_automatically_by_setup"},
		{Key: accountKey, Example: "generated_automatically_by_setup"},
		{Key: retiredKey, Example: "[]"},
	},
	// Tinyauth was the sign-in service before Authentik.
	RetiredSettings:   []string{"TINYAUTH_USERS", "ADMIN_LOGIN_FINGERPRINT", "TINYAUTH_SECURE_COOKIE"},
	RetiredConfigDirs: []string{"tinyauth"},
	Compose:           compose,
	Services:          func(app.Values) []string { return []string{"authentik-db", "authentik-server", "authentik-worker"} },
	ConfigDirs:        []string{"authentik/data", "authentik/blueprints"},
	Derive: func(v app.Values) {
		for _, key := range generated {
			if !v.Has(key) {
				v.Set(key, app.RandomToken(48))
			}
		}
	},
	Configure: configure,
	Prepare: func(e *app.Env) ([]string, error) {
		if trackAdmin(e.Values) {
			if err := e.SaveValues(); err != nil {
				return nil, err
			}
		}
		_, err := files.WriteIfChanged(e.ConfigPath("authentik", "blueprints", "selfhost.yaml"), []byte(Blueprint(e.Values)))
		return nil, err
	},
	Setup: func(e *app.Env) error {
		return e.Step("authentik", false, func() error { return flow.Show(e.UI, Guide(e.Values)) })
	},
	Routes: func(app.Values) []app.Route {
		return []app.Route{
			{Upstream: address, Public: true, SignIn: true, Check: outpostPath + "/auth/caddy", Port: signInPort},
			{Path: outpostPath, Upstream: address, Public: true},
		}
	},
	Tiles: func(app.Values) []app.Tile {
		return []app.Tile{{Group: "Admin", Position: 6, YAML: `    - Authentik:
        icon: authentik.png
        server: media-stack
        container: authentik-server
        href: "{{HOMEPAGE_VAR_URL}}:` + strconv.Itoa(signInPort) + `/if/admin/"
        description: Manage sign-in methods and users
`}}
	},
	Credentials: &app.CredentialGroup{Name: "admin", Fields: []app.CredentialField{
		{Label: "Username", Key: userKey, Kind: app.PlainText, UsedByStack: true},
		{Label: "Password", Key: passwordKey, Kind: app.Password, UsedByStack: true},
	}},
}

// User and Password are the admin sign-in.
func User(v app.Values) string { return v.Get(userKey) }

func Password(v app.Values) string { return v.Get(passwordKey) }

// URL is the address of Authentik's own pages.
func URL(v app.Values) string { return caddy.URL(v) + ":" + strconv.Itoa(signInPort) }

// trackAdmin retires the previous admin username when it changed, so the
// blueprint keeps that account deactivated. It reports whether values changed.
func trackAdmin(v app.Values) bool {
	current, previous := User(v), v.Get(accountKey)
	retired := retiredAdmins(v)
	if previous != "" && previous != current && !slices.Contains(retired, previous) {
		retired = append(retired, previous)
	}
	retired = slices.DeleteFunc(retired, func(name string) bool { return name == current })
	encoded, _ := json.Marshal(retired)
	changed := previous != current || v.Get(retiredKey) != string(encoded)
	v.Set(accountKey, current)
	v.Set(retiredKey, string(encoded))
	return changed
}

func retiredAdmins(v app.Values) []string {
	retired := []string{}
	json.Unmarshal([]byte(v.Get(retiredKey)), &retired)
	return retired
}

func configure(e *app.Env) error {
	if e.Values.Has(userKey) && e.Values.Has(passwordKey) {
		return nil
	}
	var fields []flow.Field
	if !e.Values.Has(userKey) {
		fields = append(fields, flow.UsernameField(userKey, "Admin"))
	}
	if !e.Values.Has(passwordKey) {
		fields = append(fields, flow.PasswordField(passwordKey, "Admin"))
	}
	answers, err := e.UI.Ask(flow.Screen{
		Title:  "Admin sign-in",
		Body:   "Create one username and password. " + coverage(e.Routes) + "\n\nThe following prompts request the username and password.",
		Fields: fields,
	})
	if err != nil {
		return err
	}
	for key, value := range answers {
		e.Values.Set(key, value)
	}
	return nil
}

// coverage names the applications the sign-in protects and those that keep their own login.
func coverage(routes []app.Route) string {
	var protected, own []string
	for _, route := range routes {
		switch {
		case route.Name == "":
		case route.Public:
			own = append(own, route.Name)
		default:
			protected = append(protected, route.Name)
		}
	}
	text := "They protect " + list(protected) + ", which no longer ask for their own logins."
	switch len(own) {
	case 0:
	case 1:
		text += " " + own[0] + " keeps its own login."
	default:
		text += " " + list(own) + " keep their own logins."
	}
	return text
}

// list joins names as "A", "A and B", or "A, B, and C".
func list(names []string) string {
	switch len(names) {
	case 0:
		return "no application"
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
}

// unencrypted explains, on the sign-in page and in setup, what local mode cannot offer.
const unencrypted = "This address is not encrypted, so passkeys and Google sign-in are unavailable."

// Guide shows where the admin signs in and where sign-in methods are added.
func Guide(v app.Values) flow.Screen {
	body := `Open this link:
` + URL(v) + `/if/admin/

1. Sign in with the admin login.
2. Username: ` + User(v) + `
3. Password: ` + Password(v) + `
4. To let people sign in with Google or another account, open Directory > Federation and Social login, click Create, then paste the keys from that provider.`
	if !caddy.Secure(v) {
		body += "\n\n" + unencrypted
	}
	return flow.Screen{Title: "Authentik setup", Body: body}
}

// Blueprint is the Authentik configuration the program owns: the admin
// user, the sign-in check for the administration pages, and the sign-in
// page's title. Authentik applies it whenever the file changes. Secrets are
// read from the worker's environment, never written into the file; the login
// fingerprint changes the file when the admin login changes.
func Blueprint(v app.Values) string {
	title := "Welcome to authentik!"
	if !caddy.Secure(v) {
		title = "Sign in. " + unencrypted
	}
	return `# Generated by selfhost. Changes here are overwritten.
# Admin login fingerprint: ` + loginFingerprint(v) + `
version: 1
metadata:
  name: selfhost
entries:
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: authentik Bootstrap
      required: true
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: Default - Authentication flow
      required: true
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: Default - Provider authorization flow (implicit consent)
      required: true
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: Default - Provider invalidation flow
      required: true
  - model: authentik_core.user
    state: present
    identifiers:
      username: !Env ADMIN_USER
    attrs:
      name: !Env ADMIN_USER
      password: !Env ADMIN_PASS
      groups:
        - !Find [authentik_core.group, [name, authentik Admins]]
` + retiredEntries(v) + `  - model: authentik_providers_proxy.proxyprovider
    id: provider
    state: present
    identifiers:
      name: selfhost administration pages
    attrs:
      mode: forward_single
      external_host: ` + quote(caddy.URL(v)) + `
      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]
      invalidation_flow: !Find [authentik_flows.flow, [slug, default-provider-invalidation-flow]]
  - model: authentik_core.application
    state: present
    identifiers:
      slug: selfhost-administration
    attrs:
      name: Administration pages
      provider: !KeyOf provider
  - model: authentik_outposts.outpost
    state: present
    identifiers:
      managed: goauthentik.io/outposts/embedded
    attrs:
      providers:
        - !KeyOf provider
      config:
        authentik_host: ` + quote(URL(v)) + `
  - model: authentik_flows.flow
    state: present
    identifiers:
      slug: default-authentication-flow
    attrs:
      title: ` + quote(title) + `
`
}

// retiredEntries keeps every earlier admin account deactivated.
func retiredEntries(v app.Values) string {
	var out strings.Builder
	for _, name := range retiredAdmins(v) {
		out.WriteString("  - model: authentik_core.user\n    state: present\n    identifiers:\n      username: " + quote(name) + "\n    attrs:\n      is_active: false\n")
	}
	return out.String()
}

// loginFingerprint changes whenever the admin login changes. It is keyed
// with the stack's secret, so it does not reveal the password.
func loginFingerprint(v app.Values) string {
	mac := hmac.New(sha256.New, []byte(v.Get("AUTHENTIK_SECRET_KEY")))
	mac.Write([]byte(User(v) + "\x00" + Password(v)))
	return hex.EncodeToString(mac.Sum(nil))
}

// quote writes value as a YAML string.
func quote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
