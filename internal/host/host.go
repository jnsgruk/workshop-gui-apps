// Package host provides the Linux host helpers' project paths and lifecycle.
package host

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

func Load(directory string) (Project, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return Project{}, err
	}

	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return Project{}, err
	}

	data, err := os.ReadFile(filepath.Join(directory, ".workshop.lock"))
	if err != nil {
		return Project{}, fmt.Errorf("launch the workshop first: %w", err)
	}

	id := strings.TrimSpace(string(data))
	if !projectIDPattern.MatchString(id) {
		return Project{}, errors.New("invalid project ID in .workshop.lock")
	}

	p := Project{
		Directory: directory,
		ID:        id,
		Root:      fmt.Sprintf("/tmp/workshop-desktop-%d-%s", os.Getuid(), id),
	}

	for _, part := range []string{"", "session", "pipewire", "open"} {
		if err := privateDirectory(filepath.Join(p.Root, part)); err != nil {
			return Project{}, err
		}
	}

	return p, nil
}

func privateDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return fmt.Errorf("not a directory owned by you: %s", path)
	}

	return os.Chmod(path, 0700)
}

// Lock is per capability: serving one capability does not require the others.
// connect takes all three locks before changing mounts.
func (p Project) Lock(capability string) (*os.File, error) {
	fd, err := syscall.Open(filepath.Join(p.Root, capability+".lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(fd), capability+".lock")
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s helper already running; stop it before reconnecting", capability)
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

// Run supplies common flags, signal handling, private paths and error reporting.
func Run(capability string, args []string, stderr io.Writer, register func(*flag.FlagSet), run func(context.Context, Project) error) int {
	logger := logging.New(stderr).With("command", capability)
	flags := flag.NewFlagSet("workshop-desktop host "+capability, flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("project", ".", "workshop project directory")
	if register != nil {
		register(flags)
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		logger.Error("unexpected positional arguments")
		return 2
	}

	if os.Getuid() == 0 {
		logger.Error("run as your desktop user, without sudo")
		return 1
	}
	syscall.Umask(0077)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx = logging.WithLogger(ctx, logger)

	err := func() error {
		p, err := Load(*directory)
		if err != nil {
			return err
		}

		return run(ctx, p)
	}()

	stop()

	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("host command failed", "error", err)
		return 1
	}
	return 0
}
