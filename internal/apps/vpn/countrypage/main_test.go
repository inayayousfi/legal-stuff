package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// gluetunStub answers like Gluetun's control API and records country changes.
type gluetunStub struct {
	mu        sync.Mutex
	countries []string
	keys      []string
	fail      bool
}

func (g *gluetunStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.keys = append(g.keys, r.Header.Get("X-API-Key"))
	switch {
	case r.URL.Path == "/v1/publicip/ip":
		io.WriteString(w, `{"public_ip": "203.0.113.5", "country": "Spain"}`)
	case r.URL.Path == "/v1/vpn/settings" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(selectionBody(g.countries))
	case r.URL.Path == "/v1/vpn/settings" && r.Method == http.MethodPut:
		if g.fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var body struct {
			Provider struct {
				ServerSelection struct {
					Countries []string `json:"countries"`
				} `json:"server_selection"`
			} `json:"provider"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		g.countries = body.Provider.ServerSelection.Countries
		io.WriteString(w, "settings updated")
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newPage(t *testing.T, stub *gluetunStub) *page {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "countries.json"), []byte(`["Austria", "Spain"]`), 0o644)
	return &page{dataDir: dir, gluetunURL: server.URL, gluetunKey: "control", client: server.Client()}
}

func post(p *page, country string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"country": {country}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	p.ServeHTTP(response, request)
	return response
}

func TestChosenCountryIsAppliedSavedAndShown(t *testing.T) {
	stub := &gluetunStub{}
	p := newPage(t, stub)
	response := post(p, "Spain")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Reconnecting through Spain.") {
		t.Fatalf("response %d:\n%s", response.Code, response.Body)
	}
	if !slices.Equal(stub.countries, []string{"Spain"}) || !slices.Equal(p.savedCountries(), []string{"Spain"}) {
		t.Errorf("gluetun = %v, saved = %v", stub.countries, p.savedCountries())
	}
	if slices.ContainsFunc(stub.keys, func(k string) bool { return k != "control" }) {
		t.Errorf("requests used keys %v", stub.keys)
	}
	page := httptest.NewRecorder()
	p.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	body := page.Body.String()
	if !strings.Contains(body, `<option value="Spain" selected>Spain</option>`) || !strings.Contains(body, "203.0.113.5 (Spain)") {
		t.Errorf("page:\n%s", body)
	}
}

func TestAnyCountryClearsTheSelection(t *testing.T) {
	stub := &gluetunStub{countries: []string{"Spain"}}
	p := newPage(t, stub)
	if response := post(p, ""); !strings.Contains(response.Body.String(), "Reconnecting through any country.") {
		t.Errorf("body:\n%s", response.Body)
	}
	if len(stub.countries) != 0 {
		t.Errorf("gluetun = %v", stub.countries)
	}
}

func TestUnknownCountryIsRejected(t *testing.T) {
	stub := &gluetunStub{}
	p := newPage(t, stub)
	response := post(p, "Atlantis")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Unknown country: Atlantis") || stub.countries != nil {
		t.Errorf("response %d, gluetun = %v", response.Code, stub.countries)
	}
}

// A refused change stays saved, so the reapply loop puts it in place once Gluetun accepts it.
func TestRefusedChangeIsReportedAndKeptForTheNextAttempt(t *testing.T) {
	stub := &gluetunStub{fail: true}
	p := newPage(t, stub)
	response := post(p, "Spain")
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "will apply when Gluetun accepts it") {
		t.Errorf("response %d:\n%s", response.Code, response.Body)
	}
	if !slices.Equal(p.savedCountries(), []string{"Spain"}) {
		t.Errorf("saved = %v", p.savedCountries())
	}
	stub.fail = false
	if err := p.reapplySavedSelection(); err != nil || !slices.Equal(stub.countries, []string{"Spain"}) {
		t.Errorf("err = %v, gluetun = %v", err, stub.countries)
	}
}

// A choice that cannot be saved is not applied, so the reapply loop cannot undo it.
func TestUnsavedChangeLeavesGluetunAlone(t *testing.T) {
	stub := &gluetunStub{countries: []string{"Austria"}}
	p := newPage(t, stub)
	os.Mkdir(p.selectionFile(), 0o755)
	os.WriteFile(filepath.Join(p.selectionFile(), "keep"), nil, 0o644)
	response := post(p, "Spain")
	if response.Code != http.StatusInternalServerError || !slices.Equal(stub.countries, []string{"Austria"}) {
		t.Errorf("response %d, gluetun = %v", response.Code, stub.countries)
	}
}

// After Gluetun restarts with its start-up country, the saved choice is put back.
func TestSavedSelectionIsReappliedAfterARestart(t *testing.T) {
	stub := &gluetunStub{}
	p := newPage(t, stub)
	post(p, "Austria")
	stub.countries = nil
	if err := p.reapplySavedSelection(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stub.countries, []string{"Austria"}) {
		t.Errorf("gluetun = %v", stub.countries)
	}
}

func TestNoSavedSelectionLeavesGluetunAlone(t *testing.T) {
	stub := &gluetunStub{countries: []string{"Spain"}}
	p := newPage(t, stub)
	p.reapplySavedSelection()
	if len(stub.keys) != 0 {
		t.Errorf("gluetun was called %d times", len(stub.keys))
	}
}
