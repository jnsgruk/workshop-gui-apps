package guestcmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCompatibilityArgumentsAndExitCodes(t *testing.T) {
	for _, args := range [][]string{nil, {""}, {"--unknown"}, {"one", "two"}, {"--help"}, {"-h"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr, func(string) error {
			t.Fatal("informational or invalid arguments contacted host")
			return nil
		})

		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "--version") {
			if code != 0 || stdout.Len() == 0 {
				t.Fatalf("args %v: code %d, stdout %q", args, code, stdout.String())
			}
		} else if code != 1 || stderr.Len() == 0 {
			t.Fatalf("args %v: code %d, stderr %q", args, code, stderr.String())
		}
	}

	for _, fail := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"a file with spaces"}, &stdout, &stderr, func(target string) error {
			if target != "a file with spaces" {
				t.Fatal("changed argument:", target)
			}
			if fail {
				return errors.New("denied")
			}
			return nil
		})

		if fail && (code != 4 || !strings.Contains(stderr.String(), "denied")) {
			t.Fatalf("lost host failure: %d, %q", code, stderr.String())
		}
		if !fail && code != 0 {
			t.Fatal("successful request failed:", stderr.String())
		}
	}
}
