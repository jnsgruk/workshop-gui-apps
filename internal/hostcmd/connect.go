// connect changes Workshop mounts and interfaces; it does not serve endpoints.
package hostcmd

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

const workshopWaitDelay = 3 * time.Second

func connect(ctx context.Context, p host.Project, name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "/ \t\n") {
		return fmt.Errorf("invalid workshop name")
	}

	for _, capability := range []string{"dbus", "pipewire"} {
		lock, err := p.Lock(capability)
		if err != nil {
			return err
		}

		defer func() { _ = lock.Close() }()
	}

	if _, err := exec.LookPath("workshop"); err != nil {
		return err
	}

	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "workshop", args...)
		cmd.WaitDelay = workshopWaitDelay
		cmd.Dir = p.Directory
		logger := logging.Logger(ctx).With("process", "workshop", "operation", args[0])
		stdout := &logging.Writer{Context: ctx, Logger: logger.With("stream", "stdout"), Level: slog.LevelInfo}
		stderr := &logging.Writer{Context: ctx, Logger: logger.With("stream", "stderr"), Level: slog.LevelWarn}
		defer stdout.Flush()
		defer stderr.Flush()
		cmd.Stdout, cmd.Stderr = stdout, stderr
		return cmd.Run()
	}

	if err := run("stop", name); err != nil {
		return err
	}

	mounts := []struct{ plug, directory string }{
		{"host-session", "session"},
		{"host-pipewire", "pipewire"},
	}

	for _, mount := range mounts {
		if err := run("remount", name+"/host-integration:"+mount.plug, filepath.Join(p.Root, mount.directory)); err != nil {
			return fmt.Errorf("remount failed; workshop remains stopped: %w", err)
		}
	}

	if err := run("start", name); err != nil {
		return err
	}

	interfaces := []struct{ sdk, capability string }{
		{"host-integration", "desktop"},
		{"firefox", "camera"},
	}
	for _, iface := range interfaces {
		if err := connectInterface(ctx, p.Directory, name, iface.sdk, iface.capability); err != nil {
			return err
		}
	}

	logging.Logger(ctx).InfoContext(ctx, "Connected. Start workshop-desktop host serve.")
	return nil
}

func connectInterface(ctx context.Context, directory, name, sdk, capability string) error {
	plug := name + "/" + sdk + ":" + capability
	slot := name + "/system:" + capability
	cmd := exec.CommandContext(ctx, "workshop", "connect", plug)
	cmd.WaitDelay = workshopWaitDelay
	cmd.Dir = directory

	output, err := cmd.CombinedOutput()
	// Workshop reports an existing connection as an error. Only ignore that exact error.
	alreadyConnected := fmt.Sprintf("error: cannot perform the following tasks:\n- Connect %q to %q (%s interface already connected)", plug, slot, capability)
	if err != nil && strings.TrimSpace(string(output)) == alreadyConnected {
		return nil
	}

	if message := strings.TrimSpace(string(output)); message != "" {
		logging.Logger(ctx).InfoContext(ctx, message, "process", "workshop", "operation", "connect", "capability", capability)
	}
	return err
}
