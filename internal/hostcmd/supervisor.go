package hostcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"workshop-desktop/internal/host"
)

type endpointReadyKey struct{}

func reportEndpointReady(ctx context.Context) {
	if ready, ok := ctx.Value(endpointReadyKey{}).(chan<- struct{}); ok {
		ready <- struct{}{}
	}
}

type bridgeState struct {
	ready   chan struct{}
	stopped chan struct{}
	err     error // Written before ready is closed.
}

// supervise takes ownership of the listener and project lock inherited from the launcher.
func supervise(ctx context.Context, project host.Project, workshop string) error {
	paths, err := supervisorPaths(project)
	if err != nil {
		return err
	}
	listenerFD := os.NewFile(3, "supervisor listener")
	listener, err := net.FileListener(listenerFD)
	_ = listenerFD.Close()
	if err != nil {
		return fmt.Errorf("open inherited supervisor listener: %w", err)
	}
	defer listener.Close()
	if listener.Addr().Network() != "unix" || listener.Addr().String() != paths.socket {
		return errors.New("invalid inherited supervisor listener")
	}

	syscall.CloseOnExec(4)
	lock := os.NewFile(4, "supervisor lock")
	defer lock.Close()
	session := desktopSession(paths.runtime)
	return runSupervisor(ctx, listener, session, func() { _ = host.ClearSocket(paths.socket) }, func(ctx context.Context, cancel context.CancelCauseFunc) error {
		watch, stopWatch := context.WithCancel(ctx)
		bus, err := validateSession(watch, paths.runtime, session, cancel)
		if err != nil {
			stopWatch()
			return err
		}
		defer bus.Close()
		defer stopWatch()
		return up(context.WithValue(ctx, workshopExecutableKey{}, workshop), project, []string{"apps"}, handlers)
	})
}

func runSupervisor(ctx context.Context, listener net.Listener, session launchSession, unlink func(), start func(context.Context, context.CancelCauseFunc) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	state := &bridgeState{ready: make(chan struct{}), stopped: make(chan struct{})}

	firstClient := make(chan struct{})
	served := make(chan struct{})
	go func() {
		defer close(served)
		state.serve(listener, session, ctx.Done(), cancel, sync.OnceFunc(func() { close(firstClient) }))
	}()

	timer := time.AfterFunc(bridgeStartupTimeout, func() {
		cancel(errors.New("timed out preparing the desktop bridge"))
	})
	defer timer.Stop()
	select {
	case <-firstClient:
	case <-ctx.Done():
	}
	endpoints := make(chan struct{}, len(handlers))
	work := make(chan error, 1)
	go func() {
		if ctx.Err() != nil {
			work <- context.Cause(ctx)
			return
		}
		work <- start(context.WithValue(ctx, endpointReadyKey{}, (chan<- struct{})(endpoints)), cancel)
	}()

	err, ready := waitBridge(ctx, endpoints, work)
	timer.Stop()
	state.err = err
	close(state.ready)

	if ready {
		err = <-work
	}
	cancel(err)
	_ = listener.Close()
	unlink()
	close(state.stopped)
	<-served
	return err
}

func waitBridge(ctx context.Context, endpoints <-chan struct{}, work <-chan error) (error, bool) {
	for range handlers {
		select {
		case <-endpoints:
		case err := <-work:
			return startupError(ctx, err), false
		}
	}
	select {
	case err := <-work:
		return startupError(ctx, err), false
	case <-ctx.Done():
		return startupError(ctx, <-work), false
	default:
		return nil, true
	}
}

func startupError(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err == nil {
		return errors.New("bridge exited before readiness")
	}
	return err
}

func (s *bridgeState) serve(listener net.Listener, session launchSession, canceled <-chan struct{}, cancel context.CancelCauseFunc, accepted func()) {
	var clients sync.WaitGroup
	defer clients.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		clients.Go(func() {
			defer conn.Close()
			s.handle(conn, session, canceled, cancel)
		})
		accepted()
	}
}

func (s *bridgeState) handle(conn net.Conn, session launchSession, canceled <-chan struct{}, cancel context.CancelCauseFunc) {
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var req bridgeRequest
	if err := json.NewDecoder(io.LimitReader(conn, 8192)).Decode(&req); err != nil {
		return
	}
	var reply bridgeReply
	switch {
	case req.Stop:
		cancel(context.Canceled)
		<-s.stopped
	case req.Session != session:
		reply.Error = "the desktop bridge belongs to a different graphical session"
	default:
		<-s.ready
		if s.err != nil {
			reply.Error = s.err.Error()
		} else {
			select {
			case <-canceled:
				reply.Error = "the desktop bridge has stopped; retry the launcher"
			default:
			}
		}
	}
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_ = json.NewEncoder(conn).Encode(reply)
}
