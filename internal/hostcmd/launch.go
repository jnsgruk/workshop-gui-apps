package hostcmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

const (
	bridgeStartupTimeout = 2 * time.Minute
	launchTimeout        = bridgeStartupTimeout + 5*time.Second
)

type bridgePaths struct {
	runtime string
	socket  string
	log     string
}

func supervisorPaths(p host.Project) (bridgePaths, error) {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtime) {
		return bridgePaths{}, errors.New("XDG_RUNTIME_DIR must be set to the graphical session's runtime directory")
	}
	// Keep the control address within the Unix socket path limit even for long project IDs.
	hash := sha256.Sum256([]byte(p.ID))
	dir := filepath.Join(runtime, fmt.Sprintf("workshop-desktop-%x", hash[:8]))
	if err := host.PrivateDirectory(dir); err != nil {
		return bridgePaths{}, err
	}
	return bridgePaths{runtime, filepath.Join(dir, "control"), filepath.Join(dir, "bridge.log")}, nil
}

type bridgeRequest struct {
	Session launchSession
	Stop    bool
}

type bridgeReply struct {
	Error string
}

// Launch ensures the shared bridge is ready and replaces the wrapper with the app command.
func Launch(project string, command []string, notify bool, stdout, stderr io.Writer, verbosity logging.Verbosity) (int, error) {
	cmd := exec.Command(command[0], command[1:]...)
	ctx := logging.WithCLIOutput(context.Background(), "host launch", verbosity, stdout, stderr)
	code := 1
	previousMask := syscall.Umask(0o077)
	defer syscall.Umask(previousMask)
	err := host.Run(ctx, project, false, func(ctx context.Context, p host.Project) error {
		if cmd.Err != nil {
			code = 127
			return fmt.Errorf("cannot find command %q: %w", command[0], cmd.Err)
		}
		var workshop string
		if filepath.IsAbs(cmd.Path) && filepath.Base(cmd.Path) == "workshop" {
			workshop = cmd.Path
		}
		if err := ensureSupervisor(ctx, p, workshop); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Chdir(p.Directory); err != nil {
			return err
		}
		syscall.Umask(previousMask)
		if err := syscall.Exec(cmd.Path, cmd.Args, os.Environ()); err != nil {
			code = 126
			return fmt.Errorf("cannot run command %q: %w", command[0], err)
		}
		return nil
	})
	if err != nil && notify {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		notifyLaunchFailure(ctx, err.Error())
	}
	if err == nil {
		return 0, nil
	}
	return code, err
}

func ensureSupervisor(ctx context.Context, project host.Project, workshop string) error {
	paths, err := supervisorPaths(project)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()

	conn, err := connectSupervisor(ctx, project, paths, workshop)
	if err == nil {
		defer conn.Close()
		err = requestBridge(ctx, conn, bridgeRequest{Session: desktopSession(paths.runtime)})
	}
	if err != nil {
		return fmt.Errorf("cannot start the desktop bridge (log: %s): %w", paths.log, err)
	}
	return nil
}

func dialSupervisor(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}

func supervisorAbsent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

func connectSupervisor(ctx context.Context, project host.Project, paths bridgePaths, workshop string) (net.Conn, error) {
	for {
		conn, err := dialSupervisor(ctx, paths.socket)
		if !supervisorAbsent(err) {
			return conn, err
		}
		lock, err := project.Lock("supervisor")
		if err == nil {
			defer lock.Close()
			// A supervisor may have started and stopped between the dial and lock.
			conn, err := dialSupervisor(ctx, paths.socket)
			if !supervisorAbsent(err) {
				return conn, err
			}
			return startSupervisor(ctx, project, paths, lock, workshop)
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
}

func startSupervisor(ctx context.Context, project host.Project, paths bridgePaths, lock *os.File, workshop string) (net.Conn, error) {
	if err := host.ClearSocket(paths.socket); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: paths.socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	listenerFD, err := listener.File()
	if err != nil {
		return nil, err
	}
	defer listenerFD.Close()

	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logFD, err := syscall.Open(paths.log, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	log := os.NewFile(uintptr(logFD), paths.log)
	defer log.Close()

	// Queue the initial caller before starting the child, including on fast failures.
	conn, err := dialSupervisor(ctx, paths.socket)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, "host", "supervise", "--verbose")
	if workshop != "" {
		cmd.Args = append(cmd.Args, "--workshop-executable", workshop)
	}
	cmd.Dir = project.Directory
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.ExtraFiles = []*os.File{listenerFD, lock}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	go func() { _ = cmd.Wait() }()
	return conn, nil
}

func requestBridge(ctx context.Context, conn net.Conn, request bridgeRequest) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return err
	}
	var reply bridgeReply
	err := json.NewDecoder(io.LimitReader(conn, 8192)).Decode(&reply)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("lost the supervisor connection: %w", err)
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}

func stopSupervisor(ctx context.Context, p host.Project) error {
	paths, err := supervisorPaths(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	conn, err := dialSupervisor(ctx, paths.socket)
	if supervisorAbsent(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	return requestBridge(ctx, conn, bridgeRequest{Stop: true})
}
