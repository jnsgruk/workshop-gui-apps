package hostcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workshop-desktop/internal/host"
)

func TestRemoveLaunchersWithoutMetadata(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	project := host.Project{Directory: t.TempDir()}
	other := host.Project{Directory: t.TempDir()}
	for _, tc := range []struct {
		sdk     string
		project host.Project
	}{
		{"any", project}, {"firefox", project}, {"codex-desktop", other},
	} {
		paths, err := launcherPathsFor(tc.project)
		if err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{paths.applications, paths.icons} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		id := launcherPrefix + tc.sdk
		if err := os.WriteFile(filepath.Join(paths.applications, id+".desktop"), []byte("[Desktop Entry]\n"+paths.owner+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(paths.icons, id+".png"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := launcherPathsFor(project)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(paths.applications, launcherPrefix+"codex-desktop.desktop")
	if err := manageLauncher(t.Context(), project, "codex-desktop", false); err == nil ||
		!strings.Contains(err.Error(), "launcher belongs to another installation: "+foreign) {
		t.Fatalf("expected an error naming the other checkout's launcher, got %v", err)
	}
	if err := manageLauncher(t.Context(), project, "missing", false); err != nil {
		t.Fatalf("removing a missing launcher should succeed: %v", err)
	}
	if err := manageLauncher(t.Context(), project, "any", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.applications, launcherPrefix+"any.desktop")); !os.IsNotExist(err) {
		t.Fatalf("expected single launcher to be removed, got %v", err)
	}
	if err := manageAllLaunchers(t.Context(), project, false); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(paths.applications)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != launcherPrefix+"codex-desktop.desktop" {
		t.Fatalf("expected only the other checkout's launcher, got %v", entries)
	}
	icons, err := os.ReadDir(paths.icons)
	if err != nil {
		t.Fatal(err)
	}
	if len(icons) != 0 {
		t.Fatalf("expected owned icons to be removed, got %v", icons)
	}
}

func TestInstallRejectsNestedDesktopIDCollision(t *testing.T) {
	for _, location := range []string{"user", "system"} {
		t.Run(location, func(t *testing.T) {
			user, system := t.TempDir(), t.TempDir()
			t.Setenv("XDG_DATA_HOME", user)
			t.Setenv("XDG_DATA_DIRS", system)
			dataDir := user
			if location == "system" {
				dataDir = system
			}
			nested := filepath.Join(dataDir, "applications", "workshop", "apps")
			if err := os.MkdirAll(nested, 0o700); err != nil {
				t.Fatal(err)
			}
			collision := filepath.Join(nested, "firefox.desktop")
			if err := os.WriteFile(collision, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			project := host.Project{Directory: t.TempDir()}
			sdk := filepath.Join(project.Directory, ".workshop", "firefox")
			if err := os.MkdirAll(sdk, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sdk, "launcher.json"), []byte(`{"name":"Firefox","action":"firefox","icon":"launcher.png"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := manageLauncher(t.Context(), project, "firefox", true); err == nil || !strings.Contains(err.Error(), collision) {
				t.Fatalf("expected an error naming the conflicting desktop file, got %v", err)
			}
		})
	}
}

func TestInstallLauncherUsesSharedBridgeWrapper(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	toolsDir := filepath.Join(t.TempDir(), "host tools")
	if err := os.Mkdir(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	workshop := filepath.Join(toolsDir, "workshop")
	if err := os.WriteFile(workshop, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolsDir)

	project := host.Project{Directory: filepath.Join(t.TempDir(), "app project")}
	sdk := filepath.Join(project.Directory, ".workshop", "any")
	if err := os.MkdirAll(sdk, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := []byte(`{"name":"Any","action":"any","icon":"launcher.png","startup_wm_class":"Any"}`)
	if err := os.WriteFile(filepath.Join(sdk, "launcher.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "launcher.png"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manageLauncher(t.Context(), project, "any", true); err != nil {
		t.Fatal(err)
	}
	launcher, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(filepath.Join(dataHome, "applications", launcherPrefix+"any.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"Exec=" + desktopExec(launcher) + " host launch --notify -- " + desktopExec(workshop) + " run apps -- any",
		"Path=" + desktopValue(project.Directory),
		"StartupWMClass=Any",
	} {
		if !strings.Contains(string(entry), "\n"+line+"\n") {
			t.Errorf("desktop entry missing %q:\n%s", line, entry)
		}
	}
}
