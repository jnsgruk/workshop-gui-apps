// xdg-open serves guest opening requests; it never replaces the host utility.
package hostcmd

import (
	"context"
	"os"
	"path/filepath"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/opener"
)

func runOpen(ctx context.Context, p host.Project) error {
	socket := filepath.Join(p.Root, "open", "socket")
	if err := host.ClearSocket(socket); err != nil {
		return err
	}

	defer os.Remove(socket)

	return opener.Serve(ctx, socket, p.Directory)
}
