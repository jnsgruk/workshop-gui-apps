package guestcmd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"workshop-desktop/internal/logging"
)

// The subprocess runs the same launcher as the CLI and lets it replace itself.
func TestLaunchChild(t *testing.T) {
	root := os.Getenv("LAUNCH_TEST_ROOT")
	if root == "" || len(os.Args) < 4 || os.Args[2] != "--" || os.Args[3] != "launch-child" {
		return
	}
	args := os.Args[4:]
	if len(args) < 6 || args[0] != "guest" || args[1] != "launch" ||
		args[2] != "--session-bus" || args[4] != "--" {
		t.Fatalf("unexpected launch arguments: %q", args)
	}
	verbosity := logging.Brief
	switch os.Getenv("LAUNCH_TEST_VERBOSITY") {
	case "quiet":
		verbosity = logging.Quiet
	case "verbose":
		verbosity = logging.Verbose
	}
	code, err := launch(args[5:], args[3], os.Stdout, os.Stderr, 1000, root,
		filepath.Join(root, "host-bus"), filepath.Join(root, "pipewire"), verbosity)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	os.Exit(code)
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
printf '%s\n' "probe output"
printf '%s\n' "probe diagnostic" >&2
exit "${PROBE_STATUS:-0}"
`,
		"systemctl": `#!/bin/sh
printf '%s\n' "$XDG_RUNTIME_DIR" "$DBUS_SESSION_BUS_ADDRESS" "$@" > "$LAUNCH_TEST_ROOT/service-log"
printf '%s\n' "service output"
printf '%s\n' "service diagnostic" >&2
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
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0o700); err != nil {
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
		t.Cleanup(func() { _ = listener.Close() })
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
			"--session-bus", "host", "--", app, "", "--", "--help", "argument with spaces", "$(not a shell command)")
	}
	t.Run("prerequisites", func(t *testing.T) {
		for _, tc := range []struct {
			name, display, status, message string
		}{
			{"no display", "", "0", "a Wayland display is required"},
			{"failed display probe", "test-display", "9", `cannot connect to the Wayland display: exit status 9: "probe diagnostic"`},
			{"missing host bus", "test-display", "0", "required session D-Bus socket is missing at " + hostBus},
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
		if output := run(t, hostLaunch(), 1); !strings.Contains(output, "host PipeWire socket is missing") {
			t.Fatalf("missing PipeWire prerequisite error: %q", output)
		}
		if _, err := os.Stat(serviceLog); !os.IsNotExist(err) {
			t.Fatalf("started audio without prerequisites: %v", err)
		}
	})
	listen(t, hostBus)
	listen(t, filepath.Join(root, "pipewire"))
	listen(t, localBus)
	for _, verbosity := range []string{"brief", "quiet", "verbose"} {
		t.Run("host mode "+verbosity, func(t *testing.T) {
			t.Setenv("LAUNCH_TEST_VERBOSITY", verbosity)
			var stderr strings.Builder
			cmd := hostLaunch()
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 8 {
				t.Fatalf("lost application status: %v, %s", err, &stderr)
			}
			want := root + "\nunix:path=" + hostBus + "\nunset\nwayland\nunix:" + root +
				"/pulse/native\n\n--\n--help\nargument with spaces\n$(not a shell command)\n"
			if string(output) != want {
				t.Fatalf("wrong application environment or arguments: %q", output)
			}
			if verbosity == "verbose" {
				for _, detail := range []string{"time=", "msg=\"probe output\"", "msg=\"probe diagnostic\"", "msg=\"service output\"", "msg=\"service diagnostic\""} {
					if !strings.Contains(stderr.String(), detail) {
						t.Errorf("missing %q in verbose output: %s", detail, &stderr)
					}
				}
			} else if stderr.Len() != 0 {
				t.Fatalf("unexpected helper output: %s", &stderr)
			}
			assertLog(t, probeLog, "test-display\n"+root+"\n")
			assertLog(t, serviceLog, root+"\nunix:path="+localBus+"\n--user\nstart\nworkshop-pipewire-pulse.service\n")
		})
	}
	t.Run("firefox snap", func(t *testing.T) {
		args := []string{firefox, "argument with spaces", "$(not a shell command)"}
		snapWant := "unix:path=" + localBus + "\n1\nrun\n--shell\nfirefox\n"

		if output := run(t, exec.Command("/bin/bash", args...), 1); !strings.Contains(output, "host session D-Bus socket is missing") {
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
