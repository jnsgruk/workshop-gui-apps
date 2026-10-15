package pipewire

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPublishRejectsNonSockets(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"", "relative", "new socket: relative", file} {
		if err := Publish(line, filepath.Join(root, "destination")); err == nil {
			t.Fatalf("accepted %q", line)
		}
	}
}

func TestPublishPreservesSocketInode(t *testing.T) {
	root := t.TempDir()
	for _, prefix := range []string{"", "new socket: "} {
		source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: source, Net: "unix"})
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skip("sandbox prohibits Unix socket binding")
		}

		if err != nil {
			t.Fatal(err)
		}

		before, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}

		if err := Publish(prefix+source, destination); err != nil {
			t.Fatal(err)
		}

		after, err := os.Stat(destination)
		if err != nil {
			t.Fatal(err)
		}

		if !os.SameFile(before, after) {
			t.Fatal("socket inode changed")
		}
		listener.Close()
		os.Remove(destination)
	}
}
