package hostcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

type handler struct {
	name string
	run  func(context.Context, host.Project) error
}

var handlers = []handler{
	{"dbus", runDBus},
	{"pipewire", runPipeWire},
}

func selectHandlers(value string) ([]handler, error) {
	var selected []handler
	seen := make(map[string]bool)
	for name := range strings.SplitSeq(value, ",") {
		name = strings.TrimSpace(name)
		var found bool
		for _, h := range handlers {
			if h.name == name {
				if !seen[name] {
					selected = append(selected, h)
					seen[name] = true
				}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown handler %q; choose from dbus, pipewire", name)
		}
	}
	return selected, nil
}

func lockHandlers(p host.Project, selected []handler) (func(), error) {
	var locks []*os.File
	unlock := func() {
		for _, lock := range locks {
			_ = lock.Close()
		}
	}
	for _, h := range selected {
		lock, err := p.Lock(h.name)
		if err != nil {
			unlock()
			return nil, err
		}
		locks = append(locks, lock)
	}
	return unlock, nil
}

// Keep the locks until all handlers finish cleanup, including after a sibling fails.
func runHandlers(ctx context.Context, p host.Project, selected []handler) error {
	unlock, err := lockHandlers(p, selected)
	if err != nil {
		return err
	}
	defer unlock()
	return serveHandlers(ctx, p, selected)
}

func serveHandlers(ctx context.Context, p host.Project, selected []handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(selected))
	for _, h := range selected {
		go func() {
			handlerCtx := logging.WithLogger(ctx, logging.Logger(ctx).With("handler", h.name))
			err := h.run(handlerCtx, p)
			if ctx.Err() != nil {
				// Child processes often return a signal error on cancellation.
				err = ctx.Err()
			} else if err == nil {
				err = errors.New("handler exited unexpectedly")
			}
			results <- fmt.Errorf("%s: %w", h.name, err)
		}()
	}

	var failure error
	for range selected {
		err := <-results
		if failure == nil && !errors.Is(err, context.Canceled) {
			failure = err
		}
		cancel()
	}
	if failure != nil {
		return failure
	}
	return ctx.Err()
}
