package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workshop-desktop/internal/logging"
)

func TestLocksPreventDuplicateServersAndAllowOtherCapabilities(t *testing.T) {
	p := Project{Root: t.TempDir()}
	lock, err := p.Lock("dbus")
	if err != nil {
		t.Fatal(err)
	}

	if duplicate, err := p.Lock("dbus"); err == nil {
		duplicate.Close()
		t.Fatal("duplicate acquired lock")
	}

	other, err := p.Lock("open")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	lock.Close()
	lock, err = p.Lock("dbus")
	if err != nil {
		t.Fatal("lock not released:", err)
	}
	lock.Close()
}

func TestPrivatePathsAndSocketCleanupRejectSymlinksAndFiles(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	if err := privateDirectory(link); err == nil {
		t.Fatal("accepted symlink directory")
	}

	p := Project{Root: root}
	if err := os.Symlink(filepath.Join(root, "target"), filepath.Join(root, "dbus.lock")); err != nil {
		t.Fatal(err)
	}

	if lock, err := p.Lock("dbus"); err == nil {
		lock.Close()
		t.Fatal("followed symlink lock")
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := ClearSocket(file); err == nil {
		t.Fatal("removed regular file")
	}

	if err := ClearSocket(filepath.Join(root, "absent")); err != nil {
		t.Fatal(err)
	}
}

func TestProcessCancellationAndEarlyExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := Start(ctx, func(c *exec.Cmd) { c.Env = append(os.Environ(), "WORKSHOP_TEST_CHILD=1") }, os.Args[0], "-test.run=^TestProcessChild$")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	finished := make(chan struct{})
	go func() { p.Stop(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not stop")
	}

	p, err = Start(context.Background(), nil, os.Args[0], "-test.run=^TestProcessChild$")
	if err != nil {
		t.Fatal(err)
	}

	defer p.Stop()
	if err := p.Wait(context.Background()); err == nil {
		t.Fatal("accepted unexpected successful exit")
	}
}

func TestProcessChild(t *testing.T) {
	if os.Getenv("WORKSHOP_TEST_CHILD") == "output" {
		fmt.Fprint(os.Stdout, "child output\ntrailing output")
		fmt.Fprint(os.Stderr, "child diagnostic\n")
	}
	if os.Getenv("WORKSHOP_TEST_CHILD") == "1" {
		time.Sleep(30 * time.Second)
	}
}

func TestProcessOutputUsesCommandLogger(t *testing.T) {
	var output bytes.Buffer
	ctx := logging.WithLogger(context.Background(), logging.New(&output).With("handler", "test"))
	p, err := Start(ctx, func(c *exec.Cmd) {
		c.Env = append(os.Environ(), "WORKSHOP_TEST_CHILD=output")
	}, os.Args[0], "-test.run=^TestProcessChild$")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
	for _, want := range []string{"child output", "trailing output", "child diagnostic", "handler=test", "stream=stdout", "stream=stderr", "level=WARN"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("missing %q in logs: %s", want, output.String())
		}
	}
}
