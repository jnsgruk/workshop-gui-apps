package hostcmd

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSupervisorSharesReadinessAndStops(t *testing.T) {
	allowPipeWire := make(chan struct{})
	started := make(chan struct{})
	socket, session, done := testSupervisor(t, func(ctx context.Context, _ context.CancelCauseFunc) error {
		reportEndpointReady(ctx)
		close(started)
		select {
		case <-allowPipeWire:
			reportEndpointReady(ctx)
		case <-ctx.Done():
			return ctx.Err()
		}
		<-ctx.Done()
		return ctx.Err()
	})

	first := testBridgeRequest(t, socket, bridgeRequest{Session: session})
	<-started
	second := testBridgeRequest(t, socket, bridgeRequest{Session: session})
	otherSession := session
	otherSession.ID = "other-login"
	if err := <-testBridgeRequest(t, socket, bridgeRequest{Session: otherSession}); err == nil ||
		!strings.Contains(err.Error(), "different graphical session") {
		t.Fatalf("expected session conflict, got %v", err)
	}

	select {
	case err := <-first:
		t.Fatalf("first launcher proceeded before both endpoints were ready: %v", err)
	case err := <-second:
		t.Fatalf("second launcher proceeded before both endpoints were ready: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	close(allowPipeWire)
	for _, result := range []<-chan error{first, second} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-testBridgeRequest(t, socket, bridgeRequest{Stop: true}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected explicit stop, got %v", err)
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected socket cleanup, got %v", err)
	}
}

func TestSupervisorReportsStartupFailure(t *testing.T) {
	want := errors.New("test PipeWire startup failure")
	socket, session, done := testSupervisor(t, func(ctx context.Context, _ context.CancelCauseFunc) error {
		reportEndpointReady(ctx)
		return want
	})
	if err := <-testBridgeRequest(t, socket, bridgeRequest{Session: session}); err == nil || err.Error() != want.Error() {
		t.Fatalf("launcher got %v, want %v", err, want)
	}
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("supervisor got %v, want %v", err, want)
	}
}

func testSupervisor(t *testing.T, start func(context.Context, context.CancelCauseFunc) error) (string, launchSession, <-chan error) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "control")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	session := launchSession{ID: "test-login"}
	done := make(chan error, 1)
	go func() {
		done <- runSupervisor(ctx, listener, session, func() {}, start)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return socket, session, done
}

func testBridgeRequest(t *testing.T, socket string, request bridgeRequest) <-chan error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	conn, err := dialSupervisor(ctx, socket)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		defer cancel()
		defer conn.Close()
		result <- requestBridge(ctx, conn, request)
	}()
	return result
}
