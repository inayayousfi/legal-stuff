package stack

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inayayousfi/selfnook/internal/apps"
	"github.com/inayayousfi/selfnook/internal/apps/vpn"
	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/settings"
	"github.com/inayayousfi/selfnook/internal/shell"
)

const (
	sonarrKey   = "0123456789abcdef0123456789abcdef"
	radarrKey   = "fedcba9876543210fedcba9876543210"
	jellyfinKey = "00112233445566778899aabbccddeeff"
)

// dockerHost answers like a working Docker host whose VPN connects at once.
func dockerHost(subnet string) func([]string) shell.Result {
	return func(args []string) shell.Result {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "docker network inspect selfnook "):
			if subnet == "" {
				return shell.Result{Code: 1}
			}
			return shell.Result{Stdout: subnet + " \n"}
		case strings.HasPrefix(line, "docker network inspect media-stack"):
			return shell.Result{Code: 1}
		case strings.HasPrefix(line, "docker inspect selfnook-gluetun"):
			return shell.Result{Stdout: "healthy\n"}
		case strings.HasPrefix(line, "docker exec selfnook-gluetun"):
			return shell.Result{Stdout: `[{"vpn":"openvpn","country":"Spain"},{"vpn":"wireguard","country":"Chile"}]`}
		}
		return shell.Result{}
	}
}

// newTestStack isolates PATH, HOME, and the stack folder, with a docker
// command present so the stack finds Docker installed.
func newTestStack(t *testing.T, ui flow.UI, sh *fake.Shell) *Stack {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER", "tester")
	return &Stack{Root: t.TempDir(), Apps: apps.All, Shell: sh, UI: ui, Program: "/opt/selfnook/selfnook", Sleep: func(time.Duration) {}}
}

func setupAnswers(media string) map[string]string {
	return map[string]string{
		"media":                media,
		"accept":               "yes",
		"provider":             "nordvpn",
		"VPN_OPENVPN_USER":     "vpn-user",
		"VPN_OPENVPN_PASSWORD": "vpn-secret",
		"mode":                 "domain",
		"host":                 "Media.Example.com.",
		"ADMIN_USER":           "admin",
		"ADMIN_PASS":           "admin-secret",
		"SONARR_API_KEY":       sonarrKey,
		"RADARR_API_KEY":       radarrKey,
		"JELLYFIN_ADMIN_USER":  "jelly",
		"JELLYFIN_ADMIN_PASS":  "jelly-secret",
		"JELLYFIN_API_KEY":     jellyfinKey,
		"kind":                 "user",
	}
}

func readEnv(t *testing.T, s *Stack) *settings.Values {
	t.Helper()
	values, err := settings.Read(s.envFile())
	if err != nil {
		t.Fatal(err)
	}
	return values
}

// Given a fresh folder, through the whole guided setup with every app in the
// registry, expect saved values, each guide in order, and a resumable record.
func TestSetupGuidesEveryAppInOrderAndSavesValues(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	media := filepath.Join(t.TempDir(), "Media")
	ui.Answers = setupAnswers(media)
	var savedAtJellyfinWait, savedAtNotifications *settings.Values
	commandsAtSeerr := -1
	ui.OnAsk = func(screen flow.Screen) {
		switch {
		case screen.Wait == "Jellyfin":
			savedAtJellyfinWait = readEnv(t, s)
		case screen.Title == "Jellyfin notifications":
			savedAtNotifications = readEnv(t, s)
		case screen.Title == "Seerr setup":
			commandsAtSeerr = len(sh.Commands)
		}
	}

	if err := s.Setup(); err != nil {
		t.Fatalf("setup failed: %v\nevents: %v", err, ui.Events)
	}

	values := readEnv(t, s)
	want := map[string]string{
		"MEDIA_DIR":            filepath.ToSlash(media),
		"CONFIG_DIR":           filepath.ToSlash(filepath.Join(s.Root, "config")),
		"QBT_LEGAL_NOTICE":     "confirm",
		"VPN_GATEWAY_SERVICE":  vpn.Gluetun,
		"VPN_SERVICE_PROVIDER": "nordvpn",
		"VPN_TYPE":             "openvpn",
		"ACCESS_MODE":          "domain",
		"ADMIN_ACCESS_HOST":    "media.example.com",
		"ACCESS_URL":           "https://media.example.com",
		"ADMIN_USER":           "admin",
		"SONARR_API_KEY":       sonarrKey,
		"RADARR_API_KEY":       radarrKey,
		"JELLYFIN_API_KEY":     jellyfinKey,
		"JELLYFIN_ADMIN_USER":  "jelly",
	}
	for key, value := range want {
		if values.Get(key) != value {
			t.Errorf("%s = %q, want %q", key, values.Get(key), value)
		}
	}
	for _, dir := range []string{"Movies", "Series", "Downloads"} {
		if _, err := os.Stat(filepath.Join(media, dir)); err != nil {
			t.Errorf("media folder %s missing", dir)
		}
	}

	var order []string
	for _, event := range ui.Events {
		if strings.HasPrefix(event, "screen: ") && strings.HasSuffix(event, " setup") {
			order = append(order, strings.TrimPrefix(event, "screen: "))
		}
	}
	wantOrder := []string{"VPN kill-switch setup", "qBittorrent setup", "Sonarr setup", "Radarr setup", "Prowlarr setup", "Authentik setup", "Jellyfin setup", "Seerr setup"}
	if !slices.Equal(order, wantOrder) {
		t.Errorf("guide order = %v, want %v", order, wantOrder)
	}

	if savedAtJellyfinWait == nil || savedAtJellyfinWait.Get("JELLYFIN_ADMIN_USER") != "jelly" {
		t.Error("Jellyfin administrator login was not saved before waiting for Jellyfin")
	}
	if savedAtNotifications == nil || savedAtNotifications.Get("JELLYFIN_API_KEY") != jellyfinKey {
		t.Error("Jellyfin API key was not saved before the notifications guide")
	}
	if !strings.Contains(ui.Text(), "API Key: "+jellyfinKey) {
		t.Error("notifications guide does not print the saved Jellyfin API key")
	}
	syncAt := sh.Index("docker compose run --rm recyclarr sync")
	if syncAt < 0 || commandsAtSeerr < 0 || syncAt >= commandsAtSeerr {
		t.Errorf("Recyclarr sync (command %d) must run before the Seerr guide (after command %d)", syncAt, commandsAtSeerr)
	}

	unit, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "selfnook.service"))
	if err != nil || !strings.Contains(string(unit), "ExecStart=/opt/selfnook/selfnook start --log-file ") {
		t.Errorf("user service not installed for the program: %v\n%s", err, unit)
	}
	if last := ui.Events[len(ui.Events)-1]; !strings.Contains(last, "Setup complete.") || !strings.Contains(last, "https://media.example.com") {
		t.Errorf("last message = %q", last)
	}
}

// Given a finished setup, running setup again asks only for the automatic
// start choice, because every answer and guided step is saved.
func TestSetupResumesWithoutRepeatingSavedSteps(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}

	again := &fake.UI{Answers: map[string]string{"kind": "user"}}
	s.UI = again
	if err := s.Setup(); err != nil {
		t.Fatalf("second setup failed: %v\nevents: %v", err, again.Events)
	}
	var asked []string
	for _, event := range again.Events {
		if strings.HasPrefix(event, "ask: ") || strings.HasPrefix(event, "screen: ") {
			asked = append(asked, event)
		}
	}
	if !slices.Equal(asked, []string{"screen: Linux automatic start", "ask: kind"}) {
		t.Errorf("second setup asked %v", asked)
	}
}

// Guides describe actions in ordinary words; .env variable names are internal bookkeeping.
func TestGuidesDoNotExposeEnvironmentNames(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	if names := regexp.MustCompile(`\b[A-Z]+_[A-Z_]+\b`).FindAllString(ui.Text(), -1); len(names) > 0 {
		t.Errorf("guides expose %v", names)
	}
}

func startedStack(t *testing.T, subnet string) (*Stack, *fake.Shell, *fake.UI) {
	t.Helper()
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	sh.Commands = nil
	sh.Respond = dockerHost(subnet)
	ui.Events = nil
	return s, sh, ui
}

// Services outside the VPN start first; qBittorrent and Prowlarr start only
// after the gateway reports ready, and Recyclarr syncs last.
func TestStartWaitsForTheGatewayBeforeVPNServices(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	stop := sh.Index("docker compose stop qbittorrent prowlarr tailscale-vpn")
	up := sh.Index("docker compose up -d --no-deps --remove-orphans")
	ready := sh.Index("docker inspect selfnook-gluetun")
	vpnUp := sh.Index("docker compose up -d --no-deps qbittorrent prowlarr")
	sync := sh.Index("docker compose run --rm recyclarr sync")
	if !(stop >= 0 && stop < up && up < ready && ready < vpnUp && vpnUp < sync) {
		t.Errorf("order stop=%d up=%d ready=%d vpnUp=%d sync=%d\n%s", stop, up, ready, vpnUp, sync, strings.Join(sh.Lines(), "\n"))
	}
	upLine := sh.Lines()[up]
	for _, service := range []string{"gluetun", "vpn-country", "caddy", "authentik-db", "authentik-server", "authentik-worker", "homepage", "jellyfin-app", "seerr", "sonarr-app", "radarr-app"} {
		if !strings.Contains(upLine, " "+service) {
			t.Errorf("%q does not start %s", upLine, service)
		}
	}
	if strings.Contains(upLine, "qbittorrent") || strings.Contains(upLine, "tailscale-vpn") {
		t.Errorf("%q starts a VPN user or the inactive gateway", upLine)
	}
	if sh.Index("docker compose down") >= 0 {
		t.Error("a network with the expected address range was recreated")
	}
}

// A network left with another address range is removed so Compose recreates it.
func TestStartRecreatesANetworkWithAnotherAddressRange(t *testing.T) {
	s, sh, _ := startedStack(t, "10.0.0.0/24")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if sh.Index("docker compose down --remove-orphans") < 0 {
		t.Errorf("network not recreated:\n%s", strings.Join(sh.Lines(), "\n"))
	}
}

// An install made under the earlier project name is removed before the
// stack starts, because its containers hold the same ports and address range.
func TestStartRemovesTheEarlierProject(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24")
	host := sh.Respond
	sh.Respond = func(args []string) shell.Result {
		if strings.Join(args, " ") == "docker ps --all --quiet --filter label=com.docker.compose.project=media-stack" {
			return shell.Result{Stdout: "0123abcd\n"}
		}
		return host(args)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	down := sh.Index("docker compose --project-name media-stack down --remove-orphans")
	image := sh.Index("docker image rm media-stack/vpn-country")
	up := sh.Index("docker compose up -d --no-deps --remove-orphans")
	if !(down >= 0 && down < image && image < up) {
		t.Errorf("order down=%d image=%d up=%d\n%s", down, image, up, strings.Join(sh.Lines(), "\n"))
	}
}

// Setup replaces the service that earlier versions installed, so the earlier
// program no longer runs at login.
func TestSetupReplacesTheEarlierUserService(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	units := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	if err := os.MkdirAll(units, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(units, "media-stack.service"), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(units, "media-stack.service")); err == nil {
		t.Error("media-stack.service kept")
	}
	if _, err := os.Stat(filepath.Join(units, "selfnook.service")); err != nil {
		t.Error("selfnook.service not installed")
	}
	if sh.Index("systemctl --user disable media-stack.service") < 0 {
		t.Errorf("earlier service not disabled:\n%s", strings.Join(sh.Lines(), "\n"))
	}
}

// When the Gluetun access file changes, Gluetun restarts before the VPN users start.
func TestChangedGatewayAccessRestartsGluetunBeforeVPNServices(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24")
	values := readEnv(t, s)
	values.Set("GLUETUN_API_KEY", "changed")
	if err := settings.Write(s.envFile(), values); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	restart := sh.Index("docker compose restart gluetun")
	if restart < 0 || restart > sh.Index("docker compose up -d --no-deps qbittorrent prowlarr") {
		t.Errorf("gluetun restart missing or late:\n%s", strings.Join(sh.Lines(), "\n"))
	}
	if sh.Index("docker compose restart caddy") >= 0 {
		t.Error("Caddy restarted although its configuration did not change")
	}
}

func TestStartWithoutSetupAsksForSetup(t *testing.T) {
	s := newTestStack(t, &fake.UI{}, &fake.Shell{})
	if err := s.Start(); err == nil || err.Error() != "Run 'selfnook setup' first." {
		t.Errorf("err = %v", err)
	}
}

func TestStartReportsMissingValues(t *testing.T) {
	s := newTestStack(t, &fake.UI{}, &fake.Shell{})
	values := settings.NewValues()
	values.Set("MEDIA_DIR", "/media")
	if err := settings.Write(s.envFile(), values); err != nil {
		t.Fatal(err)
	}
	err := s.Start()
	if err == nil || !strings.HasPrefix(err.Error(), "Missing required .env values: ACCESS_MODE, ADMIN_ACCESS_HOST, ADMIN_PASS,") {
		t.Errorf("err = %v", err)
	}
}

func TestVPNStatusReportsHealthAndCountries(t *testing.T) {
	s, _, ui := startedStack(t, "")
	values := readEnv(t, s)
	values.Set("VPN_SERVER_COUNTRIES", "Spain")
	settings.Write(s.envFile(), values)
	if err := s.AppStatus(vpn.App); err != nil {
		t.Fatal(err)
	}
	if got := ui.Events[len(ui.Events)-1]; got != "say: Gluetun VPN: healthy\nSelected countries:\nSpain" {
		t.Errorf("status = %q", got)
	}
}

// A start that fails after stopping qBittorrent and Prowlarr says they stay stopped.
func TestFailedStartNamesTheServicesItLeftStopped(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24")
	ready := sh.Respond
	sh.Respond = func(args []string) shell.Result {
		if strings.HasPrefix(strings.Join(args, " "), "docker compose up -d --no-deps --remove-orphans") {
			return shell.Result{Code: 1}
		}
		return ready(args)
	}
	err := s.Start()
	var stopped *StoppedError
	if !errors.As(err, &stopped) || !slices.Equal(stopped.Services, []string{"qbittorrent", "prowlarr"}) {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasSuffix(err.Error(), "\nStopped until the next successful start: qbittorrent, prowlarr") {
		t.Errorf("message = %q", err.Error())
	}
}

// Containers reach Sonarr, Radarr, Prowlarr, and Jellyfin by name through Caddy.
func TestCaddyAnswersForTheInternalNames(t *testing.T) {
	s, _, _ := startedStack(t, "")
	content, err := os.ReadFile(filepath.Join(s.Root, "compose", "caddy", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "        aliases:\n          - sonarr\n          - radarr\n          - prowlarr\n          - jellyfin\n"
	if !strings.Contains(string(content), want) {
		t.Errorf("caddy fragment:\n%s", content)
	}
}

// Values saved by earlier versions under old names reach the current names at setup.
func TestSetupMovesRenamedSettings(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := os.WriteFile(s.envFile(), []byte("JELLYFIN_USER=\"jelly\"\nJELLYFIN_PASS=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(s.envFile())
	values := readEnv(t, s)
	if values.Get("JELLYFIN_ADMIN_USER") != "jelly" || values.Get("JELLYFIN_ADMIN_PASS") != "secret" || strings.Contains(string(content), "JELLYFIN_USER=") {
		t.Errorf(".env:\n%s", content)
	}
}

var update = flag.Bool("update", false, "rewrite .env.example from the apps")

// .env.example lists every setting the apps declare, so it changes with them.
func TestEnvExampleMatchesTheApps(t *testing.T) {
	path := filepath.Join("..", "..", ".env.example")
	want := ExampleEnv(apps.All)
	if *update {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != want {
		t.Errorf(".env.example is stale; run: go test ./internal/stack -run TestEnvExampleMatchesTheApps -update")
	}
}

// Each setting has one owner, so only that app can change it.
func TestEverySettingHasOneOwner(t *testing.T) {
	owners := map[string]string{}
	for _, setting := range coreSettings {
		owners[setting.Key] = "stack"
	}
	for _, a := range apps.All {
		for _, setting := range a.Settings {
			if owner, ok := owners[setting.Key]; ok {
				t.Errorf("%s is owned by both %s and %s", setting.Key, owner, a.Name)
			}
			owners[setting.Key] = a.Name
		}
		for _, current := range a.Renamed {
			if !a.Owns(current) {
				t.Errorf("%s renames into %s, which it does not own", a.Name, current)
			}
		}
	}
	for _, a := range apps.All {
		for _, key := range a.RetiredSettings {
			if owner, ok := owners[key]; ok {
				t.Errorf("%s retires %s, which %s still owns", a.Name, key, owner)
			}
		}
	}
}

// An install upgraded from Tinyauth loses its leftovers at the next start.
func TestStartRemovesWhatTinyauthLeftBehind(t *testing.T) {
	s, _, _ := startedStack(t, "172.31.250.0/24")
	values := readEnv(t, s)
	values.Set("TINYAUTH_USERS", "admin:hash")
	values.Set("ADMIN_LOGIN_FINGERPRINT", "abc")
	if err := settings.Write(s.envFile(), values); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(values.Get("CONFIG_DIR"), "tinyauth")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if after := readEnv(t, s); after.Has("TINYAUTH_USERS") || after.Has("ADMIN_LOGIN_FINGERPRINT") {
		t.Errorf("Tinyauth values kept: %v", after.Keys())
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("config/tinyauth kept")
	}
}

func TestSignInTextListsTheRegistryApps(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	want := "They protect qBittorrent, Sonarr, Radarr, Prowlarr, and VPN country page, which no longer ask for their own logins. Jellyfin and Seerr keep their own logins."
	if !strings.Contains(ui.Text(), want) {
		t.Errorf("sign-in text missing; guides:\n%s", ui.Text())
	}
}
