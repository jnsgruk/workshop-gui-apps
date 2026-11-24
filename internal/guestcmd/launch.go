package guestcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	guestRuntime   = "/run/user/1000"
	hostBusSocket  = "/mnt/workshop-host/session/bus"
	pipeWireSocket = "/mnt/workshop-host/pipewire/pipewire-0"
)

const LaunchUsage = "Usage: workshop-desktop guest launch --session-bus=host|local COMMAND [ARG...]"

func Launch(args []string, stdout, stderr io.Writer) int {
	return launch(args, stdout, stderr, os.Getuid(), guestRuntime, hostBusSocket, pipeWireSocket)
}

func launch(args []string, stdout, stderr io.Writer, uid int, runtime, hostBusSocket, pipeWireSocket string) int {
	flags := flag.NewFlagSet("launch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = fmt.Fprintln(flags.Output(), LaunchUsage) }
	bus := flags.String("session-bus", "", "session bus for the launched command")
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, _ = fmt.Fprintln(stdout, LaunchUsage)
		return 0
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() == 0 || (*bus != "host" && *bus != "local") {
		flags.Usage()
		return 2
	}
	if uid != 1000 {
		_, _ = fmt.Fprintln(stderr, "Run the launcher as the workshop user (UID 1000).")
		return 1
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		_, _ = fmt.Fprintln(stderr, "A Wayland display is required. Run workshop-desktop host connect from your host graphical session.")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, "wayland-info")
	probe.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtime)
	probe.Stderr = stderr
	if err := probe.Run(); err != nil {
		_, _ = fmt.Fprintf(stderr, "Cannot connect to the Wayland display: %v. Reconnect the workshop from your host graphical session.\n", err)
		return 1
	}

	socket := runtime + "/bus"
	if *bus == "host" {
		socket = hostBusSocket
	}
	if !isSocket(socket) {
		_, _ = fmt.Fprintf(stderr, "Required session D-Bus socket is missing at %s. Check the guest user session and keep workshop-desktop host serve running.\n", socket)
		return 1
	}
	if !isSocket(pipeWireSocket) {
		_, _ = fmt.Fprintln(stderr, "Host PipeWire socket is missing. Keep workshop-desktop host serve running.")
		return 1
	}

	cmd := exec.Command(flags.Arg(0), flags.Args()[1:]...)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtime,
		"DBUS_SESSION_BUS_ADDRESS=unix:path="+socket, "GDK_BACKEND=wayland")
	cmd.Env = slices.DeleteFunc(cmd.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "DBUS_SYSTEM_BUS_ADDRESS=")
	})
	return startAudioAndExec(cmd, stdout, stderr, runtime)
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// Audio preserves the caller's environment for legacy workshop-host-audio callers.
func Audio(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "Usage: workshop-host-audio COMMAND [ARG...]")
		return 2
	}
	return startAudioAndExec(exec.Command(args[0], args[1:]...), stdout, stderr, guestRuntime)
}

func startAudioAndExec(cmd *exec.Cmd, stdout, stderr io.Writer, runtime string) int {
	service := exec.Command("systemctl", "--user", "start", "workshop-pipewire-pulse.service")
	service.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtime, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtime+"/bus")
	service.Stdin, service.Stdout, service.Stderr = os.Stdin, stdout, stderr
	if err := service.Run(); err != nil {
		var status *exec.ExitError
		if errors.As(err, &status) && status.ExitCode() > 0 {
			return status.ExitCode()
		}
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	if cmd.Err != nil {
		_, _ = fmt.Fprintln(stderr, cmd.Err)
		return 127
	}
	cmd.Env = append(cmd.Environ(), "PULSE_SERVER=unix:"+runtime+"/pulse/native")
	if err := syscall.Exec(cmd.Path, cmd.Args, cmd.Environ()); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 126
	}
	return 0
}
