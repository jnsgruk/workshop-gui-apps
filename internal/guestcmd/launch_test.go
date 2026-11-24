package guestcmd

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The subprocess runs the same launcher as the CLI and lets it replace itself.
func TestLaunchChild(t *testing.T) {
	root := os.Getenv("LAUNCH_TEST_ROOT")
	if root == "" || len(os.Args) < 4 || os.Args[2] != "--" || os.Args[3] != "launch-child" {
		return
	}
	args := os.Args[4:]
	if len(args) < 2 || args[0] != "guest" || args[1] != "launch" {
		t.Fatalf("unexpected launch arguments: %q", args)
	}
	os.Exit(launch(args[2:], os.Stdout, os.Stderr, 1000, root,
		filepath.Join(root, "host-bus"), filepath.Join(root, "pipewire")))
}

func TestGuestLaunch(t *testing.T) {
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := os.ReadFile("../../.workshop/firefox/bin/workshop-firefox")
	if err != nil {
		t.Fatal(err)
	}
	confinedBus := filepath.Join(root, "confined-bus")
	firefox := filepath.Join(root, "workshop-firefox")
	app := filepath.Join(root, "app")
	snapLog := filepath.Join(root, "snap-log")
	serviceLog := filepath.Join(root, "service-log")
	probeLog := filepath.Join(root, "probe-log")
	for name, script := range map[string]string{
		"workshop-desktop": `#!/bin/sh
exec "$LAUNCH_TEST_BINARY" -test.run='^TestLaunchChild$' -- launch-child "$@"
`,
		"workshop-firefox": strings.Replace(strings.ReplaceAll(string(wrapper),
			"/run/user/1000/snap.firefox/hostbus/bus", confinedBus),
			"/usr/local/bin/workshop-desktop", `"$LAUNCH_TEST_ROOT/workshop-desktop"`, 1),
		"wayland-info": `#!/bin/sh
printf '%s\n' "$WAYLAND_DISPLAY" "$XDG_RUNTIME_DIR" > "$LAUNCH_TEST_ROOT/probe-log"
exit "${PROBE_STATUS:-0}"
`,
		"systemctl": `#!/bin/sh
printf '%s\n' "$XDG_RUNTIME_DIR" "$DBUS_SESSION_BUS_ADDRESS" "$@" > "$LAUNCH_TEST_ROOT/service-log"
`,
		"snap": `#!/bin/sh
printf '%s\n' "$DBUS_SESSION_BUS_ADDRESS" "$MOZ_ENABLE_WAYLAND" "$1" "$2" "$3" > "$LAUNCH_TEST_ROOT/snap-log"
shift 3
exec /bin/bash "$@"
`,
		"firefox.launcher": `#!/bin/sh
printf '%s\n' "$DBUS_SESSION_BUS_ADDRESS" "$@"
exit 7
`,
		"app": `#!/bin/sh
printf '%s\n' "$XDG_RUNTIME_DIR" "$DBUS_SESSION_BUS_ADDRESS" "${DBUS_SYSTEM_BUS_ADDRESS-unset}" "$GDK_BACKEND" "$PULSE_SERVER" "$@"
exit 8
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	t.Setenv("LAUNCH_TEST_BINARY", executable)
	t.Setenv("LAUNCH_TEST_ROOT", root)
	t.Setenv("SNAP", root)
	t.Setenv("XDG_RUNTIME_DIR", "/inherited/runtime")
	t.Setenv("WAYLAND_DISPLAY", "test-display")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/inherited/session")
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path=/inherited/system")
	t.Setenv("GDK_BACKEND", "x11")
	t.Setenv("PULSE_SERVER", "inherited-audio")
	t.Setenv("PROBE_STATUS", "0")

	run := func(t *testing.T, cmd *exec.Cmd, wantStatus int) string {
		t.Helper()
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if (wantStatus == 0 && err != nil) || (wantStatus != 0 && (!ok || exit.ExitCode() != wantStatus)) {
			t.Fatalf("want exit %d, got %v with output %q", wantStatus, err, output)
		}
		return string(output)
	}
	listen := func(t *testing.T, path string) {
		t.Helper()
		listener, err := net.Listen("unix", path)
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skip("sandbox prohibits Unix socket binding")
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
	}
	assertLog := func(t *testing.T, path, want string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s: got %q, %v, want %q", filepath.Base(path), data, err, want)
		}
	}

	hostBus := filepath.Join(root, "host-bus")
	localBus := filepath.Join(root, "bus")
	hostLaunch := func() *exec.Cmd {
		return exec.Command(filepath.Join(root, "workshop-desktop"), "guest", "launch",
			"--session-bus=host", app, "argument with spaces", "$(not a shell command)")
	}
	t.Run("prerequisites", func(t *testing.T) {
		for _, tc := range []struct {
			name, display, status, message string
		}{
			{"no display", "", "0", "A Wayland display is required"},
			{"failed display probe", "test-display", "9", "Cannot connect to the Wayland display"},
			{"missing host bus", "test-display", "0", "Required session D-Bus socket is missing at " + hostBus},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Setenv("WAYLAND_DISPLAY", tc.display)
				t.Setenv("PROBE_STATUS", tc.status)
				if output := run(t, hostLaunch(), 1); !strings.Contains(output, tc.message) {
					t.Fatalf("missing prerequisite error: %q", output)
				}
			})
		}
		listen(t, hostBus)
		if output := run(t, hostLaunch(), 1); !strings.Contains(output, "Host PipeWire socket is missing") {
			t.Fatalf("missing PipeWire prerequisite error: %q", output)
		}
		if _, err := os.Stat(serviceLog); !os.IsNotExist(err) {
			t.Fatalf("started audio without prerequisites: %v", err)
		}
	})
	listen(t, hostBus)
	listen(t, filepath.Join(root, "pipewire"))
	listen(t, localBus)
	t.Run("host mode", func(t *testing.T) {
		output := run(t, hostLaunch(), 8)
		want := root + "\nunix:path=" + hostBus + "\nunset\nwayland\nunix:" + root +
			"/pulse/native\nargument with spaces\n$(not a shell command)\n"
		if output != want {
			t.Fatalf("wrong application environment or arguments: %q", output)
		}
		assertLog(t, probeLog, "test-display\n"+root+"\n")
		assertLog(t, serviceLog, root+"\nunix:path="+localBus+"\n--user\nstart\nworkshop-pipewire-pulse.service\n")
	})
	t.Run("firefox snap", func(t *testing.T) {
		args := []string{firefox, "argument with spaces", "$(not a shell command)"}
		snapWant := "unix:path=" + localBus + "\n1\nrun\n--shell\nfirefox\n"

		if output := run(t, exec.Command("/bin/bash", args...), 1); !strings.Contains(output, "Host session D-Bus socket is missing") {
			t.Fatalf("missing confined bus error: %q", output)
		}
		assertLog(t, snapLog, snapWant)
		listen(t, confinedBus)
		output := run(t, exec.Command("/bin/bash", args...), 7)
		assertLog(t, snapLog, snapWant)
		want := "unix:path=" + confinedBus + "\n--no-remote\nargument with spaces\n$(not a shell command)\n"
		if output != want {
			t.Fatalf("wrong Firefox bus or arguments: %q", output)
		}
	})
}
