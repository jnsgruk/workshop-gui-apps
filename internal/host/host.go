// Package host provides the Linux host helpers' project paths and lifecycle.
package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"workshop-desktop/internal/logging"
)

var projectIDPattern = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

type Project struct {
	Directory string
	ID        string
	Root      string
}

// loadDirectory resolves the checkout path used to identify launcher ownership.
func loadDirectory(directory string) (Project, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return Project{}, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return Project{}, err
	}
	return Project{Directory: directory}, nil
}

func Load(directory string) (Project, error) {
	p, err := loadDirectory(directory)
	if err != nil {
		return Project{}, err
	}
	directory = p.Directory

	data, err := os.ReadFile(filepath.Join(directory, ".workshop.lock"))
	if err != nil {
		return Project{}, fmt.Errorf("launch the workshop first: %w", err)
	}

	id := strings.TrimSpace(string(data))
	if !projectIDPattern.MatchString(id) {
		return Project{}, errors.New("invalid project ID in .workshop.lock")
	}

	p.ID = id
	p.Root = fmt.Sprintf("/tmp/workshop-desktop-%d-%s", os.Getuid(), id)

	for _, part := range []string{"", "session", "pipewire"} {
		if err := privateDirectory(filepath.Join(p.Root, part)); err != nil {
			return Project{}, err
		}
	}

	return p, nil
}

func privateDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return fmt.Errorf("not a directory owned by you: %s", path)
	}

	return os.Chmod(path, 0o700)
}

// Lock is per capability: serving one capability does not require the others.
// connect takes both locks before changing mounts.
func (p Project) Lock(capability string) (*os.File, error) {
	fd, err := syscall.Open(filepath.Join(p.Root, capability+".lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(fd), capability+".lock")
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%s helper already running; stop it before reconnecting", capability)
		}
		return nil, fmt.Errorf("lock %s capability: %w", capability, err)
	}

	return f, nil
}

// ClearSocket only removes stale sockets, never arbitrary files.
func ClearSocket(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket: %s", path)
	}

	return os.Remove(path)
}

// Run supplies signal handling and loads the project for a host operation.
// directoryOnly allows launcher removal after the workshop was removed.
func Run(ctx context.Context, directory string, directoryOnly bool, run func(context.Context, Project) error) error {
	if os.Getuid() == 0 {
		return errors.New("run as your desktop user without sudo")
	}
	syscall.Umask(0o077)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	load := Load
	if directoryOnly {
		load = loadDirectory
	}
	p, err := load(directory)
	if err != nil {
		return err
	}
	err = run(ctx, p)
	if ctx.Err() != nil {
		logging.Logger(ctx).InfoContext(ctx, "Host command canceled.", "cause", context.Cause(ctx))
		if errors.Is(err, ctx.Err()) {
			return nil
		}
	}
	return err
}
