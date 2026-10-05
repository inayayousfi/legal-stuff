package app

import (
	"testing"

	"github.com/inayayousfi/selfnook/internal/settings"
)

// An app changes only its own settings; changing another app's value stops the program.
func TestValuesChangeOnlyOwnedSettings(t *testing.T) {
	saved := settings.NewValues()
	saved.Set("OTHER", "kept")
	owner := &App{Name: "demo", Settings: []Setting{{Key: "MINE"}}}
	v := ValuesFor(saved, owner)
	v.Set("MINE", "changed")
	if saved.Get("MINE") != "changed" || v.Get("OTHER") != "kept" {
		t.Errorf("MINE = %q, OTHER = %q", saved.Get("MINE"), v.Get("OTHER"))
	}
	defer func() {
		if recover() == nil || saved.Get("OTHER") != "kept" {
			t.Errorf("OTHER = %q after an unowned change", saved.Get("OTHER"))
		}
	}()
	v.Set("OTHER", "taken")
}
