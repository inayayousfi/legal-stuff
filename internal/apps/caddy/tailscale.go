package caddy

// This file runs the Tailscale commands on the host that Tailscale mode uses
// for the address and its HTTPS certificate.

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"

	"github.com/inayayousfi/legal-stuff/internal/shell"
)

// tailscaleServeConflict explains why Tailscale Serve blocks the stack.
const tailscaleServeConflict = "Tailscale Serve already uses HTTPS port 443 on this computer, so the stack cannot use it. " +
	"Remove the existing Tailscale Serve configuration with this command:\n" +
	"tailscale serve reset"

// tailscaleDNSName returns this computer's Tailscale name, or "" when Tailscale
// is missing or signed out.
func tailscaleDNSName(s shell.Shell) string {
	tailscale, ok := shell.Found("tailscale")
	if !ok {
		return ""
	}
	result, err := shell.Capture(s, tailscale, "status", "--self", "--json")
	if err != nil || result.Code != 0 {
		return ""
	}
	var status struct{ Self struct{ DNSName string } }
	if json.Unmarshal([]byte(result.Stdout), &status) != nil {
		return ""
	}
	return strings.TrimSuffix(status.Self.DNSName, ".")
}

// tailscaleServeUsesHTTPS reports whether Tailscale Serve already holds port 443.
func tailscaleServeUsesHTTPS(s shell.Shell) bool {
	tailscale, ok := shell.Found("tailscale")
	if !ok {
		return false
	}
	result, err := shell.Capture(s, tailscale, "serve", "status", "--json")
	if err != nil || result.Code != 0 || strings.TrimSpace(result.Stdout) == "" {
		return false
	}
	var status struct{ TCP map[string]any }
	if json.Unmarshal([]byte(result.Stdout), &status) != nil {
		return false
	}
	_, used := status.TCP["443"]
	return used
}

// grantTailscaleOperator lets the current Linux user fetch certificates. It
// does nothing on other systems.
func grantTailscaleOperator(s shell.Shell, say func(string)) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	user := os.Getenv("USER")
	if user == "" {
		return errors.New("Cannot determine the current Linux user.")
	}
	say("Tailscale needs permission for this user to fetch certificates. sudo asks for your password.")
	tailscale, ok := shell.Found("tailscale")
	if !ok {
		tailscale = "tailscale"
	}
	_, err := s.Run(shell.Cmd{Args: []string{"sudo", tailscale, "set", "--operator=" + user}, Terminal: true, Check: true})
	return err
}

// tailscaleCert writes the HTTPS certificate for host and reports whether
// either file changed.
func tailscaleCert(s shell.Shell, certFile, keyFile, host string) (bool, error) {
	tailscale, ok := shell.Found("tailscale")
	if !ok {
		return false, errors.New("Tailscale is not installed, so the HTTPS certificate cannot be renewed.")
	}
	if tailscaleServeUsesHTTPS(s) {
		return false, errors.New(tailscaleServeConflict)
	}
	before := [2]string{readOrEmpty(certFile), readOrEmpty(keyFile)}
	result, err := shell.Capture(s, tailscale, "cert", "--cert-file", certFile, "--key-file", keyFile, host)
	if err != nil {
		return false, err
	}
	if result.Code != 0 {
		message := result.Stderr
		if message == "" {
			message = result.Stdout
		}
		return false, errors.New("Tailscale did not issue the HTTPS certificate: " + strings.TrimSpace(message))
	}
	return [2]string{readOrEmpty(certFile), readOrEmpty(keyFile)} != before, nil
}

func readOrEmpty(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return "\x00missing"
	}
	return string(content)
}
