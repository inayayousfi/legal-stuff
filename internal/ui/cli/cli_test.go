package cli

import (
	"bufio"
	"errors"
	"strings"
	"testing"

	"github.com/inayayousfi/selfnook/internal/flow"
)

func run(t *testing.T, input string, screen flow.Screen) (flow.Answers, string, error) {
	t.Helper()
	var out strings.Builder
	ui := &UI{In: bufio.NewReader(strings.NewReader(input)), Out: &out}
	answers, err := ui.Ask(screen)
	return answers, out.String(), err
}

func TestGuideIsPrintedBeforeThePauseAndTheField(t *testing.T) {
	answers, out, err := run(t, "\n"+strings.Repeat("A", 32)+"\n", flow.Screen{
		Title: "Sonarr setup", Body: "1. Copy the key.", Wait: "Sonarr",
		Fields: []flow.Field{flow.APIKeyField("key", "Sonarr")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "\nSonarr setup\n\n1. Copy the key.\n\nPress Enter when Sonarr is configured.Paste the Sonarr API key: "
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	if answers["key"] != strings.Repeat("a", 32) {
		t.Errorf("key = %q", answers["key"])
	}
}

func TestInvalidEntryShowsTheErrorAndAsksAgain(t *testing.T) {
	answers, out, err := run(t, "bad\n"+strings.Repeat("b", 32)+"\n", flow.Screen{Fields: []flow.Field{flow.APIKeyField("key", "Radarr")}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Error: API keys must contain exactly 32 hexadecimal characters.\nPaste the Radarr API key: ") || answers["key"] != strings.Repeat("b", 32) {
		t.Errorf("output = %q, answers = %v", out, answers)
	}
}

func TestUsernameDefaultsToAdmin(t *testing.T) {
	answers, out, _ := run(t, "\n", flow.Screen{Fields: []flow.Field{flow.UsernameField("user", "Admin")}})
	if answers["user"] != "admin" || out != "Admin username [admin]: " {
		t.Errorf("answers = %v, output = %q", answers, out)
	}
}

func TestPasswordRequiresAMatchingRepeat(t *testing.T) {
	answers, out, err := run(t, "\nsecret\nother\nsecret\nsecret\n", flow.Screen{Fields: []flow.Field{flow.PasswordField("pass", "Admin")}})
	if err != nil {
		t.Fatal(err)
	}
	if answers["pass"] != "secret" {
		t.Errorf("password = %q", answers["pass"])
	}
	for _, line := range []string{"Error: Password cannot be empty.", "Error: Passwords do not match.", "Repeat Admin password: "} {
		if !strings.Contains(out, line) {
			t.Errorf("output lacks %q: %q", line, out)
		}
	}
}

func TestChoiceListsOptionsUsesTheDefaultAndRejectsOtherNumbers(t *testing.T) {
	field := flow.Field{Key: "p", Prompt: "Select a VPN provider", Kind: flow.Choice, Default: "b",
		Options: []flow.Option{{Value: "a", Label: "Alpha"}, {Value: "b", Label: "Beta"}}}
	answers, out, _ := run(t, "7\n\n", flow.Screen{Fields: []flow.Field{field}})
	if answers["p"] != "b" {
		t.Errorf("answer = %q", answers["p"])
	}
	want := "1. Alpha\n2. Beta\nSelect a VPN provider [2]: Select a number from 1 to 2.\nSelect a VPN provider [2]: "
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	field.Listed = true
	answers, out, _ = run(t, "1\n", flow.Screen{Fields: []flow.Field{field}})
	if answers["p"] != "a" || strings.Contains(out, "Alpha") {
		t.Errorf("listed choice printed options again: %q", out)
	}
}

func TestYesNoUsesItsDefault(t *testing.T) {
	notice := flow.Field{Key: "accept", Prompt: "Accept qBittorrent's legal notice?", Kind: flow.YesNo, Default: "yes"}
	answers, out, _ := run(t, "maybe\n\n", flow.Screen{Fields: []flow.Field{notice}})
	if answers["accept"] != "yes" || !strings.Contains(out, "[Y/n]: Enter Y or N.") {
		t.Errorf("answers = %v, output = %q", answers, out)
	}
	answers, _, _ = run(t, "n\n", flow.Screen{Fields: []flow.Field{notice}})
	if answers["accept"] != "no" {
		t.Errorf("answers = %v", answers)
	}
}

func TestClosedInputCancels(t *testing.T) {
	_, _, err := run(t, "", flow.Screen{Fields: []flow.Field{{Key: "x", Prompt: "X"}}})
	if !errors.Is(err, flow.ErrCancelled) {
		t.Errorf("err = %v", err)
	}
}

func TestMaskedInputShowsStarsAndHandlesBackspace(t *testing.T) {
	var shown strings.Builder
	value, err := ReadMasked("Password: ", bufio.NewReader(strings.NewReader("ab\x7fc\x1b[Dé\r")), func(s string) { shown.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if value != "acé" || shown.String() != "Password: **\b \b**\r\n" {
		t.Errorf("value = %q, shown = %q", value, shown.String())
	}
}

func TestMaskedInputCancelsOnControlC(t *testing.T) {
	_, err := ReadMasked("Password: ", bufio.NewReader(strings.NewReader("ab\x03")), func(string) {})
	if !errors.Is(err, flow.ErrCancelled) {
		t.Errorf("err = %v", err)
	}
}
