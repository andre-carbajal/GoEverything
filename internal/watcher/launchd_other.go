//go:build !darwin

package watcher

import "errors"

func InstallLaunchAgent(_, _, _ string) (string, error) {
	return "", errors.New("persistent watch service commands are only supported on macOS")
}

func UninstallLaunchAgent() error {
	return errors.New("persistent watch service commands are only supported on macOS")
}
func StartLaunchAgent() error {
	return errors.New("persistent watch service commands are only supported on macOS")
}
func StopLaunchAgent() error {
	return errors.New("persistent watch service commands are only supported on macOS")
}
func RestartLaunchAgent() error {
	return errors.New("persistent watch service commands are only supported on macOS")
}
func LaunchAgentStatus() (string, error) {
	return "", errors.New("persistent watch service commands are only supported on macOS")
}

func LaunchAgentLogPaths() (string, string, error) {
	return "", "", errors.New("persistent watch service commands are only supported on macOS")
}
