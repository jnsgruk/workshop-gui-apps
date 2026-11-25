// Package hostcmd implements the host desktop bridge commands.
package hostcmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

// Run selects a host operation before initializing project state.
func Run(args []string, stdout, stderr io.Writer) int {
	const usage = "Usage: workshop-desktop host {up|connect|serve|dbus|pipewire|launchers} [--project PATH]\n       workshop-desktop host connect [--workshop NAME]\n       workshop-desktop host serve [--only=dbus,pipewire]\n       workshop-desktop host up [--workshop NAME]... [--only=dbus,pipewire]\n       workshop-desktop host launchers {install|remove} {SDK|--all} [--project PATH]"
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprintln(stdout, usage)
		return 0
	}
	var register func(*flag.FlagSet)
	var operation func(context.Context, host.Project) error
	capability := args[0]
	switch capability {
	case "launchers":
		const launcherUsage = "Usage: workshop-desktop host launchers {install|remove} {SDK|--all} [--project PATH]"
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			_, _ = fmt.Fprintln(stdout, launcherUsage)
			return 0
		}
		if len(args) == 3 && (args[1] == "install" || args[1] == "remove") &&
			(args[2] == "--help" || args[2] == "-h") {
			_, _ = fmt.Fprintln(stdout, launcherUsage)
			return 0
		}
		if len(args) < 3 || (args[1] != "install" && args[1] != "remove") ||
			(args[2] != "--all" && !launcherName.MatchString(args[2])) {
			_, _ = fmt.Fprintln(stderr, launcherUsage)
			return 2
		}
		install := args[1] == "install"
		operation := func(ctx context.Context, p host.Project) error {
			if args[2] == "--all" {
				return manageAllLaunchers(ctx, p, install)
			}
			return manageLauncher(ctx, p, args[2], install)
		}
		command := "launchers " + args[1] + " " + args[2]
		if !install {
			return host.RunForDirectory(command, args[3:], stderr, operation)
		}
		return host.Run(command, args[3:], stderr, nil, operation)
	case "connect":
		var name string
		register = func(flags *flag.FlagSet) {
			flags.StringVar(&name, "workshop", "apps", "workshop name (must match its definition)")
		}
		operation = func(ctx context.Context, p host.Project) error { return connect(ctx, p, name) }
	case "serve", "up":
		selected := handlers
		var names []string
		register = func(flags *flag.FlagSet) {
			if capability == "up" {
				flags.Func("workshop", "desktop-enabled workshop to prepare (repeatable; default: apps)", func(name string) error {
					if !validWorkshopName(name) || name == "." || name == ".." {
						return fmt.Errorf("invalid workshop name %q", name)
					}
					if !slices.Contains(names, name) {
						names = append(names, name)
					}
					return nil
				})
			}
			flags.Func("only", "comma-separated handlers to run (dbus, pipewire; default: all)", func(value string) error {
				var err error
				selected, err = selectHandlers(value)
				return err
			})
		}
		operation = func(ctx context.Context, p host.Project) error {
			if capability == "serve" {
				return runHandlers(ctx, p, selected)
			}
			if len(names) == 0 {
				names = []string{"apps"}
			}
			return up(ctx, p, names, selected)
		}
	case "dbus", "pipewire":
		selected, _ := selectHandlers(capability)
		operation = func(ctx context.Context, p host.Project) error { return runHandlers(ctx, p, selected) }
	default:
		logging.New(stderr).Error("unknown host command", "command", capability)
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	return host.Run(capability, args[1:], stderr, register, operation)
}
