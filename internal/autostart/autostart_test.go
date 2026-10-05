package autostart

import (
	"strings"
	"testing"
)

func TestUserUnitRunsAsItsOwnerAfterLogin(t *testing.T) {
	unit := SystemdUnit("/opt/stack/selfnook", "/opt/stack", "")
	if strings.Contains(unit, "User=") || strings.Contains(unit, "docker.service") || !strings.Contains(unit, "WantedBy=default.target") {
		t.Errorf("unit:\n%s", unit)
	}
	if !strings.Contains(unit, "ExecStart=/opt/stack/selfnook start --log-file /opt/stack/config/startup.log\n") {
		t.Errorf("unit:\n%s", unit)
	}
}

func TestSystemUnitRunsAsTheSelectedUserAfterDocker(t *testing.T) {
	unit := SystemdUnit("/opt/stack/selfnook", "/opt/stack", "media")
	for _, line := range []string{"User=media\n", "Requires=docker.service\n", "After=docker.service network-online.target\n", "WantedBy=multi-user.target\n"} {
		if !strings.Contains(unit, line) {
			t.Errorf("unit lacks %q", line)
		}
	}
}

func TestUnitQuotesPathsWithSpaces(t *testing.T) {
	unit := SystemdUnit("/home/me/my stack/selfnook", "/home/me/my stack", "")
	if !strings.Contains(unit, `ExecStart="/home/me/my stack/selfnook" start --log-file "/home/me/my stack/config/startup.log"`) {
		t.Errorf("unit:\n%s", unit)
	}
}

func TestWindowsLauncherStartsWithoutAWindow(t *testing.T) {
	launcher := WindowsLauncher(`C:\Media Stack\selfnook.exe`, `C:\Media Stack\config\startup.log`)
	want := "Set shell = CreateObject(\"WScript.Shell\")\nshell.Run \"\"\"C:\\Media Stack\\selfnook.exe\"\" start --log-file \"\"C:\\Media Stack\\config\\startup.log\"\"\", 0, False\n"
	if launcher != want {
		t.Errorf("launcher = %q", launcher)
	}
}
