// workshop-desktop provides explicit host and guest desktop bridge entrypoints.
package main

import (
	"fmt"
	"io"
	"os"

	"workshop-desktop/internal/guestcmd"
	"workshop-desktop/internal/hostcmd"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	const usage = "Usage: workshop-desktop host {connect|serve|dbus|pipewire|open} [OPTIONS]\n       workshop-desktop guest open URL|FILE"
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return 0
	case "host":
		return hostcmd.Run(args[1:], stdout, stderr)
	case "guest":
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprintln(stdout, "Usage: workshop-desktop guest open URL|FILE")
			return 0
		}
		if len(args) >= 2 && args[1] == "open" {
			return guestcmd.Run(args[2:], stdout, stderr)
		}
	}
	fmt.Fprintln(stderr, usage)
	return 2
}
