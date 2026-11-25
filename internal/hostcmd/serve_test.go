package hostcmd

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"workshop-desktop/internal/host"
)

func TestSelectHandlers(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []string
	}{
		{"pipewire,dbus", []string{"pipewire", "dbus"}},
		{"pipewire", []string{"pipewire"}},
		{"dbus,pipewire", []string{"dbus", "pipewire"}},
		{" dbus , dbus,pipewire ", []string{"dbus", "pipewire"}},
	} {
		t.Run(tc.value, func(t *testing.T) {
			selected, err := selectHandlers(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, h := range selected {
				names = append(names, h.name)
			}
			if !slices.Equal(names, tc.want) {
				t.Fatalf("got %v, want %v", names, tc.want)
			}
		})
	}
	for _, value := range []string{"", "unknown", "dbus,", ",dbus", "dbus,,pipewire", "open", "connect", "serve"} {
		if _, err := selectHandlers(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestServeAcquiresAllLocksBeforeStarting(t *testing.T) {
	p := host.Project{Root: t.TempDir()}
	lock, err := p.Lock("pipewire")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	started := make(chan struct{}, 2)
	run := func(context.Context, host.Project) error {
		started <- struct{}{}
		return nil
	}
	if err := runHandlers(t.Context(), p, []handler{{"dbus", run}, {"pipewire", run}}); err == nil {
		t.Fatal("ignored lock conflict")
	}
	if len(started) != 0 {
		t.Fatal("started a handler despite lock conflict")
	}
	// A partial lock acquisition must be rolled back.
	other, err := p.Lock("dbus")
	if err != nil {
		t.Fatal("leaked lock:", err)
	}
	_ = other.Close()
}

func TestServeFailureCancelsAndWaitsForSiblingCleanup(t *testing.T) {
	for _, unexpectedExit := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "unexpected exit"}[unexpectedExit], func(t *testing.T) {
			p := host.Project{Root: t.TempDir()}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			started := make(chan struct{})
			cleaned := make(chan struct{})
			failure := errors.New("startup failed")
			err := runHandlers(ctx, p, []handler{
				{"dbus", func(ctx context.Context, _ host.Project) error {
					close(started)
					<-ctx.Done()
					// Locks must remain held until cleanup finishes.
					if lock, err := p.Lock("dbus"); err == nil {
						_ = lock.Close()
						t.Error("released lock before cleanup")
					}
					close(cleaned)
					return ctx.Err()
				}},
				{"pipewire", func(ctx context.Context, _ host.Project) error {
					select {
					case <-started:
					case <-ctx.Done():
						return ctx.Err()
					}
					if unexpectedExit {
						return nil
					}
					return failure
				}},
			})
			if err == nil || (!unexpectedExit && !errors.Is(err, failure)) {
				t.Fatalf("lost handler failure: %v", err)
			}
			if unexpectedExit && (errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "pipewire: handler exited unexpectedly")) {
				t.Fatalf("unexpected exit was not reported as a handler failure: %v", err)
			}
			select {
			case <-cleaned:
			default:
				t.Fatal("returned before sibling cleanup")
			}
			for _, name := range []string{"dbus", "pipewire"} {
				lock, err := p.Lock(name)
				if err != nil {
					t.Fatal("leaked lock:", err)
				}
				_ = lock.Close()
			}
		})
	}
}

func TestServeCancellationOnlyLocksSelectedHandlers(t *testing.T) {
	p := host.Project{Root: t.TempDir()}
	// An unselected handler may already be running.
	lock, err := p.Lock("pipewire")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runHandlers(ctx, p, []handler{{"dbus", func(ctx context.Context, _ host.Project) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}})
	}()
	select {
	case <-started:
	case err := <-result:
		t.Fatalf("failed before starting: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	if duplicate, err := p.Lock("dbus"); err == nil {
		_ = duplicate.Close()
		t.Fatal("selected handler was not locked")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected cancellation result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handlers did not stop")
	}
}

func TestServeHandlerCancellationStopsSiblings(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := serveHandlers(ctx, host.Project{}, []handler{
		{"dbus", func(context.Context, host.Project) error {
			return context.Canceled
		}},
		{"pipewire", func(ctx context.Context, _ host.Project) error {
			<-ctx.Done()
			return ctx.Err()
		}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("serve returned %v, want cancellation", err)
	}
}
