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
	const guestUsage = guestcmd.LaunchUsage
	const usage = "Usage: workshop-desktop host {connect|serve|dbus|pipewire} [OPTIONS]\n       workshop-desktop guest launch [OPTIONS]"
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
			fmt.Fprintln(stdout, guestUsage)
			return 0
		}
		if len(args) >= 2 {
			switch args[1] {
			case "launch":
				return guestcmd.Launch(args[2:], stdout, stderr)
			case "audio":
				return guestcmd.Audio(args[2:], stdout, stderr)
			}
		}
	}
	fmt.Fprintln(stderr, usage)
	return 2
}
