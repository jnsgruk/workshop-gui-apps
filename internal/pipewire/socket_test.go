package pipewire

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPublishRejectsInvalidSources(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", "relative")
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("sandbox prohibits Unix socket binding")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	destination := filepath.Join(root, "destination")
	for _, line := range []string{"", "relative", "new socket: relative", file} {
		if err := Publish(line, destination); err == nil {
			t.Fatalf("accepted %q", line)
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			t.Fatalf("created destination for invalid source %q: %v", line, err)
		}
	}
	if _, err := os.Lstat("relative"); err != nil {
		t.Fatalf("removed relative socket: %v", err)
	}
}

func TestPublishPreservesSocketInodeAndConnectivity(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{{"path", ""}, {"prefixed", "new socket: "}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: source, Net: "unix"})
			if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
				t.Skip("sandbox prohibits Unix socket binding")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()

			before, err := os.Lstat(source)
			if err != nil {
				t.Fatal(err)
			}
			if err := Publish(tc.prefix+source, destination); err != nil {
				t.Fatal(err)
			}
			after, err := os.Lstat(destination)
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode()&os.ModeSocket == 0 || !os.SameFile(before, after) {
				t.Fatal("destination is not the original socket")
			}
			if _, err := os.Lstat(source); !os.IsNotExist(err) {
				t.Fatalf("original path still exists: %v", err)
			}

			deadline := time.Now().Add(5 * time.Second)
			if err := listener.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			client, err := net.DialTimeout("unix", destination, time.Until(deadline))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			server, err := listener.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			if err := server.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			if err := client.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}

			const message = "socket stayed connected"
			if _, err := io.WriteString(client, message); err != nil {
				t.Fatal(err)
			}
			received := make([]byte, len(message))
			if _, err := io.ReadFull(server, received); err != nil || string(received) != message {
				t.Fatalf("received %q, %v", received, err)
			}
		})
	}
}
