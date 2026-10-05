// Package tinyauth is the admin sign-in page that protects the administration pages.
package tinyauth

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"regexp"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/settings"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

//go:embed compose.yaml
var compose embed.FS

const (
	userKey     = "ADMIN_USER"
	passwordKey = "ADMIN_PASS"
	// SignInPort serves the sign-in page.
	SignInPort = 9091
	// Address is where Caddy asks whether a request is signed in.
	Address = "tinyauth:3000"
)

var App = &app.App{
	Name:       "tinyauth",
	Compose:    compose,
	Services:   func(*settings.Values) []string { return []string{"tinyauth"} },
	ConfigDirs: []string{"tinyauth"},
	Required:   []string{userKey, passwordKey},
	Configure:  configure,
	Prepare: func(e *app.Env) ([]string, error) {
		changed, err := EnsureUsers(e.Shell, e.Values)
		if err != nil || !changed {
			return nil, err
		}
		return nil, e.SaveValues()
	},
	Routes: func(*settings.Values) []app.Route {
		return []app.Route{{Upstream: Address, Public: true, Port: SignInPort}}
	},
	Credentials: &app.CredentialGroup{Name: "admin", Fields: []app.CredentialField{
		{Label: "Username", Key: userKey, Kind: app.PlainText, UsedByStack: true},
		{Label: "Password", Key: passwordKey, Kind: app.Password, UsedByStack: true},
	}},
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
		Title: "Admin sign-in",
		Body: `Create one username and password. They protect Homepage, qBittorrent, Sonarr, Radarr, Prowlarr, and the VPN country page, which no longer ask for their own logins. Jellyfin and Seerr keep their own logins.

The following prompts request the username and password.`,
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

var (
	colors     = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	bcryptHash = `\$2[aby]\$\d\d\$[./A-Za-z0-9]{53}`
)

// loginFingerprint changes whenever the admin username or password changes.
func loginFingerprint(v *settings.Values) string {
	sum := sha256.Sum256([]byte(v.Get(userKey) + "\x00" + v.Get(passwordKey)))
	return hex.EncodeToString(sum[:])
}

// EnsureUsers stores the hashed admin login that Tinyauth reads, creating it
// with the Tinyauth image whenever the login changed. It reports whether values changed.
func EnsureUsers(s shell.Shell, v *settings.Values) (bool, error) {
	fingerprint := loginFingerprint(v)
	if v.Has("TINYAUTH_USERS") && v.Get("ADMIN_LOGIN_FINGERPRINT") == fingerprint {
		return false, nil
	}
	result, err := shell.ComposeCapture(s, "run", "--rm", "--no-deps", "-T", "tinyauth", "user", "create",
		"--username", v.Get(userKey), "--password", v.Get(passwordKey))
	if err != nil {
		return false, err
	}
	output := colors.ReplaceAllString(result.Stdout+result.Stderr, "")
	match := regexp.MustCompile(regexp.QuoteMeta(v.Get(userKey)) + ":(" + bcryptHash + ")").FindStringSubmatch(output)
	if result.Code != 0 || match == nil {
		return false, errors.New("Could not create the admin sign-in. Check that Docker can run the tinyauth image.")
	}
	v.Set("TINYAUTH_USERS", v.Get(userKey)+":"+match[1])
	v.Set("ADMIN_LOGIN_FINGERPRINT", fingerprint)
	return true, nil
}
