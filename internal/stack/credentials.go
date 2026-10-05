package stack

import (
	"fmt"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/settings"
)

// CredentialGroups lists the groups' names in registry order.
func CredentialGroups(apps []*app.App) []string {
	var names []string
	for _, a := range apps {
		if a.Credentials != nil {
			names = append(names, a.Credentials.Name)
		}
	}
	return names
}

// Credentials shows one group's saved values and offers to change them.
// Without a group, it lists the groups.
func (s *Stack) Credentials(name string) error {
	if name == "" {
		s.UI.Say("Choose a group: " + strings.Join(CredentialGroups(s.Apps), ", ") + "\nPasswords and API keys appear only when you choose a group.")
		return nil
	}
	var owner *app.App
	for _, a := range s.Apps {
		if a.Credentials != nil && a.Credentials.Name == name {
			owner = a
		}
	}
	if owner == nil {
		return fmt.Errorf("Unknown group: %s.", name)
	}
	saved, err := s.readSaved()
	if err != nil {
		return err
	}
	group, values := owner.Credentials, app.ValuesFor(saved, owner)

	var shown []app.CredentialField
	for _, field := range group.Fields {
		if (group.Visible == nil || group.Visible(values, field)) && values.Has(field.Key) {
			shown = append(shown, field)
		}
	}
	if len(shown) == 0 {
		s.UI.Say("No saved values for " + name + ".")
		return nil
	}
	var listing []string
	var fields []flow.Field
	for _, field := range shown {
		listing = append(listing, field.Label+": "+values.Get(field.Key))
		if field.Kind != app.Fixed {
			fields = append(fields, newValueField(field))
		}
	}
	screen := flow.Screen{Body: strings.Join(listing, "\n")}
	if len(fields) == 0 {
		return flow.Show(s.UI, screen)
	}
	screen.Fields = []flow.Field{{Key: "change", Prompt: "Change these values?", Kind: flow.YesNo, Default: "no"}}
	answers, err := s.UI.Ask(screen)
	if err != nil || answers["change"] != "yes" {
		return err
	}
	if answers, err = s.UI.Ask(flow.Screen{Fields: fields}); err != nil {
		return err
	}
	return s.saveChanges(saved, values, shown, answers)
}

func newValueField(field app.CredentialField) flow.Field {
	label := strings.ToLower(field.Label)
	result := flow.Field{Key: field.Key, Prompt: "New " + label + " [Enter keeps it]"}
	switch field.Kind {
	case app.Password:
		result.Kind = flow.Secret
		result.Repeat = "Repeat new " + label
		result.Mismatch = "Values do not match."
	case app.APIKey:
		result.Check = func(value string) (string, error) {
			if value == "" {
				return "", nil
			}
			return flow.ValidAPIKey(value)
		}
	}
	return result
}

func (s *Stack) saveChanges(saved *settings.Values, values app.Values, fields []app.CredentialField, answers flow.Answers) error {
	changed, usedByStack := false, false
	for _, field := range fields {
		value := answers[field.Key]
		if value == "" || value == values.Get(field.Key) {
			continue
		}
		values.Set(field.Key, value)
		changed = true
		usedByStack = usedByStack || field.UsedByStack
	}
	if !changed {
		s.UI.Say("No changes.")
		return nil
	}
	if err := settings.Write(s.envFile(), saved); err != nil {
		return err
	}
	s.UI.Say("Saved.")
	if usedByStack {
		s.UI.Say("The stack uses the changed values after its next start.")
	}
	return nil
}
