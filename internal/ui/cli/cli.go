// Package cli presents steps as printed text and line prompts.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/inayayousfi/legal-stuff/internal/flow"
)

// UI reads answers from In and prints to Out.
type UI struct {
	In  *bufio.Reader
	Out io.Writer
	// Terminal is the input terminal used to mask secrets. Zero reads secrets
	// as visible lines, as when input is not a terminal.
	Terminal int
	masked   bool
}

// New returns a UI on the process's terminal.
func New() *UI {
	fd := int(os.Stdin.Fd())
	return &UI{In: bufio.NewReader(os.Stdin), Out: os.Stdout, Terminal: fd, masked: term.IsTerminal(fd)}
}

func (u *UI) Say(text string) { fmt.Fprintln(u.Out, text) }

func (u *UI) Output() io.Writer { return u.Out }

func (u *UI) Ask(screen flow.Screen) (flow.Answers, error) {
	if screen.Title != "" {
		fmt.Fprintf(u.Out, "\n%s\n\n", screen.Title)
	}
	if screen.Body != "" {
		fmt.Fprintln(u.Out, screen.Body)
	}
	if screen.Wait != "" {
		if _, err := u.line(fmt.Sprintf("\nPress Enter when %s is configured.", screen.Wait)); err != nil {
			return nil, err
		}
	}
	answers := flow.Answers{}
	for _, field := range screen.Fields {
		value, err := u.field(field)
		if err != nil {
			return nil, err
		}
		answers[field.Key] = value
	}
	return answers, nil
}

func (u *UI) line(prompt string) (string, error) {
	fmt.Fprint(u.Out, prompt)
	text, err := u.In.ReadString('\n')
	if err != nil && (text == "" || !errors.Is(err, io.EOF)) {
		return "", flow.ErrCancelled
	}
	return strings.TrimSpace(text), nil
}

func (u *UI) field(field flow.Field) (string, error) {
	switch field.Kind {
	case flow.Choice:
		return u.choice(field)
	case flow.YesNo:
		return u.yesNo(field)
	case flow.Secret:
		return u.secret(field)
	}
	prompt := field.Prompt
	if field.Default != "" {
		prompt += " [" + field.Default + "]"
	}
	for {
		value, err := u.line(prompt + ": ")
		if err != nil {
			return "", err
		}
		if value == "" {
			value = field.Default
		}
		if value, err = check(field, value); err == nil {
			return value, nil
		}
		fmt.Fprintln(u.Out, err)
	}
}

func check(field flow.Field, value string) (string, error) {
	if field.Check == nil {
		return value, nil
	}
	return field.Check(value)
}

func (u *UI) readSecret(prompt string) (string, error) {
	if !u.masked {
		return u.line(prompt)
	}
	state, err := term.MakeRaw(u.Terminal)
	if err != nil {
		return "", err
	}
	defer term.Restore(u.Terminal, state)
	return ReadMasked(prompt, u.In, func(s string) { fmt.Fprint(u.Out, s) })
}

func (u *UI) secret(field flow.Field) (string, error) {
	for {
		value, err := u.readSecret(field.Prompt + ": ")
		if err != nil {
			return "", err
		}
		if value, err = check(field, value); err != nil {
			fmt.Fprintln(u.Out, err)
			continue
		}
		if field.Repeat == "" || value == "" {
			return value, nil
		}
		repeated, err := u.readSecret(field.Repeat + ": ")
		if err != nil {
			return "", err
		}
		if repeated == value {
			return value, nil
		}
		fmt.Fprintln(u.Out, field.Mismatch)
	}
}

func (u *UI) choice(field flow.Field) (string, error) {
	if !field.Listed {
		detailed := false
		for _, option := range field.Options {
			detailed = detailed || option.Detail != ""
		}
		for i, option := range field.Options {
			if detailed {
				fmt.Fprintf(u.Out, "\n%d. %s\n%s\n", i+1, option.Label, option.Detail)
			} else {
				fmt.Fprintf(u.Out, "%d. %s\n", i+1, option.Label)
			}
		}
		if detailed {
			fmt.Fprintln(u.Out)
		}
	}
	prompt := field.Prompt
	for i, option := range field.Options {
		if option.Value == field.Default {
			prompt += " [" + strconv.Itoa(i+1) + "]"
		}
	}
	for {
		answer, err := u.line(prompt + ": ")
		if err != nil {
			return "", err
		}
		if answer == "" && field.Default != "" {
			return field.Default, nil
		}
		if number, err := strconv.Atoi(answer); err == nil && number >= 1 && number <= len(field.Options) {
			return field.Options[number-1].Value, nil
		}
		fmt.Fprintf(u.Out, "Select a number from 1 to %d.\n", len(field.Options))
	}
}

func (u *UI) yesNo(field flow.Field) (string, error) {
	hint := " [y/N]"
	if field.Default == "yes" {
		hint = " [Y/n]"
	}
	for {
		answer, err := u.line(field.Prompt + hint + ": ")
		if err != nil {
			return "", err
		}
		switch strings.ToLower(answer) {
		case "":
			return field.Default, nil
		case "y", "yes":
			return "yes", nil
		case "n", "no":
			return "no", nil
		}
		fmt.Fprintln(u.Out, "Enter Y or N.")
	}
}
