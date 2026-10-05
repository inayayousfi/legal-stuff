package tui

import (
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/inayayousfi/legal-stuff/internal/flow"
)

// bridge is the flow.UI handed to a running command. The command runs in its
// own goroutine; the bridge forwards its screens and messages to the
// program and waits for the answers.
type bridge struct {
	program *tea.Program
	output  *lineWriter
}

type askMsg struct {
	screen flow.Screen
	reply  chan askReply
}

type askReply struct {
	answers flow.Answers
	err     error
}

type logMsg string

type doneMsg struct{ err error }

func newBridge(program *tea.Program) *bridge {
	return &bridge{program: program, output: &lineWriter{send: func(line string) { program.Send(logMsg(line)) }}}
}

func (b *bridge) Ask(screen flow.Screen) (flow.Answers, error) {
	reply := make(chan askReply)
	b.program.Send(askMsg{screen: screen, reply: reply})
	result := <-reply
	return result.answers, result.err
}

func (b *bridge) Say(text string) {
	for _, line := range strings.Split(text, "\n") {
		b.program.Send(logMsg(line))
	}
}

// takeTerminal lets a command such as sudo use the terminal directly.
func (b *bridge) takeTerminal(run func() error) error {
	if err := b.program.ReleaseTerminal(); err != nil {
		return err
	}
	err := run()
	if restoreErr := b.program.RestoreTerminal(); err == nil {
		err = restoreErr
	}
	return err
}

// lineWriter sends complete lines of command output to the log. Carriage
// returns used for progress redraws keep only the latest text of a line.
type lineWriter struct {
	mu      sync.Mutex
	pending string
	send    func(string)
}

func (w *lineWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(data)
	for {
		index := strings.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSuffix(w.pending[:index], "\r")
		if cut := strings.LastIndexByte(line, '\r'); cut >= 0 {
			line = line[cut+1:]
		}
		w.send(line)
		w.pending = w.pending[index+1:]
	}
	return len(data), nil
}
