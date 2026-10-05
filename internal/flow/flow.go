// Package flow describes what a guided step shows and asks, and runs a chain
// of steps through a user interface. Steps never print or read input
// themselves, so the CLI and the TUI present the same steps.
package flow

import (
	"errors"
	"io"
)

type Kind int

const (
	// Text asks for a visible line of text.
	Text Kind = iota
	// Secret asks for a masked value.
	Secret
	// Choice asks for one of Options.
	Choice
	// YesNo asks for a yes or no answer, returned as "yes" or "no".
	YesNo
)

type Option struct {
	Value string
	Label string
	// Detail is extra text shown under the label, such as pros and cons.
	Detail string
}

// Field is one value the user enters.
type Field struct {
	Key    string
	Prompt string
	Kind   Kind
	// Default is used when the user enters nothing. For Choice it is an
	// option value; for YesNo it is "yes" or "no".
	Default string
	Options []Option
	// Listed means the options were just shown, so a line-based interface
	// asks again without listing them a second time.
	Listed bool
	// Repeat, for a Secret, asks for the value a second time. Mismatch is
	// shown when the two entries differ.
	Repeat   string
	Mismatch string
	// Check validates the entry and returns the value to keep. Its error
	// text is shown as is before asking again.
	Check func(string) (string, error)
}

// Screen is what one step shows: a guide, then an optional pause, then fields.
type Screen struct {
	Title string
	Body  string
	// Wait, when set, pauses until the user confirms that the named
	// application is configured, before the fields are asked.
	Wait   string
	Fields []Field
}

// Answers maps field keys to entered values.
type Answers map[string]string

// Step is a screen plus what happens with its answers. Then returns the next
// step, or nil when the chain is finished.
type Step struct {
	Screen
	Then func(Answers) (*Step, error)
}

// UI presents screens and reports progress.
type UI interface {
	// Ask shows the screen and returns its answers once every field is valid.
	Ask(Screen) (Answers, error)
	// Say shows a progress message.
	Say(text string)
	// Output receives the output of commands that run while a step works.
	Output() io.Writer
}

// ErrCancelled is returned when the user cancels an Ask.
var ErrCancelled = errors.New("Command cancelled.")

// Run presents step and every step that follows it.
func Run(ui UI, step *Step) error {
	for step != nil {
		answers, err := ui.Ask(step.Screen)
		if err != nil {
			return err
		}
		if step.Then == nil {
			return nil
		}
		if step, err = step.Then(answers); err != nil {
			return err
		}
	}
	return nil
}

// Show presents one screen that has nothing to answer, such as a guide with a pause.
func Show(ui UI, screen Screen) error {
	_, err := ui.Ask(screen)
	return err
}
