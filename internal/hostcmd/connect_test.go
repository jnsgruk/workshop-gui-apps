package hostcmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"workshop-desktop/internal/host"
)

func TestConnectOrdersAppsMountsAndInterfaces(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	log := filepath.Join(root, "calls")
	script := `#!/bin/sh
printf '%s | %s\n' "$PWD" "$*" >> "$CONNECT_TEST_LOG"
if [ "$1" = remount ] && [ "$CONNECT_TEST_MODE" = remount ]; then
  exit 3
fi
if [ "$1 $2" = 'connect apps/host-integration:desktop' ]; then
  case "$CONNECT_TEST_MODE" in
    ordinary)
      printf '%s\n' 'error: interface connection failed' >&2
      exit 7
      ;;
    already|near-match)
      printf 'error: cannot perform the following tasks:\n- Connect "%s" to "apps/system:desktop" (desktop interface already connected)\n' "$2" >&2
      if [ "$CONNECT_TEST_MODE" = near-match ]; then
        printf '%s\n' 'error: a second task failed' >&2
      fi
      exit 7
      ;;
  esac
fi
`
	if err := os.WriteFile(filepath.Join(root, "workshop"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", root)
	t.Setenv("CONNECT_TEST_LOG", log)
	p := host.Project{Directory: project, Root: root}
	wantCalls := []string{
		"stop apps",
		"remount apps/host-integration:host-session " + filepath.Join(root, "session"),
		"remount apps/host-integration:host-pipewire " + filepath.Join(root, "pipewire"),
		"start apps",
		"connect apps/host-integration:desktop",
		"connect apps/firefox:camera",
	}
	for _, tc := range []struct {
		mode   string
		status int
		calls  int
	}{
		{"success", 0, len(wantCalls)},
		{"already", 0, len(wantCalls)},
		{"ordinary", 7, 5},
		{"near-match", 7, 5},
		{"remount", 3, 2},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("CONNECT_TEST_MODE", tc.mode)
			if err := os.WriteFile(log, nil, 0600); err != nil {
				t.Fatal(err)
			}
			err := connect(context.Background(), p, "apps")
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != tc.status {
					t.Fatalf("wanted command failure with status %d, got %v", tc.status, err)
				}
			}

			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			var want strings.Builder
			for _, call := range wantCalls[:tc.calls] {
				want.WriteString(project + " | " + call + "\n")
			}
			if string(data) != want.String() {
				t.Fatalf("wrong connection sequence:\n%s\nwant:\n%s", data, want.String())
			}
		})
	}
}

func TestConnectAcquiresBothLocksBeforeCallingWorkshop(t *testing.T) {
	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	t.Setenv("PATH", root)
	t.Setenv("CONNECT_TEST_LOG", calls)
	if err := os.WriteFile(filepath.Join(root, "workshop"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$CONNECT_TEST_LOG"
`), 0700); err != nil {
		t.Fatal(err)
	}

	p := host.Project{Directory: t.TempDir(), Root: root}
	for _, name := range []string{"dbus", "pipewire"} {
		t.Run(name, func(t *testing.T) {
			lock, err := p.Lock(name)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := connect(context.Background(), p, "apps"); err == nil || !strings.Contains(err.Error(), name+" helper already running") {
				t.Fatalf("connect did not report %s lock contention: %v", name, err)
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatalf("called workshop before acquiring locks: %v", err)
			}
		})
	}
	lock, err := p.Lock("dbus")
	if err != nil {
		t.Fatal("connect leaked the first lock:", err)
	}
	lock.Close()
}
