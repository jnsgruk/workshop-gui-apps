package hostcmd

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"

	"workshop-desktop/internal/host"
)

type workshopRepair struct {
	start        bool
	mounts       []bridgeMount
	connectMount []bridgeMount
	interfaces   []bridgeInterface
}

func (r workshopRepair) needed() bool {
	return r.start || len(r.mounts) != 0 || len(r.interfaces) != 0
}

func up(ctx context.Context, p host.Project, names []string, selected []handler) error {
	if _, err := exec.LookPath("workshop"); err != nil {
		return err
	}
	// Mount repairs can restart the guest and affect both handlers.
	unlock, err := lockHandlers(p, handlers)
	if err != nil {
		return err
	}
	defer unlock()

	client := newWorkshopClient(workshopSocket())
	defer client.CloseIdleConnections()

	repairs := make([]workshopRepair, len(names))
	for i, name := range names {
		repair, err := client.inspect(ctx, p, name)
		if err != nil {
			return fmt.Errorf("inspect workshop %q: %w", name, err)
		}
		repairs[i] = repair
	}
	for i, repair := range repairs {
		if !repair.needed() {
			continue
		}
		name := names[i]
		if err := repair.apply(ctx, p, name); err != nil {
			return fmt.Errorf("repair workshop %q: %w", name, err)
		}
		verified, err := client.inspect(ctx, p, name)
		if err != nil {
			return fmt.Errorf("verify workshop %q: %w", name, err)
		}
		if verified.needed() {
			return fmt.Errorf("verify workshop %q: desktop integration is still incomplete", name)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return serveHandlers(ctx, p, selected)
}

func (r workshopRepair) apply(ctx context.Context, p host.Project, name string) error {
	if len(r.mounts) != 0 {
		if !r.start {
			if err := workshopCommand(ctx, p, "stop", name); err != nil {
				return err
			}
		}
		for _, mount := range r.connectMount {
			if err := workshopCommand(ctx, p, "connect", name+"/host-integration:"+mount.plug, name+"/system:mount"); err != nil {
				return err
			}
		}
		if err := remount(ctx, p, name, r.mounts); err != nil {
			return err
		}
	}
	if r.start || len(r.mounts) != 0 {
		if err := workshopCommand(ctx, p, "start", name); err != nil {
			return err
		}
	}
	for _, iface := range r.interfaces {
		if err := connectInterface(ctx, p.Directory, name, iface.sdk, iface.capability); err != nil {
			return fmt.Errorf("connect %s:%s: %w", iface.sdk, iface.capability, err)
		}
	}
	return nil
}

func (c workshopClient) inspect(ctx context.Context, p host.Project, name string) (workshopRepair, error) {
	var info workshopInfo
	path := "/v1/projects/" + p.ID + "/workshops/" + url.PathEscape(name)
	if err := c.get(ctx, path, &info); err != nil {
		return workshopRepair{}, err
	}
	if info.Name != name || info.ProjectID != p.ID {
		return workshopRepair{}, fmt.Errorf("received details for a different project or workshop")
	}
	if info.Status != "Ready" && info.Status != "Stopped" {
		return workshopRepair{}, fmt.Errorf("expected Ready or Stopped status, got %q", info.Status)
	}

	var connections workshopConnections
	query := url.Values{"project-id": {p.ID}, "workshop": {name}, "select": {"all"}}
	if err := c.get(ctx, "/v1/connections?"+query.Encode(), &connections); err != nil {
		return workshopRepair{}, err
	}

	repair := workshopRepair{start: info.Status == "Stopped"}
	for _, mount := range bridgeMounts {
		source, err := info.mountSource(mount)
		if err != nil {
			return workshopRepair{}, err
		}
		plug := workshopPlug{p.ID, name, "host-integration", mount.plug}
		connected, err := connections.slotFor(plug)
		if err != nil {
			return workshopRepair{}, err
		}
		if (source == "") != (connected == nil) {
			return workshopRepair{}, fmt.Errorf("inconsistent mount and connection state for %s", mount.plug)
		}
		if source != filepath.Join(p.Root, mount.directory) {
			repair.mounts = append(repair.mounts, mount)
			if connected == nil {
				repair.connectMount = append(repair.connectMount, mount)
			}
		}
	}
	for _, iface := range bridgeInterfaces {
		plug := workshopPlug{p.ID, name, iface.sdk, iface.capability}
		slot, err := connections.slotFor(plug)
		if err != nil {
			return workshopRepair{}, err
		}
		if slot == nil {
			repair.interfaces = append(repair.interfaces, iface)
		} else if *slot != (workshopSlot{p.ID, name, "system", iface.capability}) {
			return workshopRepair{}, fmt.Errorf("%s:%s is connected to an unexpected slot", iface.sdk, iface.capability)
		}
	}
	return repair, nil
}
