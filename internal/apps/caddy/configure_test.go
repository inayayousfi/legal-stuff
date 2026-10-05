package caddy

import (
	"testing"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/fake"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

// A saved mode with an unusable address asks again instead of stopping setup.
func TestInvalidSavedAddressIsAskedAgain(t *testing.T) {
	saved := settings.NewValues()
	saved.Set(modeKey, Domain)
	saved.Set(hostKey, "localhost")
	ui := &fake.UI{Answers: map[string]string{"mode": Domain, "host": "media.example.com"}}
	e := &app.Env{Values: app.ValuesFor(saved, App), UI: ui, Shell: &fake.Shell{}}
	if err := configure(e); err != nil {
		t.Fatal(err)
	}
	if saved.Get(hostKey) != "media.example.com" {
		t.Errorf("host = %q, events = %v", saved.Get(hostKey), ui.Events)
	}
}
