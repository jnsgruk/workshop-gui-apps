package hostcmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/godbus/dbus/v5"
)

const sessionManager = "org.gnome.SessionManager"

type launchSession struct {
	ID      string
	Bus     string
	Display string
}

func desktopSession(runtime string) launchSession {
	display := os.Getenv("WAYLAND_DISPLAY")
	if display != "" && !filepath.IsAbs(display) {
		display = filepath.Join(runtime, display)
	}
	return launchSession{os.Getenv("XDG_SESSION_ID"), os.Getenv("DBUS_SESSION_BUS_ADDRESS"), display}
}

func validateSession(ctx context.Context, runtime string, session launchSession, cancel context.CancelCauseFunc) (*dbus.Conn, error) {
	if session.Display == "" || session.Bus == "" {
		return nil, errors.New("a GNOME Wayland session with a session D-Bus is required")
	}
	for _, helper := range []string{"xdg-dbus-proxy", "pw-container", "stdbuf"} {
		if _, err := exec.LookPath(helper); err != nil {
			return nil, fmt.Errorf("find required host helper %s: %w", helper, err)
		}
	}
	if err := probeSocket(ctx, session.Display); err != nil {
		return nil, fmt.Errorf("cannot connect to the Wayland display: %w", err)
	}
	remote := os.Getenv("PIPEWIRE_REMOTE")
	if remote == "" {
		remote = "pipewire-0"
	}
	if !filepath.IsAbs(remote) {
		pipeRuntime := os.Getenv("PIPEWIRE_RUNTIME_DIR")
		if pipeRuntime == "" {
			pipeRuntime = runtime
		}
		remote = filepath.Join(pipeRuntime, remote)
	}
	if err := probeSocket(ctx, remote); err != nil {
		return nil, fmt.Errorf("cannot connect to host PipeWire: %w", err)
	}
	bus, err := dbus.Connect(session.Bus, dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("cannot connect to session D-Bus: %w", err)
	}
	if err := watchSession(ctx, bus, cancel); err != nil {
		_ = bus.Close()
		return nil, fmt.Errorf("cannot monitor the GNOME session: %w", err)
	}
	return bus, nil
}

func probeSocket(ctx context.Context, path string) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	return conn.Close()
}

func watchSession(ctx context.Context, bus *dbus.Conn, cancel context.CancelCauseFunc) error {
	signals := make(chan *dbus.Signal, 8)
	bus.Signal(signals)
	if err := bus.AddMatchSignalContext(ctx, dbus.WithMatchSender(sessionManager), dbus.WithMatchInterface(sessionManager),
		dbus.WithMatchObjectPath("/org/gnome/SessionManager"), dbus.WithMatchMember("SessionOver")); err != nil {
		return err
	}
	if err := bus.AddMatchSignalContext(ctx, dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, sessionManager)); err != nil {
		return err
	}
	var owner string
	if err := bus.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, sessionManager).Store(&owner); err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-bus.Context().Done():
				if ctx.Err() == nil {
					cancel(errors.New("GNOME session bus disconnected"))
				}
				return
			case signal, ok := <-signals:
				if ctx.Err() != nil {
					return
				}
				if !ok || signal.Name == sessionManager+".SessionOver" ||
					(signal.Name == "org.freedesktop.DBus.NameOwnerChanged" && len(signal.Body) == 3 && signal.Body[1] == owner) {
					cancel(errors.New("GNOME session ended"))
					return
				}
			}
		}
	}()
	return nil
}

func notifyLaunchFailure(ctx context.Context, message string) {
	if address := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); address != "" {
		bus, err := dbus.Connect(address, dbus.WithContext(ctx))
		if err != nil {
			return
		}
		defer bus.Close()
		_ = bus.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").CallWithContext(ctx,
			"org.freedesktop.Notifications.Notify", 0, "Workshop Desktop", uint32(0), "",
			"Cannot launch Workshop app", message, []string{}, map[string]dbus.Variant{}, int32(-1)).Err
	}
}
