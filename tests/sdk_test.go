package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(wrapper, []byte(strings.Replace(string(content), "/usr/local/bin/workshop-desktop", binary, 1)), 0o700); err != nil {
		t.Fatal(err)
	}

	script := "#!/bin/sh\nprintf '%s\\n' \"$XDG_RUNTIME_DIR\" \"$DBUS_SESSION_BUS_ADDRESS\" \"$@\" > \"$AUDIO_TEST_LOG\"\nexit \"${AUDIO_TEST_STATUS:-0}\"\n"
	if err := os.WriteFile(filepath.Join(root, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", root)
	t.Setenv("AUDIO_TEST_LOG", log)
	t.Setenv("XDG_RUNTIME_DIR", "/custom/runtime")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/host/proxy")
	t.Setenv("PULSE_SERVER", "old-server")
	t.Setenv("AUDIO_TEST_STATUS", "0")

	args := []string{
		wrapper, "/bin/sh", "-c",
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
	if err := os.WriteFile(filepath.Join(root, "gdbus"), []byte(script), 0o700); err != nil {
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
		{[]string{"README.md"}, 1, "Usage: xdg-open URL"},
		{[]string{"https:"}, 1, "Usage: xdg-open URL"},
		{[]string{"/project/README.md"}, 1, "Usage: xdg-open URL"},
		{[]string{"FiLe:///project/README.md"}, 1, "local files are unsupported"},
		{[]string{"1https://example.com"}, 1, "Usage: xdg-open URL"},
		{[]string{"https://example.com/a b"}, 1, "Usage: xdg-open URL"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "call")
			t.Setenv("OPENER_TEST_LOG", log)
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

	for _, uri := range []string{"https://example.com/a?x=$(test)&q='text'", "mailto:someone@example.com", "https://example.org/tag:"} {
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

func TestSetupInstallsStagedLauncherAndGuestWrappers(t *testing.T) {
	divert, err := exec.LookPath("dpkg-divert")
	if err != nil {
		t.Skip("dpkg-divert is required to exercise setup in a disposable Debian root")
	}
	install, err := exec.LookPath("install")
	if err != nil {
		t.Fatal(err)
	}
	remove, err := exec.LookPath("rm")
	if err != nil {
		t.Fatal(err)
	}

	sdk := t.TempDir()
	guest := t.TempDir()
	tools := t.TempDir()
	for _, path := range []string{"bin", "hooks", "systemd"} {
		if err := os.Mkdir(filepath.Join(sdk, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"usr/bin", "usr/local/bin", "etc/systemd/user", "etc/profile.d", "var/lib/dpkg"} {
		if err := os.MkdirAll(filepath.Join(guest, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"hooks/setup-base", "bin/xdg-open", "bin/workshop-host-audio", "systemd/workshop-pipewire-pulse.service"} {
		content, err := os.ReadFile(filepath.Join("../.workshop/host-integration", path))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sdk, path), content, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	binary := filepath.Join(sdk, "bin/workshop-desktop")
	if output, err := exec.Command("go", "build", "-o", binary, "../cmd/workshop-desktop").CombinedOutput(); err != nil {
		t.Fatalf("build staged launcher: %v, %s", err, output)
	}
	const packagedOpener = "packaged xdg-open"
	opener := filepath.Join(guest, "usr/bin/xdg-open")
	if err := os.WriteFile(opener, []byte(packagedOpener), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(guest, "etc/profile.d/workshop-host-audio.sh")
	if err := os.WriteFile(profile, []byte("old profile"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run the unchanged hook. Only the tools that touch the filesystem
	// redirect their writes into the disposable root.
	for name, script := range map[string]string{
		"apt-get":   "#!/bin/sh\nexit 0\n",
		"systemctl": "#!/bin/sh\nexit 0\n",
		"install": `#!/bin/sh
[ "$#" = 4 ] && [ "$1" = -m ] || exit 87
case "$4" in
  /usr/*|/etc/*) exec "$SETUP_TEST_INSTALL" "$1" "$2" "$3" "$SETUP_TEST_ROOT$4";;
  *) exit 87;;
esac
`,
		"rm": `#!/bin/sh
[ "$#" = 2 ] && [ "$1" = -f ] && [ "$2" = /etc/profile.d/workshop-host-audio.sh ] || exit 88
exec "$SETUP_TEST_REMOVE" "$1" "$SETUP_TEST_ROOT$2"
`,
		"dpkg-divert": `#!/bin/sh
exec "$SETUP_TEST_DIVERT" --root "$SETUP_TEST_ROOT" "$@"
`,
	} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SDK", sdk)
	t.Setenv("PATH", tools)
	t.Setenv("SETUP_TEST_ROOT", guest)
	t.Setenv("SETUP_TEST_INSTALL", install)
	t.Setenv("SETUP_TEST_REMOVE", remove)
	t.Setenv("SETUP_TEST_DIVERT", divert)

	for range 2 {
		cmd := exec.Command("/bin/bash", filepath.Join(sdk, "hooks/setup-base"))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run setup: %v, %s", err, output)
		}
		for _, tc := range []struct {
			source, target string
			mode           os.FileMode
		}{
			{"bin/workshop-desktop", "usr/local/bin/workshop-desktop", 0o755},
			{"bin/workshop-host-audio", "usr/local/bin/workshop-host-audio", 0o755},
			{"bin/xdg-open", "usr/bin/xdg-open", 0o755},
			{"systemd/workshop-pipewire-pulse.service", "etc/systemd/user/workshop-pipewire-pulse.service", 0o644},
		} {
			want, err := os.ReadFile(filepath.Join(sdk, tc.source))
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(guest, tc.target)
			got, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("installed %s does not match staged %s: %v", tc.target, tc.source, err)
			}
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != tc.mode {
				t.Fatalf("installed %s has wrong mode: %v, %v", tc.target, info, err)
			}
		}
		if got, err := os.ReadFile(opener + ".distrib"); err != nil || string(got) != packagedOpener {
			t.Fatalf("did not preserve the packaged opener: %q, %v", got, err)
		}
		if _, err := os.Stat(profile); !os.IsNotExist(err) {
			t.Fatalf("old profile remains: %v", err)
		}
	}
	output, err := exec.Command(divert, "--root", guest, "--truename", "/usr/bin/xdg-open").CombinedOutput()
	if err != nil || string(output) != "/usr/bin/xdg-open.distrib\n" {
		t.Fatalf("package diversion missing: %q, %v", output, err)
	}
}
