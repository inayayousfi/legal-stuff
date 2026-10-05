package stack

import (
	"fmt"
	"strings"

	"github.com/inayayousfi/legal-stuff/internal/app"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/settings"
)

// CredentialGroups lists the groups in registry order.
func (s *Stack) CredentialGroups() []*app.CredentialGroup {
	var groups []*app.CredentialGroup
	for _, a := range s.Apps {
		if a.Credentials != nil {
			groups = append(groups, a.Credentials)
		}
	}
	return groups
}

// Credentials shows one group's saved values and offers to change them.
// Without a group, it lists the groups.
func (s *Stack) Credentials(name string) error {
	groups := s.CredentialGroups()
	if name == "" {
		var names []string
		for _, group := range groups {
			names = append(names, group.Name)
		}
		s.UI.Say("Choose a group: " + strings.Join(names, ", ") + "\nPasswords and API keys appear only when you choose a group.")
		return nil
	}
	var group *app.CredentialGroup
	for _, candidate := range groups {
		if candidate.Name == name {
			group = candidate
		}
	}
	if group == nil {
		return fmt.Errorf("Unknown group: %s.", name)
	}
	values, err := s.readSaved()
	if err != nil {
		return err
	}

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
	return s.saveChanges(values, shown, answers)
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
			return flow.APIKey(value)
		}
	}
	return result
}

func (s *Stack) saveChanges(values *settings.Values, fields []app.CredentialField, answers flow.Answers) error {
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
	if err := settings.Write(s.envFile(), values); err != nil {
		return err
	}
	s.UI.Say("Saved.")
	if usedByStack {
		s.UI.Say("The stack uses the changed values after its next start.")
	}
	return nil
}
