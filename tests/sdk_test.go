package integration

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

func TestAudioWrapperPreservesApplicationEnvironmentAndArguments(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "service")
	wrapper, err := filepath.Abs("../.workshop/host-audio/bin/workshop-host-audio")
	if err != nil {
		t.Fatal(err)
	}

	script := "#!/bin/sh\nprintf '%s\\n' \"$XDG_RUNTIME_DIR\" \"$DBUS_SESSION_BUS_ADDRESS\" \"$@\" > \"$AUDIO_TEST_LOG\"\nexit \"${AUDIO_TEST_STATUS:-0}\"\n"
	if err := os.WriteFile(filepath.Join(root, "systemctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", root)
	t.Setenv("AUDIO_TEST_LOG", log)
	t.Setenv("XDG_RUNTIME_DIR", "/custom/runtime")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/host/proxy")
	t.Setenv("PULSE_SERVER", "old-server")
	t.Setenv("AUDIO_TEST_STATUS", "0")

	args := []string{wrapper, "/bin/sh", "-c",
		`printf '%s\n' "$XDG_RUNTIME_DIR" "$DBUS_SESSION_BUS_ADDRESS" "$PULSE_SERVER" "$@"; exit 7`,
		"application", "argument with spaces", "$(not a shell command)",
	}
	cmd := exec.Command("/bin/bash", args...)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
		t.Fatalf("lost application status: %v, %s", err, output)
	}

	want := "/custom/runtime\nunix:path=/host/proxy\nunix:/run/user/1000/pulse/native\nargument with spaces\n$(not a shell command)\n"
	if string(output) != want {
		t.Fatalf("changed application environment or arguments: %q", output)
	}

	service, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(service) != "/run/user/1000\nunix:path=/run/user/1000/bus\n--user\nstart\nworkshop-pipewire-pulse.service\n" {
		t.Fatalf("service did not use local bus: %s", service)
	}

	t.Setenv("AUDIO_TEST_STATUS", "9")
	output, err = exec.Command("/bin/bash", args...).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 9 || len(output) != 0 {
		t.Fatalf("ignored service failure: %v, %s", err, output)
	}

	output, err = exec.Command("/bin/bash", wrapper).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(output), "Usage:") {
		t.Fatalf("missing command accepted: %v, %s", err, output)
	}
}

func TestOpenerInstallRequiresBuiltClientBeforeChangingGuest(t *testing.T) {
	hook, err := filepath.Abs("../.workshop/host-open/hooks/setup-base")
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("SDK", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	output, err := exec.Command("/bin/bash", hook).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("missing client did not stop installation: %v, %s", err, output)
	}
	if !strings.Contains(string(output), "workshop run dev -- build") {
		t.Fatalf("missing rebuild instructions: %s", output)
	}
}

func TestGuestOpenerWrapperPreservesArgumentsAndExitStatus(t *testing.T) {
	wrapper, err := os.ReadFile("../.workshop/host-open/bin/xdg-open")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "workshop-desktop")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\nexit 4\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// Substitute only the installation path so no guest installation is needed.
	content := strings.Replace(string(wrapper), "/usr/local/bin/workshop-desktop", binary, 1)
	path := filepath.Join(root, "xdg-open")
	if err := os.WriteFile(path, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(path, "a file with spaces", "$(not a shell command)").CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 4 {
		t.Fatalf("lost guest status: %v, %s", err, output)
	}
	if string(output) != "guest\nopen\na file with spaces\n$(not a shell command)\n" {
		t.Fatalf("changed guest command arguments: %q", output)
	}
}

func TestFirefoxWrapperSwitchesBusAfterSnapAndChecksSocket(t *testing.T) {
	wrapper, err := os.ReadFile("../.workshop/firefox-desktop/bin/workshop-firefox")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// Substitute the confined runtime path, leaving the local Snap startup bus
	// untouched. The fake snap executes the real inner shell command.
	socket := filepath.Join(root, "bus")
	content := strings.ReplaceAll(string(wrapper), "/run/user/1000/snap.firefox/hostbus/bus", socket)
	path := filepath.Join(root, "workshop-firefox")
	for name, script := range map[string]string{
		"workshop-firefox": content,
		"id":               "#!/bin/sh\nprintf '1000\\n'\n",
		"snap": `#!/bin/sh
[ "$DBUS_SESSION_BUS_ADDRESS" = unix:path=/run/user/1000/bus ] || exit 90
[ "$XDG_RUNTIME_DIR" = /run/user/1000 ] || exit 91
[ "${DBUS_SYSTEM_BUS_ADDRESS+x}" != x ] || exit 92
[ "$1 $2 $3" = 'run --shell firefox' ] || exit 93
shift 3
exec /bin/bash "$@"
`,
		"firefox.launcher": `#!/bin/sh
printf '%s\n' "$DBUS_SESSION_BUS_ADDRESS" "$@"
exit 7
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	t.Setenv("SNAP", root)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/inherited-host-bus")
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path=/inherited-system-bus")
	args := []string{path, "argument with spaces", "$(not a shell command)"}
	// Missing endpoints must produce an actionable error before starting Firefox.
	output, err := exec.Command("/bin/bash", args...).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(string(output), "Host session D-Bus socket is missing") {
		t.Fatalf("missing socket: %v, %s", err, output)
	}
	listener, err := net.Listen("unix", socket)
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("sandbox prohibits binding a Unix socket; missing-socket check passed")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	output, err = exec.Command("/bin/bash", args...).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
		t.Fatalf("lost Firefox exit status: %v, %s", err, output)
	}
	want := "unix:path=" + socket + "\n--no-remote\nargument with spaces\n$(not a shell command)\n"
	if string(output) != want {
		t.Fatalf("changed Firefox bus or arguments: %q", output)
	}
}
