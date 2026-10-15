// dbus publishes a filtered host session bus. The guest keeps its local buses.
package hostcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

func runDBus(ctx context.Context, p host.Project) error {
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" {
		return fmt.Errorf("DBUS_SESSION_BUS_ADDRESS is unset; run from your graphical session")
	}

	socket := filepath.Join(p.Root, "session", "bus")
	if err := host.ClearSocket(socket); err != nil {
		return err
	}

	defer os.Remove(socket)

	process, err := host.Start(ctx, nil, "xdg-dbus-proxy", address, socket, "--filter",
		"--talk=org.freedesktop.Notifications",
		"--talk=org.freedesktop.portal.Desktop",
		"--talk=org.freedesktop.portal.Documents")
	if err != nil {
		return err
	}

	defer process.Stop()

	if err := process.WaitSocket(ctx, socket); err != nil {
		return err
	}

	logging.Logger(ctx).Info("Session D-Bus ready", "socket", socket)
	return process.Wait(ctx)
}
