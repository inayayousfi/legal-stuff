// Package commands lists what the program does, for both the command line
// and the full-screen interface.
package commands

import (
	"github.com/inayayousfi/selfnook/internal/apps/vpn"
	"github.com/inayayousfi/selfnook/internal/stack"
)

// Command is one action of the program.
type Command struct {
	// Name is the command-line name; Label is the menu entry.
	Name  string
	Label string
	// Help is the one-line summary; Detail is shown by -h.
	Help   string
	Detail string
	// Group means the command takes a credential group name as its only
	// argument, which the full-screen interface asks for in a list.
	Group bool
	Run   func(s *stack.Stack, args []string) error
}

var All = []Command{
	{
		Name: "setup", Label: "Setup",
		Help:   "Configure credentials, services, Recyclarr, and automatic startup.",
		Detail: "Configure credentials, start the services, apply Recyclarr profiles, and install automatic startup.",
		Run:    func(s *stack.Stack, _ []string) error { return s.Setup() },
	},
	{
		Name: "start", Label: "Start",
		Help:   "Start the services and synchronize Recyclarr.",
		Detail: "Start the media services, wait for Sonarr and Radarr, then synchronize Recyclarr.",
		Run:    func(s *stack.Stack, _ []string) error { return s.Start() },
	},
	{
		Name: "stop", Label: "Stop",
		Help:   "Stop and remove the stack containers.",
		Detail: "Stop and remove the media stack containers and network while preserving configuration and media files.",
		Run:    func(s *stack.Stack, _ []string) error { return s.Stop() },
	},
	{
		Name: "status", Label: "Status",
		Help:   "Show the current service status.",
		Detail: "Show the current Docker Compose status for every media stack service.",
		Run:    func(s *stack.Stack, _ []string) error { return s.Status() },
	},
	{
		Name: "vpn-status", Label: "VPN status",
		Help:   "Show the selected VPN gateway and its connection status.",
		Detail: "Check the selected VPN gateway health or Tailscale exit-node availability.",
		Run:    func(s *stack.Stack, _ []string) error { return s.AppStatus(vpn.App) },
	},
	{
		Name: "credentials", Label: "Credentials",
		Help:   "List or change the saved values for one service.",
		Detail: "Show the saved values for a service, vpn, or access. Answer y to type a new value for any credential, pressing Enter to keep a value. Changes are saved to .env only; change the password in the application itself as well.",
		Group:  true,
		Run: func(s *stack.Stack, args []string) error {
			group := ""
			if len(args) > 0 {
				group = args[0]
			}
			return s.Credentials(group)
		},
	},
}
