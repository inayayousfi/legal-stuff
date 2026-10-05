package shell

import (
	"errors"
	"time"
)

// Compose runs "docker compose" with args and fails on a non-zero exit code.
func Compose(s Shell, args ...string) error {
	_, err := s.Run(Cmd{Args: append([]string{"docker", "compose"}, args...), Check: true})
	return err
}

// ComposeCapture runs "docker compose" with args, collecting its output without checking the exit code.
func ComposeCapture(s Shell, args ...string) (Result, error) {
	return s.Run(Cmd{Args: append([]string{"docker", "compose"}, args...), Capture: true})
}

// Capture runs args, collecting output without checking the exit code.
func Capture(s Shell, args ...string) (Result, error) {
	return s.Run(Cmd{Args: args, Capture: true})
}

// DockerWait is how long WaitForDocker waits for the Docker engine to start.
const DockerWait = 5 * time.Minute

// WaitForDocker waits until the Docker engine answers and Compose is available.
func WaitForDocker(s Shell, timeout time.Duration, sleep func(time.Duration)) error {
	if _, ok := Found("docker"); !ok {
		return errors.New("Docker is not installed. Follow https://docs.docker.com/get-docker/.")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := Capture(s, "docker", "info")
		if err != nil {
			return err
		}
		if info.Code == 0 {
			version, err := Capture(s, "docker", "compose", "version")
			if err != nil {
				return err
			}
			if version.Code == 0 {
				return nil
			}
			return errors.New("Docker Compose is unavailable. Follow https://docs.docker.com/compose/install/.")
		}
		sleep(2 * time.Second)
	}
	return errors.New("Docker did not become ready within five minutes.")
}
