// Package guestcmd implements the guest xdg-open compatibility command.
package guestcmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"workshop-desktop/internal/logging"
	"workshop-desktop/internal/opener"
)

// Run opens a target through the host bridge without initializing host state.
func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, func(target string) error {
		return opener.Open(context.Background(), target)
	})
}

func run(args []string, stdout, stderr io.Writer, open func(string) error) int {
	const usage = "Usage: xdg-open URL|FILE\nOpens on the host; files must be shared under /project."

	if len(args) == 1 {
		switch args[0] {
		case "--help", "-h":
			fmt.Fprintln(stdout, usage)
			return 0
		case "--version":
			fmt.Fprintln(stdout, "xdg-open workshop-host-bridge 1")
			return 0
		}
	}

	if len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, usage)
		return 1
	}

	if err := open(args[0]); err != nil {
		logging.New(stderr).Error("Keep workshop-desktop host open or host serve running on the host", "error", err)
		return 4
	}

	return 0
}
