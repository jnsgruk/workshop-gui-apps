package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDispatchAndHelpWithoutHostProject(t *testing.T) {
	// Neither guest operations nor help should load a host .workshop.lock.
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args   []string
		code   int
		output string
	}{
		{nil, 2, "Usage:"},
		{[]string{"--help"}, 0, "Usage:"},
		{[]string{"unknown"}, 2, "Usage:"},
		{[]string{"host"}, 2, "Usage:"},
		{[]string{"host", "unknown"}, 2, "unknown host command"},
		{[]string{"host", "--help"}, 0, "Usage:"},
		{[]string{"host", "connect", "--help"}, 0, "-workshop"},
		{[]string{"host", "serve", "--help"}, 0, "-only"},
		{[]string{"host", "up", "--help"}, 0, "-workshop"},
		{[]string{"host", "up", "--workshop=../apps"}, 2, "invalid workshop name"},
		{[]string{"host", "up", "--only=invalid"}, 2, "unknown handler"},
		{[]string{"host", "open"}, 2, "unknown host command"},
		{[]string{"host", "serve", "--only=invalid"}, 2, "unknown handler"},
		{[]string{"host", "serve", "--only="}, 2, "unknown handler"},
		{[]string{"host", "serve", "--only=pipewire,dbus", "unexpected"}, 2, "unexpected positional arguments"},
		{[]string{"host", "dbus", "--help"}, 0, "-project"},
		{[]string{"host", "pipewire", "--help"}, 0, "-project"},
		{[]string{"guest"}, 2, "Usage:"},
		{[]string{"guest", "unknown"}, 2, "Usage:"},
		{[]string{"guest", "--help"}, 0, "Usage:"},
		{[]string{"guest", "launch", "--help"}, 0, "--session-bus"},
		{[]string{"guest", "launch", "--session-bus=local"}, 2, "Usage:"},
		{[]string{"guest", "launch", "--session-bus=invalid", "/bin/true"}, 2, "Usage:"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != tc.code {
				t.Fatalf("exit %d, want %d: %s%s", code, tc.code, &stdout, &stderr)
			}
			if output := stdout.String() + stderr.String(); !strings.Contains(output, tc.output) {
				t.Fatalf("output %q, want %q", output, tc.output)
			}
		})
	}
}
