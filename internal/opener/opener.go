// Package opener implements the bounded JSON protocol used by the guest shim.
package opener

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"workshop-desktop/internal/logging"
)

const MaxRequest = 16 * 1024

type Request struct {
	URI string `json:"uri"`
}

type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Handle is separated from socket credentials and the launcher for protocol tests.
func Handle(reader io.Reader, uid, expectedUID int, project string, launch func(string) error) Response {
	if uid != expectedUID {
		return Response{Error: "host open requests must come from your user"}
	}

	line, err := bufio.NewReader(io.LimitReader(reader, MaxRequest+1)).ReadBytes('\n')
	if err != nil || len(line) > MaxRequest {
		return Response{Error: "invalid or oversized open request"}
	}

	var request Request

	if err := json.Unmarshal(line, &request); err != nil {
		return Response{Error: err.Error()}
	}

	target, err := Target(request.URI, project)
	if err != nil {
		return Response{Error: err.Error()}
	}

	if err := launch(target); err != nil {
		return Response{Error: err.Error()}
	}

	return Response{OK: true}
}

func Serve(ctx context.Context, socket, project string) error {
	if _, err := exec.LookPath("xdg-open"); err != nil {
		return err
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return err
	}

	defer listener.Close()

	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}

	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()

	logging.Logger(ctx).Info("Host opener ready", "socket", socket)

	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			return err
		}

		go serveConnection(ctx, conn, project)
	}
}

func serveConnection(ctx context.Context, conn *net.UnixConn, project string) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	uid, err := peerUID(conn)
	var response Response
	if err != nil {
		response.Error = err.Error()
	} else {
		response = Handle(conn, uid, os.Getuid(), project, func(target string) error {
			return launch(logging.Logger(ctx), target)
		})
	}

	json.NewEncoder(conn).Encode(response)
}

func peerUID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}

	var credentials *syscall.Ucred
	var credentialErr error
	err = raw.Control(func(fd uintptr) {
		credentials, credentialErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return 0, err
	}

	if credentialErr != nil {
		return 0, credentialErr
	}

	return int(credentials.Uid), nil
}

// Launch reports immediate errors; an accepted desktop app may outlive the broker.
func Launch(target string) error {
	return launch(slog.Default(), target)
}

func launch(logger *slog.Logger, target string) error {
	cmd := exec.Command("xdg-open", target)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		done <- err
		if err != nil {
			logger.Error("host xdg-open failed", "error", err)
		}
	}()

	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		return nil
	}
}
