package hostcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

type endpointLogs chan string

func (logs endpointLogs) Write(data []byte) (int, error) {
	var record struct {
		Message string `json:"msg"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return 0, err
	}
	logs <- record.Message
	return len(data), nil
}

type endpointRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	logs   endpointLogs
	err    error
}

func startEndpoint(t *testing.T, project host.Project, serve func(context.Context, host.Project) error) *endpointRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	run := &endpointRun{cancel: cancel, done: make(chan struct{}), logs: make(endpointLogs, 32)}
	ctx = logging.WithLogger(ctx, slog.New(slog.NewJSONHandler(run.logs, nil)))
	go func() {
		run.err = serve(ctx, project)
		close(run.done)
	}()
	t.Cleanup(func() {
		cancel()
		_ = run.wait(t)
	})
	return run
}

func (run *endpointRun) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-run.done:
		return run.err
	case <-time.After(5 * time.Second):
		t.Fatal("endpoint helper failed to stop")
		return nil
	}
}

func (run *endpointRun) waitForLogs(t *testing.T, messages ...string) {
	t.Helper()
	want := make(map[string]bool, len(messages))
	for _, message := range messages {
		want[message] = true
	}
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for len(want) != 0 {
		select {
		case message := <-run.logs:
			delete(want, message)
		case <-run.done:
			t.Fatalf("helper exited before logging %v: %v", want, run.err)
		case <-timeout.C:
			t.Fatalf("helper did not log %v", want)
		}
	}
}

func endpointProject(t *testing.T) host.Project {
	t.Helper()
	// Keep the endpoints within the Unix socket path length limit.
	root, err := os.MkdirTemp("", "workshop-endpoints-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	for _, dir := range []string{"session", "pipewire", "bin"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(root, "bin")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"xdg-dbus-proxy", "stdbuf"} {
		script := `#!/bin/sh
exec "$ENDPOINT_TEST_BINARY" -test.run='^TestEndpointChild$' -- endpoint-child "${0##*/}" "$@"
`
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("ENDPOINT_TEST_BINARY", executable)
	t.Setenv("ENDPOINT_TEST_ROOT", root)
	t.Setenv("ENDPOINT_TEST_EXIT", "0")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(root, "upstream"))
	t.Setenv("XDG_RUNTIME_DIR", root)
	return host.Project{ID: "endpoint-123", Root: root, Directory: t.TempDir()}
}

func recordedEndpointArgs(t *testing.T, p host.Project) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.Root, "args"))
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	return args
}

func assertEndpointSocket(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("published endpoint %q is not a socket: %v, %v", path, info, err)
	}
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestDBusProxyWaitsForSocketAndCleansUp(t *testing.T) {
	p := endpointProject(t)
	socket := filepath.Join(p.Root, "session", "bus")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("sandbox prohibits Unix socket binding")
	}
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	_ = stale.Close()

	run := startEndpoint(t, p, runDBus)
	run.waitForLogs(t, "proxy created a regular file")
	select {
	case msg := <-run.logs:
		t.Fatalf("logged %q before the proxy bound its socket", msg)
	case <-run.done:
		t.Fatalf("proxy stopped during startup: %v", run.err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(p.Root, "bind"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run.waitForLogs(t, "Session D-Bus ready")
	assertEndpointSocket(t, socket)

	want := []string{
		os.Getenv("DBUS_SESSION_BUS_ADDRESS"), socket, "--filter",
		"--talk=org.freedesktop.Notifications",
		"--talk=org.freedesktop.portal.Desktop",
		"--talk=org.freedesktop.portal.Documents",
		"--talk=org.freedesktop.secrets",
	}
	if args := recordedEndpointArgs(t, p); !reflect.DeepEqual(args, want) {
		t.Fatalf("wrong proxy arguments: %q; want %q", args, want)
	}
	run.cancel()
	if err := run.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("proxy stop: %v", err)
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatalf("proxy socket remains after stop: %v", err)
	}
}

func TestPipeWirePublishesFirstLineAndCleansUp(t *testing.T) {
	p := endpointProject(t)
	probe, err := net.Listen("unix", filepath.Join(p.Root, "probe"))
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("sandbox prohibits Unix socket binding")
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = probe.Close()
	run := startEndpoint(t, p, runPipeWire)
	run.waitForLogs(t, "PipeWire ready", "pw-container diagnostic")
	socket := filepath.Join(p.Root, "pipewire", "pipewire-0")
	assertEndpointSocket(t, socket)
	if _, err := os.Lstat(filepath.Join(p.Root, "source")); !os.IsNotExist(err) {
		t.Fatalf("source socket remains after publication: %v", err)
	}

	args := recordedEndpointArgs(t, p)
	if len(args) != 4 || !reflect.DeepEqual(args[:3], []string{"-oL", "pw-container", "-P"}) {
		t.Fatalf("wrong pw-container arguments: %q", args)
	}
	want := map[string]string{
		"pipewire.sec.app-id":      "org.mozilla.firefox",
		"pipewire.sec.instance-id": "workshop-" + p.ID,
	}
	var properties map[string]string
	if err := json.Unmarshal([]byte(args[3]), &properties); err != nil || !reflect.DeepEqual(properties, want) {
		t.Fatalf("wrong pw-container properties: %q, %v", properties, err)
	}
	run.cancel()
	if err := run.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("pw-container stop: %v", err)
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatalf("PipeWire socket remains after stop: %v", err)
	}
}

func TestEndpointsReportEarlyExit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		serve   func(context.Context, host.Project) error
		message string
	}{
		{"dbus", runDBus, "exited before readiness: exit status 4"},
		{"pipewire", runPipeWire, "unexpected pw-container output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := endpointProject(t)
			t.Setenv("ENDPOINT_TEST_EXIT", "4")
			err := startEndpoint(t, p, tc.serve).wait(t)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("missing early exit error: %v", err)
			}
		})
	}
}

// The helper implements only the external proxy/container socket protocols.
func TestEndpointChild(t *testing.T) {
	if len(os.Args) < 5 || os.Args[2] != "--" || os.Args[3] != "endpoint-child" {
		return
	}
	if os.Getenv("ENDPOINT_TEST_EXIT") == "4" {
		os.Exit(4)
	}

	root := os.Getenv("ENDPOINT_TEST_ROOT")
	if root == "" {
		t.Fatal("missing temporary endpoint root")
	}
	name, args := os.Args[4], os.Args[5:]
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "args"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "source")
	if name == "xdg-dbus-proxy" {
		if len(args) < 2 {
			t.Fatal("missing proxy socket argument")
		}
		socket = args[1]
		file, err := os.OpenFile(socket, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		fmt.Fprintln(os.Stderr, "proxy created a regular file")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Stat(filepath.Join(root, "bind")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("did not receive permission to bind the test socket")
			case <-tick.C:
			}
		}
		if err := os.Remove(socket); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if name == "stdbuf" {
		_, _ = fmt.Fprintln(os.Stdout, "new socket: "+socket)
		_, _ = fmt.Fprintln(os.Stdout, "pw-container diagnostic")
	}
	time.Sleep(30 * time.Second)
}
