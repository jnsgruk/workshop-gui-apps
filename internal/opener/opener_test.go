package opener

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file with spaces.txt"), []byte("shared"), 0600); err != nil {
		t.Fatal(err)
	}

	return root
}

func TestSharedFilesAndSchemes(t *testing.T) {
	root := project(t)
	wantFile := (&url.URL{Scheme: "file", Path: filepath.Join(root, "file with spaces.txt")}).String()
	for _, test := range []struct{ uri, want string }{
		{"file:///project/file%20with%20spaces.txt", wantFile},
		{"file://localhost/project/file%20with%20spaces.txt", wantFile},
		{"https://example.com/?value=$(touch%20/tmp/nope)", "https://example.com/?value=$(touch%20/tmp/nope)"},
		{"mailto:someone@example.com", "mailto:someone@example.com"},
		{"vscode://file/some/path", "vscode://file/some/path"},
	} {
		t.Run(test.uri, func(t *testing.T) {
			got, err := Target(test.uri, root)
			if err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestRejectedRequestsNeverLaunch(t *testing.T) {
	root := project(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	for _, message := range []string{
		"not json\n", "[]\n", "null\n", "{}\n", "{\"uri\":42}\n",
		"{\"uri\":\"file:///etc/passwd\"}\n", "{\"uri\":\"file:///project/../secret\"}\n",
		"{\"uri\":\"file:///project/escape/secret\"}\n", "{\"uri\":\"file://remote/project/file\"}\n",
		"{\"uri\":\"file:///project/file%20with%20spaces.txt?x=1\"}\n",
		"{\"uri\":\"file:///project/missing\"}\n", "{\"uri\":\"--help\"}\n",
		"{\"uri\":\"https://example.com/\\n\"}\n", "{\"uri\":\"https://example.com\"}",
		strings.Repeat("x", MaxRequest+1) + "\n",
	} {
		t.Run(message[:min(len(message), 80)], func(t *testing.T) {
			response := Handle(strings.NewReader(message), 1000, 1000, root, func(string) error { t.Fatal("launched rejected request"); return nil })
			if response.OK || response.Error == "" {
				t.Fatalf("expected rejection, got %+v", response)
			}
		})
	}
}

func TestPeerAndLauncherErrors(t *testing.T) {
	root := project(t)
	message := "{\"uri\":\"https://example.com\"}\n"
	response := Handle(strings.NewReader(message), 1001, 1000, root, func(string) error { t.Fatal("foreign user launched app"); return nil })
	if response.OK {
		t.Fatal("accepted foreign UID")
	}

	response = Handle(strings.NewReader(message), 1000, 1000, root, func(string) error { return errors.New("launcher failed") })
	if response.OK || response.Error != "launcher failed" {
		t.Fatalf("lost launcher failure: %+v", response)
	}

	response = Handle(strings.NewReader(message), 1000, 1000, root, func(target string) error {
		if target != "https://example.com" {
			t.Fatal(target)
		}

		return nil
	})
	if !response.OK {
		t.Fatal(response.Error)
	}
}

func TestLaunchPreservesSingleArgumentAndExitStatus(t *testing.T) {
	// Fake the desktop utility, never open a real host application.
	bin := t.TempDir()
	output := filepath.Join(bin, "argument")
	script := "#!/bin/sh\n[ \"$#\" = 1 ] || exit 7\nprintf '%s' \"$1\" > \"$OPEN_TEST_OUTPUT\"\nexit \"$OPEN_TEST_STATUS\"\n"
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("OPEN_TEST_OUTPUT", output)
	t.Setenv("OPEN_TEST_STATUS", "0")
	target := fmt.Sprintf("https://example.com/?value=$(touch %s)", filepath.Join(bin, "must-not-exist"))
	if err := Launch(target); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(output)
	if err != nil || string(data) != target {
		t.Fatalf("argument changed: %q, %v", data, err)
	}

	if _, err := os.Stat(filepath.Join(bin, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatal("URI evaluated as shell code")
	}
	t.Setenv("OPEN_TEST_STATUS", "4")
	if err := Launch(target); err == nil {
		t.Fatal("lost immediate exit failure")
	}
}

func TestServeStopsWhenContextCanceled(t *testing.T) {
	// Serve checks for xdg-open, but this test never launches it.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	socket := filepath.Join(t.TempDir(), "socket")
	project := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, socket, project)
	}()

	select {
	case err := <-done:
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skip("sandbox prohibits Unix socket binding")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}

	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatalf("server did not remove its socket: %v", err)
	}
}
