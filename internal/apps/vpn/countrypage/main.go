// Command countrypage serves a page that selects the Gluetun VPN country and
// keeps that choice applied. It runs in its own container, built from this
// file alone, so it uses only the standard library.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	port           = 8090
	reapplyEvery   = 30 * time.Second
	requestTimeout = 10 * time.Second
)

type page struct {
	dataDir    string
	gluetunURL string
	gluetunKey string
	client     *http.Client
}

func main() {
	p := &page{
		dataDir:    envOr("DATA_DIR", "/data"),
		gluetunURL: envOr("GLUETUN_URL", "http://gluetun:8000"),
		gluetunKey: os.Getenv("GLUETUN_CONTROL_KEY"),
		client:     &http.Client{Timeout: requestTimeout},
	}
	go p.reapplyLoop()
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), p))
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (p *page) countriesFile() string { return filepath.Join(p.dataDir, "countries.json") }
func (p *page) selectionFile() string { return filepath.Join(p.dataDir, "selection.json") }

func readStrings(raw []any) []string {
	var values []string
	for _, item := range raw {
		if value, ok := item.(string); ok {
			values = append(values, value)
		}
	}
	return values
}

func (p *page) availableCountries() []string {
	var raw []any
	content, err := os.ReadFile(p.countriesFile())
	if err != nil || json.Unmarshal(content, &raw) != nil {
		return nil
	}
	return readStrings(raw)
}

func (p *page) savedCountries() []string {
	var selection struct {
		Countries []any `json:"countries"`
	}
	content, err := os.ReadFile(p.selectionFile())
	if err != nil || json.Unmarshal(content, &selection) != nil {
		return nil
	}
	return readStrings(selection.Countries)
}

func (p *page) saveCountries(countries []string) error {
	if countries == nil {
		countries = []string{}
	}
	content, err := json.Marshal(map[string][]string{"countries": countries})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.dataDir, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(p.dataDir, ".selection.")
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		os.Remove(file.Name())
		return err
	}
	if err := os.Rename(file.Name(), p.selectionFile()); err != nil {
		os.Remove(file.Name())
		return err
	}
	return nil
}

// gluetun calls Gluetun's control API and decodes its JSON answer. A plain
// text answer is returned under "outcome".
func (p *page) gluetun(method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		content, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(content)
	}
	request, err := http.NewRequest(method, p.gluetunURL+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-API-Key", p.gluetunKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP Error %d: %s", response.StatusCode, http.StatusText(response.StatusCode))
	}
	var result map[string]any
	if json.Unmarshal(content, &result) != nil {
		return map[string]any{"outcome": strings.TrimSpace(string(content))}, nil
	}
	return result, nil
}

func selectionBody(countries []string) map[string]any {
	if countries == nil {
		countries = []string{}
	}
	return map[string]any{"provider": map[string]any{"server_selection": map[string]any{"countries": countries}}}
}

func (p *page) currentCountries() ([]string, error) {
	settings, err := p.gluetun(http.MethodGet, "/v1/vpn/settings", nil)
	if err != nil {
		return nil, err
	}
	provider, _ := settings["provider"].(map[string]any)
	selection, _ := provider["server_selection"].(map[string]any)
	countries, _ := selection["countries"].([]any)
	return readStrings(countries), nil
}

func (p *page) applyCountries(countries []string) error {
	_, err := p.gluetun(http.MethodPut, "/v1/vpn/settings", selectionBody(countries))
	return err
}

// reapplySavedSelection puts the saved choice back after Gluetun restarts
// with the country it was started with.
func (p *page) reapplySavedSelection() error {
	if _, err := os.Stat(p.selectionFile()); err != nil {
		return nil
	}
	saved := p.savedCountries()
	current, err := p.currentCountries()
	if err != nil {
		return err
	}
	if !slices.Equal(current, saved) {
		return p.applyCountries(saved)
	}
	return nil
}

func (p *page) reapplyLoop() {
	for {
		p.reapplySavedSelection()
		time.Sleep(reapplyEvery)
	}
}

func requestedCountries(formValue string, choices []string) ([]string, error) {
	if formValue == "" {
		return []string{}, nil
	}
	if !slices.Contains(choices, formValue) {
		return nil, errors.New("Unknown country: " + formValue)
	}
	return []string{formValue}, nil
}

func (p *page) render(message string) string {
	choices := p.availableCountries()
	selected := p.savedCountries()
	location := "Unavailable"
	if address, err := p.gluetun(http.MethodGet, "/v1/publicip/ip", nil); err == nil {
		if ip, _ := address["public_ip"].(string); ip != "" {
			country, ok := address["country"].(string)
			if !ok {
				country = "?"
			}
			location = fmt.Sprintf("%s (%s)", ip, country)
		} else {
			location = "Reconnecting"
		}
	}
	options := []string{`<option value="">Any country</option>`}
	for _, country := range choices {
		mark := ""
		if len(selected) == 1 && selected[0] == country {
			mark = " selected"
		}
		options = append(options, fmt.Sprintf(`<option value="%s"%s>%s</option>`, html.EscapeString(country), mark, html.EscapeString(country)))
	}
	notice := ""
	if message != "" {
		notice = `<p class="message">` + html.EscapeString(message) + `</p>`
	}
	unavailable := ""
	if len(choices) == 0 {
		unavailable = `<p class="message">The country list is unavailable until the stack starts.</p>`
	}
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>VPN Country</title>
<style>
:root { color-scheme: dark; --bg: #1e293b; --panel: #334155; --text: #e2e8f0; --muted: #94a3b8; --accent: #38bdf8; }
body { margin: 0; background: var(--bg); color: var(--text); font: 16px system-ui, sans-serif; }
main { max-width: 28rem; margin: 3rem auto; padding: 0 16px; }
section { background: var(--panel); border-radius: 8px; padding: 1.25rem; }
p { margin: 0 0 1rem; }
.muted { color: var(--muted); }
.message { color: var(--accent); }
select, button { width: 100%; padding: .6rem; border-radius: 6px; font: inherit; margin-top: .5rem; }
button { background: var(--accent); color: #0f172a; border: 0; font-weight: 600; cursor: pointer; }
</style>
</head>
<body>
<main>
<h1>VPN Country</h1>
<section>
<p><span class="muted">Current address</span><br>` + html.EscapeString(location) + `</p>
` + notice + unavailable + `
<form method="post">
<label for="country" class="muted">Connect through</label>
<select id="country" name="country">` + strings.Join(options, "") + `</select>
<button type="submit">Connect</button>
</form>
</section>
</main>
</body>
</html>
`
}

func (p *page) send(w http.ResponseWriter, status int, message string) {
	content := p.render(message)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprint(len(content)))
	w.WriteHeader(status)
	io.WriteString(w, content)
}

func (p *page) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		p.send(w, http.StatusOK, "")
	case http.MethodPost:
		r.ParseForm()
		countries, err := requestedCountries(r.PostForm.Get("country"), p.availableCountries())
		if err != nil {
			p.send(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := p.applyCountries(countries); err != nil {
			p.send(w, http.StatusBadGateway, "Gluetun did not accept the change: "+err.Error())
			return
		}
		if err := p.saveCountries(countries); err != nil {
			p.send(w, http.StatusBadGateway, "Gluetun did not accept the change: "+err.Error())
			return
		}
		target := "any country"
		if len(countries) > 0 {
			target = countries[0]
		}
		p.send(w, http.StatusOK, "Reconnecting through "+target+". The address updates within a minute.")
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
