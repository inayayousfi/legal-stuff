// Package shell runs external programs such as docker, tailscale, and systemctl.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Cmd describes one program run.
type Cmd struct {
	Args []string
	// Capture collects output into the Result instead of showing it.
	Capture bool
	// Terminal gives the program the user's terminal, for commands such as
	// sudo that ask for a password.
	Terminal bool
	// Check turns a non-zero exit code into an error.
	Check bool
}

type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// Shell runs commands. Tests replace it to record calls.
type Shell interface {
	Run(Cmd) (Result, error)
}

// ExitError reports a checked command that failed.
type ExitError struct {
	Args []string
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("Command '%s' returned non-zero exit status %d.", strings.Join(e.Args, " "), e.Code)
}

// OS runs commands as real processes in Dir.
type OS struct {
	Context context.Context
	Dir     string
	// Output receives the output of commands that neither capture it nor use the terminal.
	Output io.Writer
	// TakeTerminal wraps a Terminal command, so a full-screen interface can
	// hand over the terminal and take it back. Nil runs the command directly.
	TakeTerminal func(run func() error) error
}

func (s *OS) Run(c Cmd) (Result, error) {
	ctx := s.Context
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.CommandContext(ctx, c.Args[0], c.Args[1:]...)
	command.Dir = s.Dir
	var stdout, stderr bytes.Buffer
	run := command.Run
	switch {
	case c.Capture:
		command.Stdout, command.Stderr = &stdout, &stderr
	case c.Terminal:
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		if s.TakeTerminal != nil {
			run = func() error { return s.TakeTerminal(command.Run) }
		}
	default:
		output := s.Output
		if output == nil {
			output = os.Stdout
		}
		command.Stdout, command.Stderr = output, output
	}
	err := run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.Code = exit.ExitCode()
		if c.Check {
			return result, &ExitError{Args: c.Args, Code: result.Code}
		}
		return result, nil
	}
	return result, err
}

// Found reports whether name is on the search path, returning its full path.
func Found(name string) (string, bool) {
	path, err := exec.LookPath(name)
	return path, err == nil
}
