// Command selfhost sets up and operates the self-hosted stack. Without a
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

	"github.com/inayayousfi/legal-stuff/internal/apps"
	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/shell"
	"github.com/inayayousfi/legal-stuff/internal/stack"
	"github.com/inayayousfi/legal-stuff/internal/ui/cli"
	"github.com/inayayousfi/legal-stuff/internal/ui/tui"
)

type command struct {
	name   string
	help   string
	detail string
	run    func(s *stack.Stack, args []string) error
}

var commands = []command{
	{"setup", "Configure credentials, services, Recyclarr, and automatic startup.", "Configure credentials, start the services, apply Recyclarr profiles, and install automatic startup.", func(s *stack.Stack, _ []string) error { return s.Setup() }},
	{"start", "Start the services and synchronize Recyclarr.", "Start the media services, wait for Sonarr and Radarr, then synchronize Recyclarr.", func(s *stack.Stack, _ []string) error { return s.Start() }},
	{"stop", "Stop and remove the stack containers.", "Stop and remove the media stack containers and network while preserving configuration and media files.", func(s *stack.Stack, _ []string) error { return s.Stop() }},
	{"status", "Show the current service status.", "Show the current Docker Compose status for every media stack service.", func(s *stack.Stack, _ []string) error { return s.Status() }},
	{"vpn-status", "Show the selected VPN gateway and its connection status.", "Check the selected VPN gateway health or Tailscale exit-node availability.", func(s *stack.Stack, _ []string) error { return s.VPNStatus() }},
	{"credentials", "List or change the saved values for one service.", "Show the saved values for a service, vpn, or access. Answer y to type a new value for any credential, pressing Enter to keep a value. Changes are saved to .env only; change the password in the application itself as well.", func(s *stack.Stack, args []string) error {
		group := ""
		if len(args) > 0 {
			group = args[0]
		}
		return s.Credentials(group)
	}},
}

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
		}))
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printHelp(os.Stdout)
		return 0
	}

	var chosen *command
	for i := range commands {
		if commands[i].name == args[0] {
			chosen = &commands[i]
		}
	}
	if chosen == nil {
		fmt.Fprintf(os.Stderr, "Error: Unknown command '%s'.\n\n", args[0])
		printHelp(os.Stderr)
		return 2
	}
	rest := args[1:]
	if slices.Contains(rest, "-h") || slices.Contains(rest, "--help") {
		usage := chosen.name
		if chosen.name == "credentials" {
			usage += " [group]"
		}
		fmt.Printf("Usage: %s %s\n\n%s\n", stack.Command, usage, chosen.detail)
		return 0
	}
	if chosen.name == "start" && len(rest) == 2 && rest[0] == "--log-file" {
		if err := logTo(rest[1]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		rest = nil
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
	return report(chosen.run(s, rest))
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
	case errors.Is(err, flow.ErrCancelled), errors.Is(err, tui.ErrInterrupted):
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
	for _, c := range commands {
		width = max(width, len(c.name))
	}
	for _, c := range commands {
		fmt.Fprintf(out, "  %-*s  %s\n", width, c.name, c.help)
	}
	fmt.Fprintf(out, "  %-*s  %s\n", width, "help", "Show this help. Add -h after a command for its details.")
	var groups []string
	for _, a := range apps.All {
		if a.Credentials != nil {
			groups = append(groups, a.Credentials.Name)
		}
	}
	fmt.Fprintf(out, "\n'credentials' takes a group name: %s.\nWithout a group it lists the groups. Passwords and API keys appear only when you choose a group.\n", strings.Join(groups, ", "))
}
