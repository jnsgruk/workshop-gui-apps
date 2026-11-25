// Package hostcmd implements the host desktop bridge commands.
package hostcmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

// Options contains the parsed arguments for a host command.
type Options struct {
	Project            string
	WorkshopExecutable string
	Workshop           string
	Workshops          []string
	Handlers           string
	SDK                string
	All                bool
	Verbosity          logging.Verbosity
}

// Validate checks command arguments without loading the project.
func Validate(command string, opts Options) error {
	switch command {
	case "connect":
		if !validWorkshopName(opts.Workshop) {
			return fmt.Errorf("invalid workshop name %q", opts.Workshop)
		}
	case "start", "serve":
		if opts.Handlers != "" {
			if _, err := selectHandlers(opts.Handlers); err != nil {
				return err
			}
		}
		for _, name := range opts.Workshops {
			if !validWorkshopName(name) {
				return fmt.Errorf("invalid workshop name %q", name)
			}
		}
	case "stop":
	case "supervise":
		if opts.WorkshopExecutable != "" && !filepath.IsAbs(opts.WorkshopExecutable) {
			return fmt.Errorf("Workshop executable must be an absolute path")
		}
	case "install-launcher", "remove-launcher":
		if opts.All && opts.SDK != "" {
			return fmt.Errorf("cannot specify both an SDK and --all")
		}
		if !opts.All && opts.SDK == "" {
			return fmt.Errorf("missing SDK name or --all")
		}
		if opts.SDK != "" && !launcherName.MatchString(opts.SDK) {
			return fmt.Errorf("invalid SDK name %q", opts.SDK)
		}
	default:
		return fmt.Errorf("unknown host command %q", command)
	}
	return nil
}

// Execute runs a validated host command with command-scoped output and signal handling.
func Execute(command string, opts Options, stdout, stderr io.Writer) error {
	selected := handlers
	if opts.Handlers != "" {
		var err error
		selected, err = selectHandlers(opts.Handlers)
		if err != nil {
			return err
		}
	}

	operation := func(ctx context.Context, p host.Project) error {
		switch command {
		case "stop":
			return stopSupervisor(ctx, p)
		case "supervise":
			return supervise(ctx, p, opts.WorkshopExecutable)
		case "connect":
			return connect(ctx, p, opts.Workshop)
		case "serve":
			return runHandlers(ctx, p, selected)
		case "start":
			var names []string
			for _, name := range opts.Workshops {
				if !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
			if len(names) == 0 {
				names = []string{"apps"}
			}
			return up(ctx, p, names, selected)
		case "install-launcher", "remove-launcher":
			install := command == "install-launcher"
			if opts.All {
				return manageAllLaunchers(ctx, p, install)
			}
			return manageLauncher(ctx, p, opts.SDK, install)
		default:
			return fmt.Errorf("unknown host command %q", command)
		}
	}

	ctx := logging.WithCLIOutput(context.Background(), "host "+command, opts.Verbosity, stdout, stderr)
	if err := host.Run(ctx, opts.Project, command == "remove-launcher", operation); err != nil {
		return fmt.Errorf("cannot run host %s: %w", command, err)
	}
	return nil
}
