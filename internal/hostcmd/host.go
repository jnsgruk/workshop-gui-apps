// Package hostcmd implements the host desktop bridge commands.
package hostcmd

import (
	"context"
	"flag"
	"fmt"
	"io"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

// Run selects a host operation before initializing project state.
func Run(args []string, stdout, stderr io.Writer) int {
	const usage = "Usage: workshop-desktop host {connect|serve|dbus|pipewire|open} [--project PATH]\n       workshop-desktop host connect [--workshop NAME]\n       workshop-desktop host serve [--only=dbus,pipewire,open]"
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, usage)
		return 0
	}
	var register func(*flag.FlagSet)
	var operation func(context.Context, host.Project) error
	capability := args[0]
	switch capability {
	case "connect":
		var name string
		register = func(flags *flag.FlagSet) {
			flags.StringVar(&name, "workshop", "firefox", "workshop name (must match its definition)")
		}
		operation = func(ctx context.Context, p host.Project) error { return connect(ctx, p, name) }
	case "serve":
		selected := handlers
		register = func(flags *flag.FlagSet) {
			flags.Func("only", "comma-separated handlers to run (dbus, pipewire, open; default: all)", func(value string) error {
				var err error
				selected, err = selectHandlers(value)
				return err
			})
		}
		operation = func(ctx context.Context, p host.Project) error { return runHandlers(ctx, p, selected) }
	case "dbus", "pipewire", "open":
		selected, _ := selectHandlers(capability)
		operation = func(ctx context.Context, p host.Project) error { return runHandlers(ctx, p, selected) }
	default:
		logging.New(stderr).Error("unknown host command", "command", capability)
		fmt.Fprintln(stderr, usage)
		return 2
	}
	return host.Run(capability, args[1:], stderr, register, operation)
}
