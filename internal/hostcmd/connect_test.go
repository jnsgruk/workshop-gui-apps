package hostcmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workshop-desktop/internal/host"
)

func TestConnectOrdersAppsMountsAndInterfaces(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$CONNECT_TEST_LOG"
if [ "$1" = remount ] && [ "$CONNECT_TEST_FAIL" = 1 ]; then
  exit 1
fi
if [ "$1" = connect ] && [ "$CONNECT_TEST_ALREADY" = 1 ]; then
  capability=${2##*:}
  printf 'error: cannot perform the following tasks:\n- Connect "%s" to "apps/system:%s" (%s interface already connected)\n' "$2" "$capability" "$capability"
  exit 1
fi
`
	if err := os.WriteFile(filepath.Join(root, "workshop"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", root)
	t.Setenv("CONNECT_TEST_LOG", log)
	t.Setenv("CONNECT_TEST_ALREADY", "1")
	p := host.Project{Directory: root, Root: root}

	if err := connect(context.Background(), p, "apps"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"stop apps",
		"remount apps/host-integration:host-session " + filepath.Join(root, "session"),
		"remount apps/host-integration:host-pipewire " + filepath.Join(root, "pipewire"),
		"start apps",
		"connect apps/host-integration:desktop",
		"connect apps/firefox:camera",
		"",
	}, "\n")
	if string(data) != want {
		t.Fatalf("wrong connection sequence:\n%s", data)
	}

	// An unrelated remount failure must not be treated as an existing connection.
	t.Setenv("CONNECT_TEST_FAIL", "1")
	if err := os.WriteFile(log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := connect(context.Background(), p, "apps"); err == nil {
		t.Fatal("ignored remount failure")
	}

	data, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "start apps") {
		t.Fatal("started workshop after failed remount")
	}
}
