package pipewire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Publish supports pw-container versions that print a path or "new socket: PATH".
// Rename on the same filesystem preserves the socket inode and FD passing.
func Publish(line, destination string) error {
	source := strings.TrimPrefix(strings.TrimSpace(line), "new socket: ")
	if !filepath.IsAbs(source) {
		return fmt.Errorf("unexpected pw-container output: %q", line)
	}

	info, err := os.Lstat(source)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSocket == 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return fmt.Errorf("not a socket owned by you: %s", source)
	}

	return os.Rename(source, destination)
}
