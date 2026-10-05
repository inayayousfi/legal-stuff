// Package autostart makes the stack start with the computer.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/inayayousfi/legal-stuff/internal/flow"
	"github.com/inayayousfi/legal-stuff/internal/shell"
)

const unitName = "media-stack.service"

// SystemdUnit returns a unit that starts the stack with program. A non-empty
// user makes it a system unit that runs as that user after Docker.
func SystemdUnit(program, root, user string) string {
	var unit strings.Builder
	unit.WriteString("[Unit]\nDescription=Media stack\n")
	if user != "" {
		unit.WriteString("Requires=docker.service\nAfter=docker.service network-online.target\n")
	}
	unit.WriteString("\n[Service]\nType=oneshot\nRemainAfterExit=yes\n")
	if user != "" {
		fmt.Fprintf(&unit, "User=%s\n", user)
	}
	logFile := filepath.Join(root, "config", "startup.log")
	fmt.Fprintf(&unit, "WorkingDirectory=%s\n", systemdQuote(root))
	fmt.Fprintf(&unit, "ExecStart=%s start --log-file %s\n", systemdQuote(program), systemdQuote(logFile))
	fmt.Fprintf(&unit, "ExecStop=%s stop\n", systemdQuote(program))
	unit.WriteString("TimeoutStartSec=infinity\n\n[Install]\n")
	if user != "" {
		unit.WriteString("WantedBy=multi-user.target\n")
	} else {
		unit.WriteString("WantedBy=default.target\n")
	}
	return unit.String()
}

// systemdQuote quotes a path only when systemd would otherwise split it.
func systemdQuote(value string) string {
	if !strings.ContainsAny(value, " \t\"'\\") {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// WindowsLauncher returns a script that starts the stack without a visible window.
func WindowsLauncher(program, logFile string) string {
	command := fmt.Sprintf(`"%s" start --log-file "%s"`, program, logFile)
	escaped := strings.ReplaceAll(command, `"`, `""`)
	return "Set shell = CreateObject(\"WScript.Shell\")\n" +
		fmt.Sprintf("shell.Run \"%s\", 0, False\n", escaped)
}

const (
	userService   = "user"
	systemService = "system"
)

// Install makes the stack start with the computer.
func Install(s shell.Shell, ui flow.UI, program, root string) error {
	switch runtime.GOOS {
	case "windows":
		return installWindowsAutostart(ui, program, root)
	case "linux":
		return installLinuxAutostart(s, ui, program, root)
	}
	return errors.New("Automatic start is supported only on Windows and Linux.")
}

func installLinuxAutostart(s shell.Shell, ui flow.UI, program, root string) error {
	answers, err := ui.Ask(flow.Screen{
		Title: "Linux automatic start",
		Fields: []flow.Field{{
			Key:    "kind",
			Prompt: "Choose 1 or 2",
			Kind:   flow.Choice,
			Options: []flow.Option{
				{Value: userService, Label: "User service (starts with this user's systemd session)"},
				{Value: systemService, Label: "System service (starts at boot and requires sudo)"},
			},
		}},
	})
	if err != nil {
		return err
	}
	if answers["kind"] == userService {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		unitPath := filepath.Join(home, ".config", "systemd", "user", unitName)
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(unitPath, []byte(SystemdUnit(program, root, "")), 0o644); err != nil {
			return err
		}
		for _, args := range [][]string{
			{"systemctl", "--user", "daemon-reload"},
			{"systemctl", "--user", "enable", unitName},
		} {
			if _, err := s.Run(shell.Cmd{Args: args, Check: true}); err != nil {
				return err
			}
		}
		ui.Say("Installed " + unitPath)
		return nil
	}

	user := os.Getenv("USER")
	if user == "" {
		return errors.New("Cannot determine the current Linux user.")
	}
	temporary, err := os.CreateTemp("", "media-stack-*.service")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.WriteString(SystemdUnit(program, root, user))
	if err := errors.Join(writeErr, temporary.Close()); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"sudo", "install", "-m", "644", temporary.Name(), "/etc/systemd/system/" + unitName},
		{"sudo", "systemctl", "daemon-reload"},
		{"sudo", "systemctl", "enable", unitName},
	} {
		if _, err := s.Run(shell.Cmd{Args: args, Terminal: true, Check: true}); err != nil {
			return err
		}
	}
	ui.Say("Installed /etc/systemd/system/" + unitName)
	return nil
}

func installWindowsAutostart(ui flow.UI, program, root string) error {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return errors.New("APPDATA is unavailable; cannot find the Startup folder.")
	}
	startup := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if err := os.MkdirAll(startup, 0o755); err != nil {
		return err
	}
	launcher := filepath.Join(startup, "media-stack.vbs")
	content := WindowsLauncher(program, filepath.Join(root, "config", "startup.log"))
	if err := os.WriteFile(launcher, []byte(content), 0o644); err != nil {
		return err
	}
	ui.Say("Installed " + launcher)
	return nil
}
