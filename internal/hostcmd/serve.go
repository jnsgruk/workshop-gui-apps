package hostcmd

import (
	"context"
	"errors"
	"fmt"
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
	{"open", runOpen},
}

func selectHandlers(value string) ([]handler, error) {
	var selected []handler
	seen := make(map[string]bool)
	for _, name := range strings.Split(value, ",") {
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
			return nil, fmt.Errorf("unknown handler %q; choose from dbus, pipewire, open", name)
		}
	}
	return selected, nil
}

// Acquire every selected lock before starting any handler. Keep the locks until
// all handlers finish cleanup, including after a sibling fails.
func runHandlers(ctx context.Context, p host.Project, selected []handler) error {
	for _, h := range selected {
		lock, err := p.Lock(h.name)
		if err != nil {
			return err
		}
		defer lock.Close()
	}

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
