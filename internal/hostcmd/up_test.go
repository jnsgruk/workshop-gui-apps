package hostcmd

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workshop-desktop/internal/host"
)

func TestUp(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "workshop.sock")
	for _, tc := range []struct {
		name       string
		mode       string
		wantRepair string
		wantError  string
	}{
		{name: "healthy"},
		{name: "missing desktop", mode: "desktop", wantRepair: "connect second/host-integration:desktop"},
		{name: "stale mount", mode: "stale", wantRepair: "stop second\nremount second/host-integration:host-session ROOT/session\nstart second"},
		{name: "disconnected mount", mode: "mount", wantRepair: "stop second\nconnect second/host-integration:host-session second/system:mount\nremount second/host-integration:host-session ROOT/session\nstart second"},
		{name: "retry after failed restart", mode: "stopped", wantRepair: "start second"},
		{name: "inspection failure before mutation", mode: "inspect", wantError: `inspect workshop "second"`},
		{name: "repair failure", mode: "repair", wantRepair: "connect second/host-integration:desktop", wantError: `repair workshop "second"`},
		{name: "verification failure", mode: "verify", wantRepair: "connect second/host-integration:desktop", wantError: `verify workshop "second"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := host.Project{ID: "test", Directory: t.TempDir(), Root: t.TempDir()}
			callsPath := filepath.Join(p.Root, "calls")
			t.Setenv("PATH", p.Root)
			t.Setenv("UP_TEST_CALLS", callsPath)
			t.Setenv("UP_TEST_MODE", tc.mode)
			if err := os.WriteFile(filepath.Join(p.Root, "workshop"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$UP_TEST_CALLS"
if [ "$UP_TEST_MODE" = repair ]; then
	exit 1
fi
`), 0o700); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := r.URL.Query().Get("workshop")
				if name == "" {
					name = filepath.Base(r.URL.Path)
				}
				_, err := os.Stat(callsPath)
				repaired := err == nil && tc.mode != "verify"
				mode := ""
				if !repaired && (name == "second" || tc.mode == "inspect") {
					mode = tc.mode
				}
				if name == "second" && mode == "inspect" {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				if err := json.NewEncoder(w).Encode(map[string]any{
					"type": "sync", "result": workshopFixture(p, name, mode, r.URL.Path == "/v1/connections"),
				}); err != nil {
					t.Error(err)
				}
			}))
			if err := server.Listener.Close(); err != nil {
				t.Fatal(err)
			}
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server.Listener = listener
			server.Start()
			t.Cleanup(server.Close)
			t.Setenv("WORKSHOP_SOCKET", socket)
			served := make(chan struct{}, 1)
			done := errors.New("handler ran")
			selected := []handler{{"dbus", func(context.Context, host.Project) error {
				lock, err := p.Lock("dbus")
				if err == nil {
					_ = lock.Close()
					t.Error("dbus lock was available while serving")
				}
				served <- struct{}{}
				return done
			}}}

			err = up(t.Context(), p, []string{"apps", "second"}, selected)
			if tc.wantError == "" {
				if !errors.Is(err, done) || len(served) != 1 {
					t.Fatalf("expected serving after preparation, got %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) || len(served) != 0 {
				t.Fatalf("expected %s and no serving, got %v", tc.wantError, err)
			}
			calls, err := os.ReadFile(callsPath)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if got, want := strings.TrimSpace(string(calls)), strings.ReplaceAll(tc.wantRepair, "ROOT", p.Root); got != want {
				t.Fatalf("repair commands = %q, want %q", got, want)
			}
		})
	}
}

func workshopFixture(p host.Project, name, mode string, connections bool) any {
	var mounts, plugs, established []any
	addPlug := func(sdk, plug, slot string, connected bool) {
		ref := workshopPlug{p.ID, name, sdk, plug}
		plugs = append(plugs, ref)
		if connected {
			established = append(established, map[string]any{"plug": ref, "slot": workshopSlot{p.ID, name, "system", slot}})
		}
	}
	for _, mount := range bridgeMounts {
		connected := mode != "mount" || mount.directory != "session"
		addPlug("host-integration", mount.plug, "mount", connected)
		if !connected {
			continue
		}
		source := filepath.Join(p.Root, mount.directory)
		if mount.directory == "session" && mode == "stale" {
			source = "/other/session"
		}
		mounts = append(mounts, map[string]any{
			"plug": workshopPlug{p.ID, name, "host-integration", mount.plug}, "host-source": source,
			"workshop-target": "/mnt/workshop-host/" + mount.directory,
		})
	}
	addPlug("host-integration", "desktop", "desktop", mode == "" || mode == "stale" || mode == "mount" || mode == "stopped")
	addPlug("firefox", "camera", "camera", true)
	if connections {
		return map[string]any{"plugs": plugs, "established": established}
	}
	status := "Ready"
	if mode == "stopped" {
		status = "Stopped"
	}
	return map[string]any{
		"project-id": p.ID, "name": name, "status": status,
		"sdks": []any{map[string]any{"name": "host-integration", "mounts": mounts}},
	}
}
