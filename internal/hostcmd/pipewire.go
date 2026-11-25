// pw-container publishes the host PipeWire security context for guest audio.
package hostcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
	"workshop-desktop/internal/pipewire"
)

func runPipeWire(ctx context.Context, p host.Project) error {
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		return fmt.Errorf("XDG_RUNTIME_DIR is unset; run from your graphical session")
	}

	socket := filepath.Join(p.Root, "pipewire", "pipewire-0")
	if err := host.ClearSocket(socket); err != nil {
		return err
	}

	defer func() { _ = os.Remove(socket) }()

	properties, _ := json.Marshal(map[string]string{
		"pipewire.sec.app-id":      "org.mozilla.firefox",
		"pipewire.sec.instance-id": "workshop-" + p.ID,
	})

	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}

	defer func() { _ = writer.Close() }()

	// Some pw-container versions do not flush their initial path on a pipe.
	process, err := host.Start(ctx, func(cmd *exec.Cmd) { cmd.Stdout = writer }, "stdbuf", "-oL", "pw-container", "-P", string(properties))
	if err != nil {
		_ = reader.Close()
		return err
	}

	_ = writer.Close()
	defer process.Stop()
	startupError := func(err error) error {
		process.Stop()
		return process.WithErrorDetail(err)
	}

	startup := make(chan pipeWireAnnouncement, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		readPipeWireOutput(ctx, reader, startup)
	}()
	defer func() {
		_ = reader.Close()
		<-readerDone
	}()

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	var result pipeWireAnnouncement
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return startupError(fmt.Errorf("pw-container did not publish a socket within 10 seconds"))
	case result = <-startup:
	}
	if result.err != nil {
		return startupError(result.err)
	}
	if err := pipewire.Publish(result.line, socket); err != nil {
		return startupError(err)
	}

	logging.Progress(ctx, "PipeWire ready.", "socket", socket)
	return process.Wait(ctx)
}

type pipeWireAnnouncement struct {
	line string
	err  error
}

// readPipeWireOutput sends the socket announcement, then drains stdout as diagnostics.
func readPipeWireOutput(ctx context.Context, reader io.Reader, startup chan<- pipeWireAnnouncement) {
	scanner := bufio.NewScanner(reader)
	if !scanner.Scan() {
		err := scanner.Err()
		if err == nil {
			startup <- pipeWireAnnouncement{err: fmt.Errorf("pw-container closed stdout before publishing a socket: %w", io.EOF)}
			return
		}
		startup <- pipeWireAnnouncement{err: fmt.Errorf("read pw-container socket announcement: %w", err)}
		return
	}
	startup <- pipeWireAnnouncement{line: scanner.Text()}

	logger := logging.Logger(ctx).With("process", "pw-container", "stream", "stdout")
	for scanner.Scan() {
		logger.InfoContext(ctx, scanner.Text())
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil && !errors.Is(err, os.ErrClosed) {
		logger.WarnContext(ctx, "cannot read pw-container diagnostics", "error", err)
		// Keep draining stdout if scanning stopped at an oversized line.
		_, _ = io.Copy(io.Discard, reader)
	}
}
