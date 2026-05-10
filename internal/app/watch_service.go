package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"goeverything/internal/watcher"
)

func newWatchInstallCommand(opt *options) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install watch as a launchd user service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			exe, err = filepath.EvalSymlinks(exe)
			if err != nil {
				exe = filepath.Clean(exe)
			}
			plistPath, err := watcher.InstallLaunchAgent(exe, opt.Root, opt.DBPath)
			if err != nil {
				return err
			}
			fmt.Printf("installed %s\n", plistPath)
			return nil
		},
	}
}

func newWatchUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall launchd watch service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := watcher.UninstallLaunchAgent(); err != nil {
				return err
			}
			fmt.Println("watch service uninstalled")
			return nil
		},
	}
}

func newWatchStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start launchd watch service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := watcher.StartLaunchAgent(); err != nil {
				return err
			}
			fmt.Println("watch service started")
			return nil
		},
	}
}

func newWatchStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop launchd watch service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := watcher.StopLaunchAgent(); err != nil {
				return err
			}
			fmt.Println("watch service stopped")
			return nil
		},
	}
}

func newWatchRestartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart launchd watch service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := watcher.RestartLaunchAgent(); err != nil {
				return err
			}
			fmt.Println("watch service restarted")
			return nil
		},
	}
}

func newWatchStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print launchd watch service status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := watcher.LaunchAgentStatus()
			if status != "" {
				fmt.Println(status)
			}
			return err
		},
	}
}

func newWatchLogsCommand() *cobra.Command {
	follow := false
	command := &cobra.Command{
		Use:   "logs",
		Short: "Show launchd log file paths (or tail logs with --follow)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			stdout, stderr, err := watcher.LaunchAgentLogPaths()
			if err != nil {
				return err
			}
			if !follow {
				fmt.Printf("stdout: %s\nstderr: %s\n", stdout, stderr)
				return nil
			}
			tail := exec.Command("tail", "-f", stdout, stderr)
			tail.Stdout = os.Stdout
			tail.Stderr = os.Stderr
			tail.Stdin = os.Stdin
			return tail.Run()
		},
	}
	command.Flags().BoolVar(&follow, "follow", false, "Tail launchd watch logs")
	return command
}
