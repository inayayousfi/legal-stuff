// Package jellyfin is the media server. It stays outside the admin sign-in
// because its phone and TV applications cannot pass it.
package jellyfin

import (
	"embed"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/apps/caddy"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/flow"
)

//go:embed compose.yaml
var compose embed.FS

const (
	userKey     = "JELLYFIN_ADMIN_USER"
	passwordKey = "JELLYFIN_ADMIN_PASS"
	keyName     = "JELLYFIN_API_KEY"
	baseURL     = "/jellyfin"
)

var App = &app.App{
	Name: "jellyfin",
	Settings: []app.Setting{
		{Key: userKey, Example: "replace_with_your_jellyfin_administrator_username"},
		{Key: passwordKey, Example: "replace_with_your_jellyfin_administrator_password"},
		{Key: keyName, Example: "replace_with_the_jellyfin_generated_api_key"},
	},
	Renamed: map[string]string{
		"JELLYFIN_USER": userKey,
		"JELLYFIN_PASS": passwordKey,
	},
	Compose:    compose,
	Services:   func(app.Values) []string { return []string{"jellyfin-app"} },
	ConfigDirs: []string{"jellyfin", "jellyfin-cache"},
	Prepare:    prepare,
	Setup:      setup,
	Routes: func(app.Values) []app.Route {
		return []app.Route{{
			Name:     "Jellyfin",
			Path:     baseURL,
			Upstream: "jellyfin-app:8096",
			Public:   true,
			Internal: &app.Internal{Name: "jellyfin", Port: 8096},
		}}
	},
	Tiles: func(app.Values) []app.Tile {
		return []app.Tile{{Group: "Media", Position: 1, YAML: `    - Jellyfin:
        icon: jellyfin.png
        server: selfnook
        container: selfnook-jellyfin-app
        href: "{{HOMEPAGE_VAR_URL}}/jellyfin"
        description: Watch movies and series
`}}
	},
	Credentials: &app.CredentialGroup{Name: "jellyfin", Fields: []app.CredentialField{
		{Label: "Administrator username", Key: userKey, Kind: app.PlainText},
		{Label: "Administrator password", Key: passwordKey, Kind: app.Password},
		{Label: "API key", Key: keyName, Kind: app.APIKey},
	}},
}

// prepare sets Jellyfin's base URL while Jellyfin is stopped, because Jellyfin
// rewrites its network file when it exits.
func prepare(e *app.Env) ([]string, error) {
	path := networkFile(e.ConfigDir())
	if BaseURLIsSet(path) {
		return nil, nil
	}
	if err := e.Compose("stop", "jellyfin-app"); err != nil {
		return nil, err
	}
	return nil, WriteBaseURL(path)
}

// AdminUser and AdminPassword are the Jellyfin administrator login.
func AdminUser(v app.Values) string { return v.Get(userKey) }

func AdminPassword(v app.Values) string { return v.Get(passwordKey) }

func setup(e *app.Env) error {
	accessURL := caddy.URL(e.Values)
	err := e.Step("jellyfin", false, func() error {
		fields := credentialFields(e.Values)
		answers, err := e.UI.Ask(Guide(accessURL, fields))
		if err != nil {
			return err
		}
		saveAnswers(e.Values, answers)
		if err := e.SaveValues(); err != nil {
			return err
		}
		if answers, err = e.UI.Ask(flow.Screen{Wait: "Jellyfin", Fields: []flow.Field{flow.APIKeyField(keyName, "Jellyfin")}}); err != nil {
			return err
		}
		e.Values.Set(keyName, answers[keyName])
		return e.SaveValues()
	})
	if err != nil {
		return err
	}

	if fields := credentialFields(e.Values); len(fields) > 0 {
		answers, err := e.UI.Ask(flow.Screen{
			Body:   "Record the existing Jellyfin administrator username and password in the following prompts.",
			Fields: fields,
		})
		if err != nil {
			return err
		}
		saveAnswers(e.Values, answers)
		if err := e.SaveValues(); err != nil {
			return err
		}
	}

	if !e.Values.Has(keyName) {
		answers, err := e.UI.Ask(flow.Screen{
			Body:   "In Jellyfin, open Dashboard > API Keys, click New API Key, set App name to Radarr and Sonarr, then click Create. The following prompt requests the generated key.",
			Fields: []flow.Field{flow.APIKeyField(keyName, "Jellyfin")},
		})
		if err != nil {
			return err
		}
		e.Values.Set(keyName, answers[keyName])
		if err := e.SaveValues(); err != nil {
			return err
		}
	}

	return e.Step("jellyfin-notifications", false, func() error {
		return flow.Show(e.UI, NotificationsGuide(accessURL, e.Values.Get(keyName)))
	})
}

// credentialFields asks only for the administrator values not saved yet.
func credentialFields(v app.Values) []flow.Field {
	var fields []flow.Field
	if !v.Has(userKey) {
		fields = append(fields, flow.UsernameField(userKey, "Jellyfin administrator"))
	}
	if !v.Has(passwordKey) {
		fields = append(fields, flow.PasswordField(passwordKey, "Jellyfin administrator"))
	}
	return fields
}

func saveAnswers(v app.Values, answers flow.Answers) {
	for key, value := range answers {
		v.Set(key, value)
	}
}

// Guide walks through Jellyfin's first-run wizard, then asks for the
// administrator login the user created there.
func Guide(accessURL string, fields []flow.Field) flow.Screen {
	return flow.Screen{
		Title: "Jellyfin setup",
		Body: `Open this link:
` + accessURL + `/jellyfin

1. Select the display language.
2. Create the Jellyfin administrator account with the username and password requested after these steps.
3. Add a Movies library using /media/Movies.
4. Add a Shows library using /media/Series.
5. Complete the remaining setup wizard pages.
6. If some media do not appear, open Dashboard > Users > your user > Parental Control. Check the maximum allowed rating and whether items with no or unrecognized rating are blocked, then save any changes.
7. Open Dashboard > Scheduled Tasks and run Scan Library to refresh the libraries.
8. Open Dashboard > API Keys, click New API Key, set App name to Radarr and Sonarr, then click Create.
9. Copy the API key that Jellyfin generated automatically. A terminal prompt after these steps will ask for it.`,
		Fields: fields,
	}
}

// NotificationsGuide makes Radarr and Sonarr refresh Jellyfin after each import.
func NotificationsGuide(accessURL, apiKey string) flow.Screen {
	return flow.Screen{
		Title: "Jellyfin notifications",
		Body: `Radarr link:
` + accessURL + `/radarr

Sonarr link:
` + accessURL + `/sonarr

1. In Radarr, open Settings > Connect, click +, then select Emby / Jellyfin.
2. Name: Jellyfin
3. Check On File Import, On File Upgrade, On Rename, On Movie Delete, On Movie File Delete, and On Movie File Delete For Upgrade.
4. Host: jellyfin
5. Port: 8096
6. API Key: ` + apiKey + `
7. Keep Update Library checked, then click Test and Save.
8. In Sonarr, open Settings > Connect, click +, then select Emby / Jellyfin.
9. Name: Jellyfin
10. Check On File Import, On File Upgrade, On Import Complete, On Rename, On Series Delete, On Episode File Delete, and On Episode File Delete For Upgrade.
11. Host: jellyfin
12. Port: 8096
13. API Key: ` + apiKey + `
14. Keep Update Library checked, then click Test and Save.`,
		Wait: "Jellyfin notification",
	}
}

func networkFile(configDir string) string {
	return filepath.Join(configDir, "jellyfin", "config", "network.xml")
}

// BaseURLIsSet reports whether Jellyfin's network file already serves /jellyfin.
func BaseURLIsSet(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var network struct {
		BaseURL *string `xml:"BaseUrl"`
	}
	if xml.Unmarshal(content, &network) != nil || network.BaseURL == nil {
		return false
	}
	return *network.BaseURL == baseURL
}

// WriteBaseURL sets BaseUrl in Jellyfin's network file, keeping every other setting.
func WriteBaseURL(path string) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		content = []byte(`<?xml version='1.0' encoding='utf-8'?>` + "\n<NetworkConfiguration />")
	} else if err != nil {
		return err
	}
	updated, err := setBaseURL(string(content))
	if err != nil {
		return err
	}
	return files.WriteAtomic(path, []byte(updated), 0o644)
}

// setBaseURL edits the BaseUrl element of the root element as text, so the
// rest of the file keeps its exact form.
func setBaseURL(document string) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(document))
	depth := 0
	rootEnd, start, end := -1, -1, -1
	rootSelfClosing := false
	for {
		offset := decoder.InputOffset()
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				rootEnd = int(decoder.InputOffset())
				rootSelfClosing = strings.HasSuffix(strings.TrimSpace(document[offset:rootEnd]), "/>")
			}
			if depth == 2 && t.Name.Local == "BaseUrl" && start < 0 {
				start = int(offset)
			}
		case xml.EndElement:
			if depth == 2 && t.Name.Local == "BaseUrl" && start >= 0 && end < 0 {
				end = int(decoder.InputOffset())
			}
			depth--
		}
	}
	if rootEnd < 0 {
		return "", errors.New("Jellyfin's network.xml has no root element.")
	}
	element := "<BaseUrl>" + baseURL + "</BaseUrl>"
	switch {
	case start >= 0 && end >= 0:
		return document[:start] + element + document[end:], nil
	case start >= 0:
		// A self-closing <BaseUrl />.
		closeAt := strings.Index(document[start:], "/>") + start + 2
		return document[:start] + element + document[closeAt:], nil
	case rootSelfClosing:
		openTag := strings.TrimSuffix(strings.TrimSpace(document[:rootEnd]), "/>")
		name := strings.Fields(openTag[strings.LastIndex(openTag, "<")+1:])[0]
		return strings.TrimRight(openTag, " ") + ">" + element + "</" + name + ">" + document[rootEnd:], nil
	default:
		return document[:rootEnd] + element + document[rootEnd:], nil
	}
}
