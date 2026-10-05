// Package tui presents the commands as a full-screen terminal interface.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/stack"
)

// Factory builds the stack a command runs with: its interface, where command
// output goes, how a command takes the terminal, and its cancellation.
type Factory func(ctx context.Context, ui flow.UI, output io.Writer, takeTerminal func(func() error) error) *stack.Stack

// ErrInterrupted reports that the user quit while a command was running.
var ErrInterrupted = errors.New("Command cancelled.")

// Run shows the menu until the user quits.
func Run(factory Factory) error {
	holder := &programHolder{}
	m := &model{factory: factory, holder: holder}
	program := tea.NewProgram(m)
	holder.program = program
	final, err := program.Run()
	if err != nil {
		return err
	}
	if final.(*model).interrupted {
		return ErrInterrupted
	}
	return nil
}

type programHolder struct{ program *tea.Program }

type action struct {
	label string
	help  string
	run   func(s *stack.Stack) error
}

var actions = []action{
	{"Setup", "Configure credentials, start the services, apply Recyclarr profiles, and install automatic startup.", (*stack.Stack).Setup},
	{"Start", "Start the services and synchronize Recyclarr.", (*stack.Stack).Start},
	{"Stop", "Stop and remove the stack containers.", (*stack.Stack).Stop},
	{"Status", "Show the current service status.", (*stack.Stack).Status},
	{"VPN status", "Show the selected VPN gateway and its connection status.", (*stack.Stack).VPNStatus},
	{"Credentials", "List or change the saved values for one service.", nil},
	{"Quit", "Leave this interface.", nil},
}

type view int

const (
	menuView view = iota
	groupView
	runView
)

type model struct {
	factory Factory
	holder  *programHolder
	width   int
	height  int

	view   view
	cursor int
	groups []string

	title       string
	log         []string
	ask         *asking
	running     bool
	err         error
	cancel      context.CancelFunc
	interrupted bool
}

// asking holds the screen being answered.
type asking struct {
	screen  flow.Screen
	reply   chan askReply
	waiting bool
	index   int
	repeat  bool
	first   string
	input   textinput.Model
	choice  int
	message string
	answers flow.Answers
}

var (
	bold   = lipgloss.NewStyle().Bold(true)
	dim    = lipgloss.NewStyle().Faint(true)
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	failed = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case logMsg:
		m.log = append(m.log, string(msg))
		return m, nil
	case askMsg:
		return m, m.beginAsk(msg)
	case doneMsg:
		m.running, m.err = false, msg.err
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, m.quit()
		}
		switch m.view {
		case menuView:
			return m, m.menuKey(msg)
		case groupView:
			return m, m.groupKey(msg)
		case runView:
			return m, m.runKey(msg)
		}
	}
	if m.ask != nil {
		var cmd tea.Cmd
		m.ask.input, cmd = m.ask.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) quit() tea.Cmd {
	if m.running {
		m.interrupted = true
		m.cancel()
		if m.ask != nil {
			m.ask.reply <- askReply{err: flow.ErrCancelled}
			m.ask = nil
		}
	}
	return tea.Quit
}

func moveCursor(key string, cursor, count int) int {
	switch key {
	case "up", "k":
		if cursor > 0 {
			return cursor - 1
		}
	case "down", "j":
		if cursor < count-1 {
			return cursor + 1
		}
	}
	return cursor
}

func (m *model) menuKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "q" || key == "esc" {
		return tea.Quit
	}
	m.cursor = moveCursor(key, m.cursor, len(actions))
	if key != "enter" {
		return nil
	}
	chosen := actions[m.cursor]
	switch chosen.label {
	case "Quit":
		return tea.Quit
	case "Credentials":
		m.groups = nil
		for _, group := range m.factory(context.Background(), nil, nil, nil).CredentialGroups() {
			m.groups = append(m.groups, group.Name)
		}
		m.view, m.cursor = groupView, 0
		return nil
	}
	m.start(chosen.label, chosen.run)
	return nil
}

func (m *model) groupKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "esc" || key == "q" {
		m.view, m.cursor = menuView, len(actions)-2
		return nil
	}
	m.cursor = moveCursor(key, m.cursor, len(m.groups))
	if key == "enter" {
		group := m.groups[m.cursor]
		m.start("Credentials: "+group, func(s *stack.Stack) error { return s.Credentials(group) })
	}
	return nil
}

// start runs a command in its own goroutine, connected through a bridge.
func (m *model) start(title string, run func(s *stack.Stack) error) {
	ctx, cancel := context.WithCancel(context.Background())
	m.view, m.title, m.log, m.err, m.running, m.cancel = runView, title, nil, nil, true, cancel
	b := newBridge(m.holder.program)
	s := m.factory(ctx, b, b.output, b.takeTerminal)
	program := m.holder.program
	go func() {
		err := run(s)
		cancel()
		program.Send(doneMsg{err: err})
	}()
}

func (m *model) runKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if !m.running && m.ask == nil {
		if key == "enter" || key == "esc" || key == "q" {
			m.view, m.cursor = menuView, 0
		}
		return nil
	}
	if m.ask == nil {
		return nil
	}
	if key == "esc" {
		m.ask.reply <- askReply{err: flow.ErrCancelled}
		m.ask = nil
		return nil
	}
	return m.answerKey(msg)
}

// beginAsk shows a screen. A screen with nothing to answer goes to the log at once.
func (m *model) beginAsk(msg askMsg) tea.Cmd {
	a := &asking{screen: msg.screen, reply: msg.reply, waiting: msg.screen.Wait != "", answers: flow.Answers{}}
	if !a.waiting && len(a.screen.Fields) == 0 {
		m.logScreen(a.screen)
		a.reply <- askReply{answers: a.answers}
		return nil
	}
	m.ask = a
	if !a.waiting {
		return m.prepareField()
	}
	return nil
}

func (m *model) logScreen(screen flow.Screen) {
	if screen.Title != "" {
		m.log = append(m.log, "", bold.Render(screen.Title), "")
	}
	if screen.Body != "" {
		m.log = append(m.log, strings.Split(screen.Body, "\n")...)
	}
}

func (m *model) prepareField() tea.Cmd {
	a := m.ask
	field := a.screen.Fields[a.index]
	a.message, a.repeat, a.first = "", false, ""
	a.choice = 0
	for i, option := range field.Options {
		if option.Value == field.Default {
			a.choice = i
		}
	}
	a.input = textinput.New()
	a.input.Prompt = ""
	a.input.Placeholder = field.Default
	if field.Kind == flow.Secret {
		a.input.EchoMode = textinput.EchoPassword
		a.input.EchoCharacter = '*'
	}
	return a.input.Focus()
}

func (m *model) answerKey(msg tea.KeyPressMsg) tea.Cmd {
	a := m.ask
	key := msg.String()
	if a.waiting {
		if key == "enter" {
			a.waiting = false
			if len(a.screen.Fields) == 0 {
				return m.finishAsk()
			}
			return m.prepareField()
		}
		return nil
	}
	field := a.screen.Fields[a.index]
	switch field.Kind {
	case flow.Choice:
		a.choice = moveCursor(key, a.choice, len(field.Options))
		if number, err := strconv.Atoi(key); err == nil && number >= 1 && number <= len(field.Options) {
			a.choice = number - 1
		}
		if key == "enter" {
			return m.accept(field.Options[a.choice].Value)
		}
		return nil
	case flow.YesNo:
		switch strings.ToLower(key) {
		case "y":
			return m.accept("yes")
		case "n":
			return m.accept("no")
		case "enter":
			return m.accept(field.Default)
		}
		return nil
	}
	if key != "enter" {
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(msg)
		return cmd
	}
	value := a.input.Value()
	if field.Kind == flow.Text {
		value = strings.TrimSpace(value)
		if value == "" {
			value = field.Default
		}
	}
	if a.repeat {
		if value != a.first {
			cmd := m.prepareField()
			a.message = field.Mismatch
			return cmd
		}
		return m.accept(value)
	}
	if field.Check != nil {
		checked, err := field.Check(value)
		if err != nil {
			a.message = strings.TrimPrefix(err.Error(), "Error: ")
			a.input.SetValue("")
			return nil
		}
		value = checked
	}
	if field.Kind == flow.Secret && field.Repeat != "" && value != "" {
		a.repeat, a.first, a.message = true, value, ""
		a.input.SetValue("")
		return nil
	}
	return m.accept(value)
}

func (m *model) accept(value string) tea.Cmd {
	a := m.ask
	a.answers[a.screen.Fields[a.index].Key] = value
	a.index++
	if a.index < len(a.screen.Fields) {
		return m.prepareField()
	}
	return m.finishAsk()
}

func (m *model) finishAsk() tea.Cmd {
	a := m.ask
	m.logScreen(a.screen)
	for _, field := range a.screen.Fields {
		switch field.Kind {
		case flow.Secret:
		case flow.Choice:
			for _, option := range field.Options {
				if option.Value == a.answers[field.Key] {
					m.log = append(m.log, field.Prompt+": "+option.Label)
				}
			}
		default:
			m.log = append(m.log, field.Prompt+": "+a.answers[field.Key])
		}
	}
	a.reply <- askReply{answers: a.answers}
	m.ask = nil
	return nil
}

func (m *model) View() tea.View {
	var content string
	switch m.view {
	case menuView:
		content = m.menuContent()
	case groupView:
		content = m.groupContent()
	default:
		content = m.runContent()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m *model) wrap(text string) string {
	if m.width <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(m.width).Render(text)
}

func (m *model) menuContent() string {
	var out strings.Builder
	out.WriteString(accent.Render(stack.Command) + "\n\n")
	for i, a := range actions {
		pointer := "  "
		label := a.label
		if i == m.cursor {
			pointer, label = accent.Render("> "), bold.Render(a.label)
		}
		out.WriteString(pointer + label + "\n")
	}
	out.WriteString("\n" + m.wrap(dim.Render(actions[m.cursor].help)) + "\n\n")
	out.WriteString(dim.Render("↑/↓ move · Enter open · q quit"))
	return out.String()
}

func (m *model) groupContent() string {
	var out strings.Builder
	out.WriteString(accent.Render("Credentials") + "\n\n")
	for i, group := range m.groups {
		pointer, label := "  ", group
		if i == m.cursor {
			pointer, label = accent.Render("> "), bold.Render(group)
		}
		out.WriteString(pointer + label + "\n")
	}
	out.WriteString("\n" + dim.Render("Passwords and API keys appear only when you choose a group.") + "\n\n")
	out.WriteString(dim.Render("↑/↓ move · Enter open · Esc back"))
	return out.String()
}

func (m *model) runContent() string {
	header := accent.Render(m.title)
	var bottom strings.Builder
	if m.ask != nil {
		bottom.WriteString(m.askContent())
	} else if m.running {
		bottom.WriteString(dim.Render("Working… Ctrl+C quits and stops the command."))
	} else {
		if errors.Is(m.err, flow.ErrCancelled) {
			bottom.WriteString(failed.Render("Command cancelled."))
		} else if m.err != nil {
			bottom.WriteString(failed.Render(m.wrap("Error: " + m.err.Error())))
		} else {
			bottom.WriteString(bold.Render("Done."))
		}
		bottom.WriteString("\n" + dim.Render("Enter returns to the menu."))
	}
	tail := bottom.String()
	room := m.height - 2 - strings.Count(tail, "\n") - 1
	logText := m.wrap(strings.Join(m.log, "\n"))
	lines := strings.Split(logText, "\n")
	if room < 0 {
		room = 0
	}
	if len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	return header + "\n" + strings.Join(lines, "\n") + "\n" + tail
}

func (m *model) askContent() string {
	a := m.ask
	var out strings.Builder
	if a.screen.Title != "" {
		out.WriteString("\n" + bold.Render(a.screen.Title) + "\n\n")
	}
	if a.screen.Body != "" {
		out.WriteString(m.wrap(a.screen.Body) + "\n")
	}
	if a.waiting {
		out.WriteString("\n" + accent.Render(fmt.Sprintf("Press Enter when %s is configured.", a.screen.Wait)))
		return out.String()
	}
	field := a.screen.Fields[a.index]
	out.WriteString("\n")
	switch field.Kind {
	case flow.Choice:
		out.WriteString(bold.Render(field.Prompt) + "\n")
		for i, option := range field.Options {
			pointer, label := "  ", fmt.Sprintf("%d. %s", i+1, option.Label)
			if i == a.choice {
				pointer, label = accent.Render("> "), bold.Render(label)
			}
			out.WriteString(pointer + label + "\n")
			if option.Detail != "" {
				out.WriteString(m.wrap(dim.Render(indent(option.Detail, "     "))) + "\n")
			}
		}
		out.WriteString(dim.Render("↑/↓ or number to choose · Enter confirm · Esc cancel"))
	case flow.YesNo:
		hint := "[y/N]"
		if field.Default == "yes" {
			hint = "[Y/n]"
		}
		out.WriteString(bold.Render(field.Prompt) + " " + hint + "\n")
		out.WriteString(dim.Render("y or n · Enter keeps the default · Esc cancel"))
	default:
		prompt := field.Prompt
		if a.repeat {
			prompt = field.Repeat
		}
		out.WriteString(bold.Render(prompt) + "\n" + accent.Render("> ") + a.input.View() + "\n")
		if a.message != "" {
			out.WriteString(failed.Render(a.message) + "\n")
		}
		out.WriteString(dim.Render("Enter confirm · Esc cancel"))
	}
	return out.String()
}

func indent(text, prefix string) string {
	return prefix + strings.ReplaceAll(text, "\n", "\n"+prefix)
}
