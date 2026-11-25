package hostcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type workshopClient struct{ *http.Client }

func workshopSocket() string {
	for _, name := range []string{"WORKSHOP_SOCKET", "WORKSHOP_COMMON", "WORKSHOP_DATA"} {
		if value := os.Getenv(name); value != "" {
			if name == "WORKSHOP_SOCKET" {
				return value
			}
			return filepath.Join(value, "workshop.socket")
		}
	}
	const snapSocket = "/var/snap/workshop/common/workshop/workshop.socket"
	if _, err := os.Stat(snapSocket); !os.IsNotExist(err) {
		return snapSocket
	}
	return "/var/lib/workshop/workshop.socket"
}

func newWorkshopClient(socket string) workshopClient {
	return workshopClient{&http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socket)
			},
		},
	}}
}

func (c workshopClient) get(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request to Workshop API %s: %s", req.URL.Path, resp.Status)
	}
	var envelope struct {
		Type   string          `json:"type"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Workshop API %s: %w", req.URL.Path, err)
	}
	if envelope.Type != "sync" || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return fmt.Errorf("unexpected Workshop API response for %s", req.URL.Path)
	}
	return json.Unmarshal(envelope.Result, result)
}

type workshopInfo struct {
	ProjectID string `json:"project-id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	SDKs      []struct {
		Name   string `json:"name"`
		Mounts []struct {
			Plug   workshopPlug `json:"plug"`
			Source string       `json:"host-source"`
			Target string       `json:"workshop-target"`
		} `json:"mounts"`
	} `json:"sdks"`
}

func (info workshopInfo) mountSource(mount bridgeMount) (string, error) {
	for _, sdk := range info.SDKs {
		if sdk.Name != "host-integration" {
			continue
		}
		var source string
		for _, current := range sdk.Mounts {
			if current.Plug.Name != mount.plug {
				continue
			}
			if source != "" || !filepath.IsAbs(current.Source) || current.Target != "/mnt/workshop-host/"+mount.directory {
				return "", fmt.Errorf("unexpected source or target for %s", mount.plug)
			}
			source = current.Source
		}
		return source, nil
	}
	return "", fmt.Errorf("host-integration SDK is missing")
}

type workshopPlug struct {
	ProjectID string `json:"project-id"`
	Workshop  string `json:"workshop"`
	SDK       string `json:"sdk"`
	Name      string `json:"plug"`
}

type workshopSlot struct {
	ProjectID string `json:"project-id"`
	Workshop  string `json:"workshop"`
	SDK       string `json:"sdk"`
	Name      string `json:"slot"`
}

type workshopConnections struct {
	Plugs       []workshopPlug `json:"plugs"`
	Established []struct {
		Plug workshopPlug `json:"plug"`
		Slot workshopSlot `json:"slot"`
	} `json:"established"`
}

func (c workshopConnections) slotFor(plug workshopPlug) (*workshopSlot, error) {
	if !slices.Contains(c.Plugs, plug) {
		return nil, fmt.Errorf("required plug %s:%s is missing", plug.SDK, plug.Name)
	}
	var slot *workshopSlot
	for _, conn := range c.Established {
		if conn.Plug != plug {
			continue
		}
		if slot != nil || conn.Slot.ProjectID == "" || conn.Slot.Workshop == "" || conn.Slot.SDK == "" || conn.Slot.Name == "" {
			return nil, fmt.Errorf("ambiguous connection state for %s:%s", plug.SDK, plug.Name)
		}
		slot = &conn.Slot
	}
	return slot, nil
}
