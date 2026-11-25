package hostcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

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
		index := slices.IndexFunc(handlers, func(h handler) bool { return h.name == name })
		if index < 0 {
			return nil, fmt.Errorf("unknown handler %q; choose from dbus, pipewire", name)
		}
		if seen[name] {
			continue
		}
		selected = append(selected, handlers[index])
		seen[name] = true
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
	group, handlerGroupCtx := errgroup.WithContext(ctx)
	for _, h := range selected {
		group.Go(func() error {
			handlerCtx := logging.WithLogger(handlerGroupCtx, logging.Logger(handlerGroupCtx).With("handler", h.name))
			err := h.run(handlerCtx, p)
			if handlerGroupCtx.Err() != nil {
				// Child processes often return a signal error on cancellation.
				err = handlerGroupCtx.Err()
			} else if err == nil {
				err = errors.New("handler exited unexpectedly")
			}
			if errors.Is(err, context.Canceled) {
				// A handler can return cancellation before the group context is done.
				cancel()
				return nil
			}
			return fmt.Errorf("%s: %w", h.name, err)
		})
	}

	if err := group.Wait(); err != nil {
		return err
	}
	return handlerGroupCtx.Err()
}
