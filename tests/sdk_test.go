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
	content, err := os.ReadFile("../.workshop/host-integration/bin/workshop-host-audio")
	if err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(root, "workshop-desktop")
	if output, err := exec.Command("go", "build", "-o", binary, "../cmd/workshop-desktop").CombinedOutput(); err != nil {
		t.Fatalf("build guest commands: %v, %s", err, output)
	}
	wrapper := filepath.Join(root, "workshop-host-audio")
	if err := os.WriteFile(wrapper, []byte(strings.Replace(string(content), "/usr/local/bin/workshop-desktop", binary, 1)), 0700); err != nil {
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

func TestSetupRequiresBuiltBinaryBeforeChangingGuest(t *testing.T) {
	hook, err := filepath.Abs("../.workshop/host-integration/hooks/setup-base")
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("SDK", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	output, err := exec.Command("/bin/bash", hook).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("missing binary did not stop installation: %v, %s", err, output)
	}
	if !strings.Contains(string(output), "workshop run dev -- build") {
		t.Fatalf("missing rebuild instructions: %s", output)
	}
}

func TestGuestOpenerWrapper(t *testing.T) {
	wrapper, err := filepath.Abs("../.workshop/host-integration/bin/xdg-open")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	log := filepath.Join(root, "call")
	script := `#!/bin/sh
printf '%s\n' "$DBUS_SESSION_BUS_ADDRESS" "$@" > "$OPENER_TEST_LOG"
printf '%s\n' '/org/freedesktop/portal/desktop/request/handle'
if [ "$OPENER_TEST_FAIL" = 1 ]; then
    printf '%s\n' 'Could not connect to the host session bus' >&2
    exit 4
fi
`
	if err := os.WriteFile(filepath.Join(root, "gdbus"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("OPENER_TEST_LOG", log)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/guest/bus")

	for _, tc := range []struct {
		args   []string
		code   int
		output string
	}{
		{nil, 1, "Usage: xdg-open URL"},
		{[]string{"https://example.com", "--help"}, 1, "Usage: xdg-open URL"},
		{[]string{"--help"}, 0, "Usage: xdg-open URL"},
		{[]string{"--version"}, 0, "xdg-open workshop-portal"},
		{[]string{"/project/README.md"}, 1, "Usage: xdg-open URL"},
		{[]string{"FiLe:///project/README.md"}, 1, "local files are unsupported"},
		{[]string{"1https://example.com"}, 1, "Usage: xdg-open URL"},
		{[]string{"https://example.com/a b"}, 1, "Usage: xdg-open URL"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd := exec.Command(wrapper, tc.args...)
			output, _ := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.code || !strings.Contains(string(output), tc.output) {
				t.Fatalf("unexpected status or output: %v, %q", cmd.ProcessState, output)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatalf("gdbus called for %q: %v", tc.args, err)
			}
		})
	}

	for _, uri := range []string{"https://example.com/a?x=$(test)&q='text'", "mailto:someone@example.com"} {
		output, err := exec.Command(wrapper, uri).CombinedOutput()
		if err != nil || len(output) != 0 {
			t.Fatalf("unexpected result: %v, %q", err, output)
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Join([]string{
			"unix:path=/guest/bus",
			"call", "--address", "unix:path=/mnt/workshop-host/session/bus",
			"--dest", "org.freedesktop.portal.Desktop",
			"--object-path", "/org/freedesktop/portal/desktop",
			"--method", "org.freedesktop.portal.OpenURI.OpenURI",
			"", uri, "{}", "",
		}, "\n")
		if string(data) != want {
			t.Fatalf("changed gdbus arguments: %q", data)
		}
	}

	t.Setenv("OPENER_TEST_FAIL", "1")
	output, err := exec.Command(wrapper, "https://example.com").CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 4 ||
		string(output) != "Could not connect to the host session bus\n" {
		t.Fatalf("lost gdbus error: %v, %q", err, output)
	}
}

func TestFirefoxWrapperSwitchesBusAfterSnapAndChecksSocket(t *testing.T) {
	wrapper, err := os.ReadFile("../.workshop/firefox/bin/workshop-firefox")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// Substitute the confined runtime path, leaving the local Snap startup bus
	// untouched. The fake snap executes the real inner shell command.
	socket := filepath.Join(root, "bus")
	content := strings.ReplaceAll(string(wrapper), "/run/user/1000/snap.firefox/hostbus/bus", socket)
	content = strings.Replace(content, "/usr/local/bin/workshop-desktop", filepath.Join(root, "workshop-desktop"), 1)
	path := filepath.Join(root, "workshop-firefox")
	for name, script := range map[string]string{
		"workshop-firefox": content,
		"workshop-desktop": `#!/bin/sh
[ "$1 $2 $3" = 'guest launch --session-bus=local' ] || exit 90
shift 3
exec "$@"
`,
		"snap": `#!/bin/sh
[ "$MOZ_ENABLE_WAYLAND" = 1 ] || exit 91
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
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/inherited-host-bus")
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
