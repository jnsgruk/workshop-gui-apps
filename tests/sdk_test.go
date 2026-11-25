package integration

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
			output, err := exec.CommandContext(t.Context(), wrapper, tc.args...).CombinedOutput()
			code := 0
			if err != nil {
				exit, ok := errors.AsType[*exec.ExitError](err)
				if !ok {
					t.Fatalf("run %q: %v, output %q", tc.args, err, output)
				}
				code = exit.ExitCode()
			}
			if code != tc.code || !strings.Contains(string(output), tc.output) {
				t.Fatalf("got exit %d and output %q, want exit %d and output containing %q", code, output, tc.code, tc.output)
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
	for _, path := range []string{"hooks/setup-base", "bin/xdg-open", "systemd/workshop-pipewire-pulse.service"} {
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

	oldWrapper := filepath.Join(guest, "usr/local/bin/workshop-host-audio")
	if err := os.WriteFile(oldWrapper, []byte("old wrapper"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Run the unchanged hook. Only the tools that touch the filesystem
	// redirect their writes into the disposable root.
	for name, script := range map[string]string{
		"apt-get":   "#!/bin/sh\nexit 0\n",
		"systemctl": "#!/bin/sh\nexit 0\n",
		"install": `#!/bin/sh
[ "$#" = 4 ] || exit 87
if [ "$1" = -d ] && [ "$2" = -m ] && [ "$4" = /mnt/workshop-host ]; then
  exec "$SETUP_TEST_INSTALL" "$1" "$2" "$3" "$SETUP_TEST_ROOT$4"
fi
[ "$1" = -m ] || exit 87
case "$4" in
  /usr/*|/etc/*) exec "$SETUP_TEST_INSTALL" "$1" "$2" "$3" "$SETUP_TEST_ROOT$4";;
  *) exit 87;;
esac
`,
		"rm": `#!/bin/sh
[ "$#" = 3 ] && [ "$1" = -f ] && [ "$2" = /etc/profile.d/workshop-host-audio.sh ] && [ "$3" = /usr/local/bin/workshop-host-audio ] || exit 88
exec "$SETUP_TEST_REMOVE" "$1" "$SETUP_TEST_ROOT$2" "$SETUP_TEST_ROOT$3"
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
		info, err := os.Stat(filepath.Join(guest, "mnt/workshop-host"))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o711 {
			t.Fatalf("host mount parent must be traversable: %v, %v", info, err)
		}
		for _, tc := range []struct {
			source, target string
			mode           os.FileMode
		}{
			{"bin/workshop-desktop", "usr/local/bin/workshop-desktop", 0o755},
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
		for _, obsolete := range []string{profile, oldWrapper} {
			if _, err := os.Stat(obsolete); !os.IsNotExist(err) {
				t.Fatalf("obsolete file %s remains: %v", obsolete, err)
			}
		}
	}
	output, err := exec.Command(divert, "--root", guest, "--truename", "/usr/bin/xdg-open").CombinedOutput()
	if err != nil || string(output) != "/usr/bin/xdg-open.distrib\n" {
		t.Fatalf("package diversion missing: %q, %v", output, err)
	}
}
