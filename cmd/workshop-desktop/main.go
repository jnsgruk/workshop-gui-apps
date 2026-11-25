// workshop-desktop provides explicit host and guest desktop bridge entrypoints.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/canonical/go-flags"

	"workshop-desktop/internal/logging"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	args, childArgs := splitGuestCommand(args)
	cli := newCLI(stdout, stderr)
	remaining, err := cli.parser.ParseArgs(args)
	active := cli.parser.Command
	var path []string
	for active.Active != nil {
		active = active.Active
		path = append(path, active.Name)
	}
	if cli.completed {
		return 0
	}
	var parseError *flags.Error
	if errors.As(err, &parseError) && parseError.Type == flags.ErrHelp {
		cli.parser.WriteHelp(stdout)
		return 0
	}
	if err != nil {
		return usageError(stderr, path, err)
	}
	if slices.Equal(path, []string{"guest", "launch"}) {
		if len(remaining) != 0 {
			return usageError(stderr, path, errors.New("place the command and its arguments after --"))
		}
		remaining = childArgs
	}
	if action := cli.actions[active]; action != nil {
		return action(remaining)
	}
	return usageError(stderr, path, errors.New("command required"))
}

func usageError(stderr io.Writer, path []string, err error) int {
	_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	hint := strings.TrimSpace("workshop-desktop help " + strings.Join(path, " "))
	_, _ = fmt.Fprintf(stderr, "Run %q for usage.\n", hint)
	return 2
}

func finish(stderr io.Writer, code int, err error) int {
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	}
	return code
}

type verbosityOptions struct {
	Quiet   bool `long:"quiet" description:"Show errors only"`
	Brief   bool `long:"brief" description:"Show essential feedback (default)"`
	Verbose bool `long:"verbose" description:"Show detailed diagnostics"`
}

func (v verbosityOptions) get() (logging.Verbosity, error) {
	if v.Quiet && (v.Brief || v.Verbose) || v.Brief && v.Verbose {
		return logging.Brief, errors.New("use only one of --quiet, --brief, and --verbose")
	}
	switch {
	case v.Quiet:
		return logging.Quiet, nil
	case v.Verbose:
		return logging.Verbose, nil
	default:
		return logging.Brief, nil
	}
}

// splitGuestCommand keeps the child arguments out of the CLI parser entirely.
func splitGuestCommand(args []string) (options, command []string) {
	if len(args) >= 3 && args[0] == "guest" && args[1] == "launch" {
		if separator := slices.Index(args[2:], "--"); separator >= 0 {
			separator += 2
			return args[:separator], args[separator+1:]
		}
	}
	return args, nil
}
