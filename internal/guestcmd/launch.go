package guestcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"

	"workshop-desktop/internal/logging"
)

const (
	guestRuntime   = "/run/user/1000"
	hostBusSocket  = "/mnt/workshop-host/session/bus"
	pipeWireSocket = "/mnt/workshop-host/pipewire/pipewire-0"
)

// Launch runs a command with the selected session bus and host audio.
func Launch(command []string, sessionBus string, stdout, stderr io.Writer, verbosity logging.Verbosity) (int, error) {
	return launch(command, sessionBus, stdout, stderr, os.Getuid(), guestRuntime, hostBusSocket, pipeWireSocket, verbosity)
}

func launch(command []string, sessionBus string, stdout, stderr io.Writer, uid int, runtime, hostBusSocket, pipeWireSocket string, verbosity logging.Verbosity) (int, error) {
	if uid != 1000 {
		return 1, errors.New("run the launcher as the workshop user (UID 1000)")
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return 1, errors.New("a Wayland display is required. Run workshop-desktop host connect from your host graphical session")
	}

	ctx := logging.WithCLIOutput(context.Background(), "guest launch", verbosity, stdout, stderr)
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	probe := exec.CommandContext(probeCtx, "wayland-info")
	probe.WaitDelay = time.Second
	probe.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtime)
	if err := runSetup(probeCtx, probe, verbosity); err != nil {
		return 1, fmt.Errorf("cannot connect to the Wayland display: %w. Reconnect the workshop from your host graphical session", err)
	}

	socket := runtime + "/bus"
	if sessionBus == "host" {
		socket = hostBusSocket
	}
	if !isSocket(socket) {
		return 1, fmt.Errorf("required session D-Bus socket is missing at %s. Check the guest user session and keep workshop-desktop host serve running", socket)
	}
	if !isSocket(pipeWireSocket) {
		return 1, errors.New("host PipeWire socket is missing. Keep workshop-desktop host serve running")
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtime,
		"DBUS_SESSION_BUS_ADDRESS=unix:path="+socket, "GDK_BACKEND=wayland")
	cmd.Env = slices.DeleteFunc(cmd.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "DBUS_SYSTEM_BUS_ADDRESS=")
	})
	return startAudioAndExec(ctx, cmd, runtime, verbosity)
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func startAudioAndExec(ctx context.Context, cmd *exec.Cmd, runtime string, verbosity logging.Verbosity) (int, error) {
	service := exec.CommandContext(ctx, "systemctl", "--user", "start", "workshop-pipewire-pulse.service")
	service.WaitDelay = time.Second
	service.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtime, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtime+"/bus")
	service.Stdin = os.Stdin
	if err := runSetup(ctx, service, verbosity); err != nil {
		code := 1
		if status, ok := errors.AsType[*exec.ExitError](err); ok && status.ExitCode() > 0 {
			code = status.ExitCode()
		}
		return code, fmt.Errorf("cannot start the host audio service: %w", err)
	}

	if cmd.Err != nil {
		return 127, fmt.Errorf("cannot find command %q: %w", cmd.Args[0], cmd.Err)
	}
	cmd.Env = append(cmd.Environ(), "PULSE_SERVER=unix:"+runtime+"/pulse/native")
	if err := syscall.Exec(cmd.Path, cmd.Args, cmd.Environ()); err != nil {
		return 126, fmt.Errorf("cannot run command %q: %w", cmd.Args[0], err)
	}
	return 0, nil
}

// runSetup logs helper output in verbose mode and retains stderr on failure.
func runSetup(ctx context.Context, cmd *exec.Cmd, verbosity logging.Verbosity) error {
	var detail logging.ErrorDetail
	cmd.Stderr = &detail
	if verbosity == logging.Verbose {
		logger := logging.Logger(ctx).With("helper", cmd.Args[0])
		logger.InfoContext(ctx, "Run desktop setup helper", "args", cmd.Args[1:])
		stdout := &logging.Writer{Context: ctx, Logger: logger.With("stream", "stdout"), Level: slog.LevelInfo}
		stderr := &logging.Writer{Context: ctx, Logger: logger.With("stream", "stderr"), Level: slog.LevelInfo, Detail: &detail}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		defer stdout.Flush()
		defer stderr.Flush()
	}
	return detail.Wrap(cmd.Run())
}
