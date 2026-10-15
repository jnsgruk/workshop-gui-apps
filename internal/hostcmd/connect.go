// connect changes Workshop mounts and interfaces; it does not serve endpoints.
package hostcmd

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

func connect(ctx context.Context, p host.Project, name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "/ \t\n") {
		return fmt.Errorf("invalid workshop name")
	}

	for _, capability := range []string{"dbus", "pipewire", "open"} {
		lock, err := p.Lock(capability)
		if err != nil {
			return err
		}

		defer lock.Close()
	}

	if _, err := exec.LookPath("workshop"); err != nil {
		return err
	}

	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "workshop", args...)
		cmd.Dir = p.Directory
		logger := logging.Logger(ctx).With("process", "workshop", "operation", args[0])
		stdout := &logging.Writer{Logger: logger.With("stream", "stdout"), Level: slog.LevelInfo}
		stderr := &logging.Writer{Logger: logger.With("stream", "stderr"), Level: slog.LevelWarn}
		defer stdout.Flush()
		defer stderr.Flush()
		cmd.Stdout, cmd.Stderr = stdout, stderr
		return cmd.Run()
	}

	if err := run("stop", name); err != nil {
		return err
	}

	mounts := []struct{ sdk, plug, directory string }{
		{"host-dbus", "host-session", "session"},
		{"host-audio", "host-pipewire", "pipewire"},
		{"host-open", "host-open", "open"},
	}

	for _, mount := range mounts {
		if err := run("remount", name+"/"+mount.sdk+":"+mount.plug, filepath.Join(p.Root, mount.directory)); err != nil {
			return fmt.Errorf("remount failed; workshop remains stopped: %w", err)
		}
	}

	if err := run("start", name); err != nil {
		return err
	}

	for _, capability := range []string{"desktop", "camera"} {
		if err := connectInterface(ctx, p.Directory, name, capability); err != nil {
			return err
		}
	}

	logging.Logger(ctx).Info("Connected. Start workshop-desktop host serve.")
	return nil
}

func connectInterface(ctx context.Context, directory, name, capability string) error {
	plug := name + "/firefox-desktop:" + capability
	slot := name + "/system:" + capability
	cmd := exec.CommandContext(ctx, "workshop", "connect", plug)
	cmd.Dir = directory

	output, err := cmd.CombinedOutput()
	// Workshop reports an existing connection as an error. Only ignore that exact error.
	alreadyConnected := fmt.Sprintf("error: cannot perform the following tasks:\n- Connect %q to %q (%s interface already connected)", plug, slot, capability)
	if err != nil && strings.TrimSpace(string(output)) == alreadyConnected {
		return nil
	}

	if message := strings.TrimSpace(string(output)); message != "" {
		logging.Logger(ctx).Info(message, "process", "workshop", "operation", "connect", "capability", capability)
	}
	return err
}
