package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"goeverything/internal/config"
	"goeverything/internal/watcher"
)

func newWatchInstallCommand(opt *options) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install watch as a persistent user service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			exe, err = filepath.EvalSymlinks(exe)
			if err != nil {
				exe = filepath.Clean(exe)
			}
			root := opt.Root
			if strings.TrimSpace(root) == "" {
				root = "~"
			}
			root, err = config.ExpandPath(root)
			if err != nil {
				return err
			}
			servicePath, err := watcher.InstallPersistentWatch(exe, root, opt.DBPath)
			if err != nil {
				return err
			}
			fmt.Printf("installed %s\n", servicePath)
			return nil
		},
	}
}

func newWatchUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall persistent watch service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := watcher.UninstallPersistentWatch(); err != nil {
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
		Short: "Start persistent watch service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := watcher.StartPersistentWatch(); err != nil {
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
		Short: "Stop persistent watch service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := watcher.StopPersistentWatch(); err != nil {
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
		Short: "Restart persistent watch service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := watcher.RestartPersistentWatch(); err != nil {
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
		Short: "Print persistent watch service status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := watcher.PersistentWatchStatus()
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
		Short: "Show persistent watch log file paths (or tail logs with --follow)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			stdout, stderr, err := watcher.PersistentWatchLogPaths()
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
	command.Flags().BoolVar(&follow, "follow", false, "Tail persistent watch logs")
	return command
}
