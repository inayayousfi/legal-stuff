// Package fake provides a recording shell and a scripted user interface for tests.
package fake

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

// Shell records every command. Respond, when set, chooses each result.
type Shell struct {
	mu       sync.Mutex
	Commands [][]string
	Respond  func(args []string) shell.Result
}

func (s *Shell) Run(c shell.Cmd) (shell.Result, error) {
	s.mu.Lock()
	s.Commands = append(s.Commands, slices.Clone(c.Args))
	s.mu.Unlock()
	result := shell.Result{}
	if s.Respond != nil {
		result = s.Respond(c.Args)
	}
	if c.Check && result.Code != 0 {
		return result, &shell.ExitError{Args: c.Args, Code: result.Code}
	}
	return result, nil
}

// Lines returns each recorded command joined by spaces.
func (s *Shell) Lines() []string {
	var lines []string
	for _, command := range s.Commands {
		lines = append(lines, strings.Join(command, " "))
	}
	return lines
}

// Index returns the position of the first command starting with prefix, or -1.
func (s *Shell) Index(prefix string) int {
	for i, line := range s.Lines() {
		if strings.HasPrefix(line, prefix) {
			return i
		}
	}
	return -1
}

// UI answers fields from Answers by field key, records every screen and
// message in order, and fails the ask when a field has no answer.
type UI struct {
	Answers map[string]string
	// Events lists "screen: <title>", "wait: <app>", "ask: <key>", and "say: <text>" in order.
	Events  []string
	Screens []flow.Screen
	Out     bytes.Buffer
	// OnAsk runs before each screen is answered, to inspect state at that moment.
	OnAsk func(flow.Screen)
}

func (u *UI) Ask(screen flow.Screen) (flow.Answers, error) {
	if u.OnAsk != nil {
		u.OnAsk(screen)
	}
	u.Screens = append(u.Screens, screen)
	if screen.Title != "" {
		u.Events = append(u.Events, "screen: "+screen.Title)
	}
	if screen.Wait != "" {
		u.Events = append(u.Events, "wait: "+screen.Wait)
	}
	answers := flow.Answers{}
	for _, field := range screen.Fields {
		u.Events = append(u.Events, "ask: "+field.Key)
		value, ok := u.Answers[field.Key]
		if !ok {
			return nil, fmt.Errorf("no scripted answer for %q", field.Key)
		}
		if field.Check != nil {
			checked, err := field.Check(value)
			if err != nil {
				return nil, fmt.Errorf("answer for %q rejected: %w", field.Key, err)
			}
			value = checked
		}
		answers[field.Key] = value
	}
	return answers, nil
}

func (u *UI) Say(text string) { u.Events = append(u.Events, "say: "+text) }

func (u *UI) Output() io.Writer { return &u.Out }

// Position returns the index of the first event equal to event, or -1.
func (u *UI) Position(event string) int {
	return slices.Index(u.Events, event)
}

// Text joins every screen's title and body.
func (u *UI) Text() string {
	var all strings.Builder
	for _, screen := range u.Screens {
		all.WriteString(screen.Title + "\n" + screen.Body + "\n")
	}
	return all.String()
}
