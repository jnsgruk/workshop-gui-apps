package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/canonical/go-flags"

	"workshop-desktop/internal/guestcmd"
	"workshop-desktop/internal/hostcmd"
)

type commandAction func([]string) int

type cli struct {
	parser    *flags.Parser
	actions   map[*flags.Command]commandAction
	stdout    io.Writer
	stderr    io.Writer
	completed bool
}

type helpOptions struct {
	Help func() error `long:"help" description:"Show help"`
}

func newCLI(stdout, stderr io.Writer) *cli {
	showHelp := func() error { return &flags.Error{Type: flags.ErrHelp} }
	options := &helpOptions{Help: showHelp}
	parser := flags.NewNamedParser("workshop-desktop", flags.PassDoubleDash)
	parser.ShortDescription = "Connect a Workshop guest to host desktop services"
	parser.LongDescription = "Add --verbose after a host operation or guest launch to show detailed diagnostics."
	if _, err := parser.AddGroup("Help", "", options); err != nil {
		panic(err)
	}
	c := &cli{parser: parser, actions: make(map[*flags.Command]commandAction), stdout: stdout, stderr: stderr}
	parser.CompletionHandler = func(_ []flags.Completion) { c.completed = true }

	host := c.add(parser.Command, "host", "Manage desktop integration from the host", &struct{}{}, nil)
	c.addHostCommands(host)
	guest := c.add(parser.Command, "guest", "Launch applications in the guest", &struct{}{}, nil)
	c.addGuestCommands(guest)

	c.add(parser.Command, "help", "Show help for a command", &helpTarget{}, c.showHelp)
	c.add(parser.Command, "version", "Show the release version", &struct{}{}, func(args []string) int {
		if len(args) != 0 {
			return usageError(stderr, []string{"version"}, errors.New("unexpected positional arguments"))
		}
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	})
	return c
}

func (c *cli) add(parent *flags.Command, name, summary string, options any, action commandAction) *flags.Command {
	cmd, err := parent.AddCommand(name, summary, "", options)
	if err != nil {
		panic(err)
	}
	if action != nil {
		c.actions[cmd] = action
	}
	return cmd
}

type helpTarget struct{}

func (*helpTarget) Usage() string { return "[<group> [<command>]]" }

func (c *cli) showHelp(args []string) int {
	c.parser.Active = nil
	target := c.parser.Command
	for _, name := range args {
		child := target.Find(name)
		if child == nil {
			return usageError(c.stderr, []string{"help"}, fmt.Errorf("unknown command %q", name))
		}
		target.Active = child
		target = child
	}
	c.parser.WriteHelp(c.stdout)
	return 0
}

type hostOptions struct {
	Project string `long:"project" value-name:"PATH" default:"." description:"Workshop project directory"`
	verbosityOptions
}

type connectOptions struct {
	hostOptions
	Workshop string `long:"workshop" value-name:"NAME" default:"apps" description:"Workshop to connect"`
}

type serveOptions struct {
	hostOptions
	Handlers []string `long:"handlers" value-name:"NAMES" description:"Comma-separated handlers (dbus, pipewire). Defaults to both"`
}

type startOptions struct {
	serveOptions
	Workshops []string `long:"workshop" value-name:"NAME" description:"Workshop to prepare (repeatable). Defaults to apps"`
}

type launcherOptions struct {
	hostOptions
	All        bool `long:"all" description:"Select all SDK launchers"`
	Positional struct {
		SDK *string `positional-arg-name:"<sdk>"`
	} `positional-args:"yes"`
}

type hostLaunchOptions struct {
	hostOptions
	Notify bool `long:"notify" description:"Show bridge startup errors as desktop notifications"`
}

func (*hostLaunchOptions) Usage() string { return "[OPTIONS] -- <command> [<arg>...]" }

func (c *cli) addHostCommands(host *flags.Command) {
	connect := &connectOptions{}
	c.add(host, "connect", "Connect a workshop to the desktop bridge", connect, func(args []string) int {
		return c.runHost("connect", connect.hostOptions, hostcmd.Options{Workshop: connect.Workshop}, nil, args)
	})
	serve := &serveOptions{}
	c.add(host, "serve", "Run bridge helpers in the foreground", serve, func(args []string) int {
		handlers, err := serve.handlerSelection()
		return c.runHost("serve", serve.hostOptions, hostcmd.Options{Handlers: handlers}, err, args)
	})
	start := &startOptions{}
	c.add(host, "start", "Prepare workshops and run the bridge in the foreground", start, func(args []string) int {
		handlers, err := start.handlerSelection()
		return c.runHost("start", start.hostOptions, hostcmd.Options{Handlers: handlers, Workshops: start.Workshops}, err, args)
	})
	launch := &hostLaunchOptions{}
	c.add(host, "launch", "Start the shared desktop bridge and run a command", launch, func(args []string) int {
		path := []string{"host", "launch"}
		if len(args) == 0 || args[0] == "" {
			return usageError(c.stderr, path, errors.New("missing command after --"))
		}
		verbosity, err := launch.get()
		if err != nil {
			return usageError(c.stderr, path, err)
		}
		code, err := hostcmd.Launch(launch.Project, args, launch.Notify, c.stdout, c.stderr, verbosity)
		return finish(c.stderr, code, err)
	})
	stop := &hostOptions{}
	c.add(host, "stop", "Stop the shared desktop bridge", stop, func(args []string) int {
		return c.runHost("stop", *stop, hostcmd.Options{}, nil, args)
	})
	supervisor := &struct {
		hostOptions
		WorkshopExecutable string `long:"workshop-executable" value-name:"PATH" description:"Workshop executable for bridge repair"`
	}{}
	c.add(host, "supervise", "Run the shared desktop bridge", supervisor, func(args []string) int {
		return c.runHost("supervise", supervisor.hostOptions, hostcmd.Options{WorkshopExecutable: supervisor.WorkshopExecutable}, nil, args)
	}).Hidden = true

	for _, launcher := range []struct{ name, summary string }{
		{"install-launcher", "Install desktop launchers for workshop SDKs"},
		{"remove-launcher", "Remove desktop launchers for workshop SDKs"},
	} {
		opts := &launcherOptions{}
		c.add(host, launcher.name, launcher.summary, opts, func(args []string) int {
			var sdk string
			if opts.Positional.SDK != nil {
				sdk = *opts.Positional.SDK
				if sdk == "" {
					return usageError(c.stderr, []string{"host", launcher.name}, errors.New("empty SDK name"))
				}
			}
			return c.runHost(launcher.name, opts.hostOptions, hostcmd.Options{SDK: sdk, All: opts.All}, nil, args)
		})
	}
}

func (o *serveOptions) handlerSelection() (string, error) {
	switch {
	case len(o.Handlers) > 1:
		return "", errors.New("specify the handler selection only once using --handlers")
	case len(o.Handlers) == 1:
		if o.Handlers[0] == "" {
			return "", errors.New("missing handler names for --handlers")
		}
		return o.Handlers[0], nil
	default:
		return "", nil
	}
}

func (c *cli) runHost(command string, opts hostOptions, operation hostcmd.Options, err error, args []string) int {
	path := []string{"host", command}
	if err != nil {
		return usageError(c.stderr, path, err)
	}
	if len(args) != 0 {
		return usageError(c.stderr, path, fmt.Errorf("unexpected positional arguments: %q", args))
	}
	operation.Project = opts.Project
	operation.Verbosity, err = opts.get()
	if err != nil {
		return usageError(c.stderr, path, err)
	}
	if err := hostcmd.Validate(command, operation); err != nil {
		return usageError(c.stderr, path, err)
	}
	if err := hostcmd.Execute(command, operation, c.stdout, c.stderr); err != nil {
		return finish(c.stderr, 1, err)
	}
	return 0
}

type launchOptions struct {
	SessionBus string `long:"session-bus" value-name:"host|local" description:"Session bus for the launched command"`
	verbosityOptions
}

func (*launchOptions) Usage() string {
	return "[OPTIONS] --session-bus <host|local> -- <command> [<arg>...]"
}

func (c *cli) addGuestCommands(guest *flags.Command) {
	launch := &launchOptions{}
	c.add(guest, "launch", "Launch an application with desktop integration", launch, func(args []string) int {
		path := []string{"guest", "launch"}
		switch launch.SessionBus {
		case "host", "local":
		case "":
			return usageError(c.stderr, path, errors.New("missing --session-bus (expected host or local)"))
		default:
			return usageError(c.stderr, path, fmt.Errorf("invalid session bus %q (expected host or local)", launch.SessionBus))
		}
		if len(args) == 0 || args[0] == "" {
			return usageError(c.stderr, path, errors.New("missing command after --"))
		}
		verbosity, err := launch.get()
		if err != nil {
			return usageError(c.stderr, path, err)
		}
		code, err := guestcmd.Launch(args, launch.SessionBus, c.stdout, c.stderr, verbosity)
		return finish(c.stderr, code, err)
	})
}
