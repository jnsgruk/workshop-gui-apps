package host

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"workshop-desktop/internal/logging"
)

type Process struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
	cancel  context.CancelFunc
}

func Start(ctx context.Context, configure func(*exec.Cmd), name string, args ...string) (*Process, error) {
	ctx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(ctx, name, args...)
	logger := logging.Logger(ctx).With("process", name)
	stdout := &logging.Writer{Logger: logger.With("stream", "stdout"), Level: slog.LevelInfo}
	stderr := &logging.Writer{Logger: logger.With("stream", "stderr"), Level: slog.LevelWarn}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second

	if configure != nil {
		configure(cmd)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	p := &Process{command: cmd, done: make(chan struct{}), cancel: cancel}

	go func() {
		p.err = cmd.Wait()
		stdout.Flush()
		stderr.Flush()
		close(p.done)
	}()
	return p, nil
}

func (p *Process) Stop() {
	p.cancel()
	<-p.done
}

func (p *Process) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		if p.err != nil {
			return p.err
		}

		return fmt.Errorf("%s exited unexpectedly", p.command.Path)
	}
}

func (p *Process) WaitSocket(ctx context.Context, path string) error {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return fmt.Errorf("%s exited before readiness: %v", p.command.Path, p.err)
		case <-timer.C:
			return fmt.Errorf("timed out waiting for %s", path)
		case <-tick.C:
			info, err := os.Lstat(path)
			if err == nil && info.Mode()&os.ModeSocket != 0 {
				return nil
			}
		}
	}
}
