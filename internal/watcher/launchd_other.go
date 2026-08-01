//go:build !darwin && !linux

package watcher

import "errors"

func InstallPersistentWatch(_, _, _ string) (string, error) {
	return "", errors.New("persistent watch service is not supported on Windows; use foreground watch")
}

func UninstallPersistentWatch() error {
	return errors.New("persistent watch service is not supported on Windows; use foreground watch")
}
func StartPersistentWatch() error {
	return errors.New("persistent watch service is not supported on Windows; use foreground watch")
}
func StopPersistentWatch() error {
	return errors.New("persistent watch service is not supported on Windows; use foreground watch")
}
func RestartPersistentWatch() error {
	return errors.New("persistent watch service is not supported on Windows; use foreground watch")
}
func PersistentWatchStatus() (string, error) {
	return "", errors.New("persistent watch service is not supported on Windows; use foreground watch")
}

func PersistentWatchLogPaths() (string, string, error) {
	return "", "", errors.New("persistent watch service is not supported on Windows; use foreground watch")
}
