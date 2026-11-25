package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestDispatchAndHelpWithoutHostProject(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args   string
		code   int
		output string
	}{
		{"", 2, "error:"},
		{"--help", 0, "Add --verbose after a host operation or guest launch"},
		{"help", 0, "Add --verbose after a host operation or guest launch"},
		{"version", 0, version},
		{"version extra", 2, "unexpected positional arguments"},
		{"unknown", 2, "Run \"workshop-desktop help\""},
		{"host", 2, "Run \"workshop-desktop help host\""},
		{"host unknown", 2, "unknown"},
		{"host --help", 0, "install-launcher"},
		{"host connect --help", 0, "--workshop"},
		{"host connect -project /missing", 2, "unknown flag"},
		{"host serve --help", 0, "--handlers"},
		{"host start --help", 0, "--workshop"},
		{"host launch --help", 0, "-- <command>"},
		{"host launch --", 2, "missing command"},
		{"host launch true -- false", 2, "after --"},
		{"host start --project /missing --help", 0, "--project"},
		{"host start --workshop apps --workshop ../bad", 2, "invalid workshop name"},
		{"host start --quiet --verbose", 2, "use only one"},
		{"host start --verbose --quiet", 2, "use only one"},
		{"host serve --handlers invalid", 2, "unknown handler"},
		{"host serve --handlers=", 2, "missing handler names"},
		{"host serve --handlers dbus --handlers pipewire", 2, "only once"},
		{"host serve --only dbus", 2, "unknown flag"},
		{"host serve --handlers dbus,pipewire unexpected", 2, "unexpected positional arguments"},
		{"host install-launcher", 2, "missing SDK name or --all"},
		{"host install-launcher --all demo", 2, "cannot specify both"},
		{"host remove-launcher --project /missing invalid/name", 2, "invalid SDK name"},
		{"host remove-launcher invalid/name --project /missing", 2, "invalid SDK name"},
		{"host up", 2, "Unknown command"},
		{"host dbus --help", 2, "Unknown command"},
		{"help host pipewire", 2, "unknown command"},
		{"host launchers install --help", 2, "Unknown command"},
		{"help host launchers remove", 2, "unknown command"},
		{"guest --help", 0, "launch"},
		{"guest launch --help", 0, "-- <command>"},
		{"guest launch --session-bus local --help", 0, "--session-bus"},
		{"guest launch -session-bus local --help", 2, "unknown flag"},
		{"guest launch --session-bus local --", 2, "missing command"},
		{"guest launch -- true", 2, "missing --session-bus"},
		{"guest launch --session-bus invalid -- true", 2, "invalid session bus"},
		{"guest audio --help", 2, "Unknown command"},
		{"-h", 2, "unknown flag"},
		{"guest launch --session-bus local true", 2, "after --"},
		{"guest launch --session-bus local true -- false", 2, "after --"},
		{"help host unknown", 2, "unknown command"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(strings.Fields(tc.args), &stdout, &stderr); code != tc.code {
				t.Fatalf("exit %d, want %d; stdout %q, stderr %q", code, tc.code, &stdout, &stderr)
			}
			got, other := stdout.String(), stderr.String()
			if tc.code != 0 {
				got, other = other, got
			}
			if !strings.Contains(got, tc.output) || other != "" {
				t.Fatalf("stdout %q, stderr %q; want %q on designated stream only", &stdout, &stderr, tc.output)
			}
		})
	}
}

func TestLaunchCommandBoundary(t *testing.T) {
	want := []string{"/some command", "--help", "", "--", "-x"}
	for _, options := range [][]string{{"guest", "launch", "--session-bus", "local"}, {"host", "launch", "--notify"}} {
		args := slices.Concat(options, []string{"--"}, want)
		gotOptions, gotCommand := splitLaunchCommand(args)
		if !slices.Equal(gotOptions, options) || !slices.Equal(gotCommand, want) {
			t.Fatalf("split %q into %q and %q; want %q and %q", args, gotOptions, gotCommand, options, want)
		}
	}
}

func TestLauncherRejectsEmptySDKWithAll(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"host", "remove-launcher", "--all", ""}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "empty SDK name") {
		t.Fatalf("exit %d; stdout %q, stderr %q", code, &stdout, &stderr)
	}
}
