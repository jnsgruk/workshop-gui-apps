package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
		_ = duplicate.Close()
		t.Fatal("duplicate acquired lock")
	}

	other, err := p.Lock("pipewire")
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Close()
	_ = lock.Close()
	lock, err = p.Lock("dbus")
	if err != nil {
		t.Fatal("lock not released:", err)
	}
	_ = lock.Close()
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
		_ = lock.Close()
		t.Fatal("followed symlink lock")
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClearSocket(file); err == nil {
		t.Fatal("removed regular file")
	}

	if err := ClearSocket(filepath.Join(root, "absent")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadResolvesProjectAndSecuresEndpointDirectories(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "project")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(directory, filepath.Join(parent, "linked")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent)

	if _, err := Load("linked"); err == nil || !strings.Contains(err.Error(), "launch the workshop first") {
		t.Fatalf("missing project lock: %v", err)
	}
	lock := filepath.Join(directory, ".workshop.lock")
	for _, id := range []string{"", "../invalid", "invalid project", "two\nlines"} {
		if err := os.WriteFile(lock, []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load("linked"); err == nil || !strings.Contains(err.Error(), "invalid project ID") {
			t.Fatalf("invalid project ID %q: %v", id, err)
		}
	}

	prefix := fmt.Sprintf("workshop-desktop-%d-", os.Getuid())
	root, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	id := strings.TrimPrefix(filepath.Base(root), prefix)
	if err := os.WriteFile(lock, []byte("  "+id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Load("linked")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Project{Directory: directory, ID: id, Root: root}); p != want {
		t.Fatalf("loaded %+v, want %+v", p, want)
	}
	for _, path := range []string{root, filepath.Join(root, "session"), filepath.Join(root, "pipewire")} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("endpoint directory %q has mode %v", path, info.Mode())
		}
	}
}

func startProcessChild(ctx context.Context, t *testing.T, mode string, configure func(*exec.Cmd)) *Process {
	t.Helper()
	p, err := Start(ctx, func(cmd *exec.Cmd) {
		cmd.Env = append(os.Environ(), "WORKSHOP_TEST_CHILD="+mode)
		if configure != nil {
			configure(cmd)
		}
	}, os.Args[0], "-test.run=^TestProcessChild$", "--", "process-child")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	return p
}

func TestProcessObservesParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	p := startProcessChild(ctx, t, "wait", func(cmd *exec.Cmd) { cmd.Stdout = writer })

	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("child did not signal readiness: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not signal readiness")
	}

	cancel()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("child continued running after cancellation of the parent context")
	}
}

func TestProcessUnexpectedSuccess(t *testing.T) {
	p := startProcessChild(t.Context(), t, "success", nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := p.Wait(ctx); err == nil || !strings.Contains(err.Error(), "exited unexpectedly") {
		t.Fatalf("wanted unexpected successful exit, got %v", err)
	}
}

func TestWaitSocketRequiresSocket(t *testing.T) {
	p := startProcessChild(t.Context(), t, "wait", nil)
	path := filepath.Join(t.TempDir(), "bus")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Give WaitSocket time to observe the regular file before the caller deadline.
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	if err := p.WaitSocket(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("regular file readiness: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("unix", path)
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("sandbox prohibits Unix socket binding")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = socket.Close() }()

	ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := p.WaitSocket(ctx, path); err != nil {
		t.Fatal(err)
	}
}

func TestWaitSocketReportsEarlyExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	child := startProcessChild(t.Context(), t, "failure", nil)
	if err := child.WaitSocket(ctx, filepath.Join(t.TempDir(), "missing")); err == nil ||
		!strings.Contains(err.Error(), "exited before readiness: exit status 4") {
		t.Fatalf("missing early exit status: %v", err)
	}
}

func TestProcessChild(t *testing.T) {
	if len(os.Args) != 4 || os.Args[2] != "--" || os.Args[3] != "process-child" {
		return
	}
	switch os.Getenv("WORKSHOP_TEST_CHILD") {
	case "output":
		_, _ = fmt.Fprint(os.Stdout, "child output\ntrailing output")
		fmt.Fprint(os.Stderr, "child diagnostic\ntrailing diagnostic")
		os.Exit(0) // Do not append the test runner's PASS line to stdout.
	case "wait":
		_, _ = fmt.Fprintln(os.Stdout, "ready")
		time.Sleep(30 * time.Second)
	case "success":
		os.Exit(0)
	case "failure":
		os.Exit(4)
	}
}

func TestProcessOutputUsesCommandLogger(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil)).With("handler", "test")
	ctx := logging.WithLogger(t.Context(), logger)
	p := startProcessChild(ctx, t, "output", nil)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}

	type record struct {
		Message string `json:"msg"`
		Level   string `json:"level"`
		Stream  string `json:"stream"`
		Handler string `json:"handler"`
		Process string `json:"process"`
	}
	want := map[string]record{
		"child output":        {Level: "INFO", Stream: "stdout"},
		"trailing output":     {Level: "INFO", Stream: "stdout"},
		"child diagnostic":    {Level: "WARN", Stream: "stderr"},
		"trailing diagnostic": {Level: "WARN", Stream: "stderr"},
	}
	decoder := json.NewDecoder(&output)
	for {
		var got record
		err := decoder.Decode(&got)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		expected, present := want[got.Message]
		if !present || got.Handler != "test" || got.Process != os.Args[0] || got.Level != expected.Level || got.Stream != expected.Stream {
			t.Fatalf("unexpected log record: %+v", got)
		}
		delete(want, got.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing log records: %v", want)
	}
}
