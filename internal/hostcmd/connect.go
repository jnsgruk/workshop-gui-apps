// connect changes Workshop mounts and interfaces; it does not serve endpoints.
package hostcmd

import (
	"bytes"
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

type bridgeMount struct{ plug, directory string }

var bridgeMounts = []bridgeMount{
	{"host-session", "session"},
	{"host-pipewire", "pipewire"},
}

type bridgeInterface struct{ sdk, capability string }

var bridgeInterfaces = []bridgeInterface{
	{"host-integration", "desktop"},
	{"firefox", "camera"},
}

func validWorkshopName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.HasPrefix(name, "-") && !strings.ContainsAny(name, "/ \t\n")
}

func connect(ctx context.Context, p host.Project, name string) error {
	if !validWorkshopName(name) {
		return fmt.Errorf("invalid workshop name")
	}

	unlock, err := lockHandlers(p, handlers)
	if err != nil {
		return err
	}
	defer unlock()

	if err := workshopCommand(ctx, p, "stop", name); err != nil {
		return err
	}
	if err := remount(ctx, p, name, bridgeMounts); err != nil {
		return err
	}
	if err := workshopCommand(ctx, p, "start", name); err != nil {
		return err
	}
	for _, iface := range bridgeInterfaces {
		if err := connectInterface(ctx, p.Directory, name, iface.sdk, iface.capability); err != nil {
			return err
		}
	}

	logging.Result(ctx, "Connected. Run workshop-desktop host serve.")
	return nil
}

func workshopCommand(ctx context.Context, p host.Project, args ...string) error {
	cmd := exec.CommandContext(ctx, "workshop", args...)
	cmd.WaitDelay = workshopWaitDelay
	cmd.Dir = p.Directory
	logger := logging.Logger(ctx).With("process", "workshop", "operation", args[0])
	stdout := &logging.Writer{Context: ctx, Logger: logger.With("stream", "stdout"), Level: slog.LevelInfo}
	detail := &logging.ErrorDetail{}
	stderr := &logging.Writer{Context: ctx, Logger: logger.With("stream", "stderr"), Level: slog.LevelInfo, Detail: detail}
	defer stdout.Flush()
	defer stderr.Flush()
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return detail.Wrap(fmt.Errorf("workshop %s: %w", strings.Join(args, " "), err))
	}
	return nil
}

func remount(ctx context.Context, p host.Project, name string, mounts []bridgeMount) error {
	for _, mount := range mounts {
		if err := workshopCommand(ctx, p, "remount", name+"/host-integration:"+mount.plug, filepath.Join(p.Root, mount.directory)); err != nil {
			return fmt.Errorf("cannot remount the workshop, which remains stopped: %w", err)
		}
	}
	return nil
}

func connectInterface(ctx context.Context, directory, name, sdk, capability string) error {
	plug := name + "/" + sdk + ":" + capability
	slot := name + "/system:" + capability
	cmd := exec.CommandContext(ctx, "workshop", "connect", plug)
	cmd.WaitDelay = workshopWaitDelay
	cmd.Dir = directory

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Workshop reports an existing connection as an error. Only ignore that exact error.
	alreadyConnected := fmt.Sprintf("error: cannot perform the following tasks:\n- Connect %q to %q (%s interface already connected)", plug, slot, capability)
	outText, errText := strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String())
	if err != nil && (outText == "" && errText == alreadyConnected || errText == "" && outText == alreadyConnected) {
		return nil
	}

	logger := logging.Logger(ctx).With("process", "workshop", "operation", "connect", "capability", capability)
	detail := &logging.ErrorDetail{}
	stdoutLog := &logging.Writer{Context: ctx, Logger: logger.With("stream", "stdout"), Level: slog.LevelInfo}
	stderrLog := &logging.Writer{Context: ctx, Logger: logger.With("stream", "stderr"), Level: slog.LevelInfo, Detail: detail}
	if errText == "" {
		stdoutLog.Detail = detail
	}
	_, _ = stdout.WriteTo(stdoutLog)
	_, _ = stderr.WriteTo(stderrLog)
	stdoutLog.Flush()
	stderrLog.Flush()
	if err != nil {
		return detail.Wrap(fmt.Errorf("workshop connect %s: %w", plug, err))
	}
	return nil
}
