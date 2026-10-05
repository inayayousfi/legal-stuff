// Command selfnook sets up and operates the self-hosted stack. Without a
// command it opens the full-screen interface.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/inayayousfi/selfnook/internal/apps"
	"github.com/inayayousfi/selfnook/internal/commands"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/shell"
	"github.com/inayayousfi/selfnook/internal/stack"
	"github.com/inayayousfi/selfnook/internal/ui/cli"
	"github.com/inayayousfi/selfnook/internal/ui/tui"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	root, program, err := locate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if len(args) == 0 && interactive {
		return report(tui.Run(func(ctx context.Context, ui flow.UI, output io.Writer, take func(func() error) error) *stack.Stack {
			return &stack.Stack{
				Root:    root,
				Apps:    apps.All,
				Shell:   &shell.OS{Context: ctx, Dir: root, Output: output, TakeTerminal: take},
				UI:      ui,
				Program: program,
			}
		}, stack.CredentialGroups(apps.All)))
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printHelp(os.Stdout)
		return 0
	}

	index := slices.IndexFunc(commands.All, func(c commands.Command) bool { return c.Name == args[0] })
	if index < 0 {
		fmt.Fprintf(os.Stderr, "Error: Unknown command '%s'.\n\n", args[0])
		printHelp(os.Stderr)
		return 2
	}
	chosen, rest := commands.All[index], args[1:]
	if slices.Contains(rest, "-h") || slices.Contains(rest, "--help") {
		usage := chosen.Name
		if chosen.Group {
			usage += " [group]"
		}
		fmt.Printf("Usage: %s %s\n\n%s\n", stack.Command, usage, chosen.Detail)
		return 0
	}
	if chosen.Name == "start" && len(rest) > 0 && rest[0] == "--log-file" {
		if len(rest) != 2 {
			fmt.Fprintln(os.Stderr, "Error: --log-file needs a file path.")
			return 2
		}
		if err := logTo(rest[1]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		rest = nil
	}
	allowed := 0
	if chosen.Group {
		allowed = 1
	}
	if len(rest) > allowed {
		fmt.Fprintf(os.Stderr, "Error: Unexpected argument '%s'. Add -h after the command for its usage.\n", rest[allowed])
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	go func() {
		<-interrupts
		cancel()
		fmt.Fprintln(os.Stderr, "\nCommand cancelled.")
		os.Exit(130)
	}()
	s := &stack.Stack{
		Root:    root,
		Apps:    apps.All,
		Shell:   &shell.OS{Context: ctx, Dir: root, Output: os.Stdout},
		UI:      cli.New(),
		Program: program,
	}
	return report(chosen.Run(s, rest))
}

// locate returns the folder that holds the stack, which is the folder of
// this program, and the program's own path for automatic start.
func locate() (string, string, error) {
	program, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	if resolved, err := filepath.EvalSymlinks(program); err == nil {
		program = resolved
	}
	return filepath.Dir(program), program, nil
}

func report(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flow.ErrCancelled):
		fmt.Fprintln(os.Stderr, "\nCommand cancelled.")
		return 130
	}
	fmt.Fprintln(os.Stderr, "Error:", err)
	return 1
}

// logTo appends all output to path, for automatic start where no terminal exists.
func logTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	os.Stdout, os.Stderr = file, file
	fmt.Fprintf(file, "\n[%s] %s start\n", time.Now().Format("2006-01-02 15:04:05"), stack.Command)
	return nil
}

func printHelp(out io.Writer) {
	fmt.Fprintf(out, "Set up and operate the self-hosted stack.\n\nUsage:\n  %s            open the full-screen interface\n  %s <command>\n\nCommands:\n", stack.Command, stack.Command)
	width := 0
	for _, c := range commands.All {
		width = max(width, len(c.Name))
	}
	for _, c := range commands.All {
		fmt.Fprintf(out, "  %-*s  %s\n", width, c.Name, c.Help)
	}
	fmt.Fprintf(out, "  %-*s  %s\n", width, "help", "Show this help. Add -h after a command for its details.")
	fmt.Fprintf(out, "\n'credentials' takes a group name: %s.\nWithout a group it lists the groups. Passwords and API keys appear only when you choose a group.\n", strings.Join(stack.CredentialGroups(apps.All), ", "))
}
