// pw-container publishes the host PipeWire security context for guest audio.
package hostcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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

	defer os.Remove(socket)

	properties, _ := json.Marshal(map[string]string{
		"pipewire.sec.app-id":      "org.mozilla.firefox",
		"pipewire.sec.instance-id": "workshop-" + p.ID,
	})

	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}

	defer reader.Close()
	defer writer.Close()

	// Some pw-container versions do not flush their initial path on a pipe.
	process, err := host.Start(ctx, func(cmd *exec.Cmd) { cmd.Stdout = writer }, "stdbuf", "-oL", "pw-container", "-P", string(properties))
	if err != nil {
		return err
	}

	writer.Close()
	defer process.Stop()

	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(reader)

		if scanner.Scan() {
			lines <- scanner.Text()
		} else {
			lines <- ""
		}

		// The first line is the socket protocol; subsequent lines are diagnostics.
		for scanner.Scan() {
			logging.Logger(ctx).Info(scanner.Text(), "process", "pw-container", "stream", "stdout")
		}
	}()

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("pw-container did not publish a socket within 10 seconds")
	case line := <-lines:
		if err := pipewire.Publish(line, socket); err != nil {
			return err
		}
	}

	logging.Logger(ctx).Info("PipeWire ready", "socket", socket)
	return process.Wait(ctx)
}
