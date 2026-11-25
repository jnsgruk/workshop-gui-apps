package hostcmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"workshop-desktop/internal/host"
	"workshop-desktop/internal/logging"
)

const launcherPrefix = "workshop-apps-"

var launcherName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type launcherMetadata struct {
	Name           string `json:"name"`
	StartupWMClass string `json:"startup_wm_class"`
	Action         string `json:"action"`
	Icon           string `json:"icon"`
}

func manageAllLaunchers(ctx context.Context, p host.Project, install bool) error {
	if !install {
		return removeLaunchers(ctx, p, "")
	}
	sdksDir := filepath.Join(p.Directory, ".workshop")
	entries, err := os.ReadDir(sdksDir)
	if err != nil {
		return fmt.Errorf("list project SDKs: %w", err)
	}
	var failures []error
	for _, entry := range entries {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		sdk := entry.Name()
		if !entry.IsDir() || !launcherName.MatchString(sdk) {
			continue
		}
		_, err := os.Lstat(filepath.Join(sdksDir, sdk, "launcher.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil {
			err = manageLauncher(ctx, p, sdk, install)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("launcher for %s: %w", sdk, err))
		}
	}
	return errors.Join(failures...)
}

func manageLauncher(ctx context.Context, p host.Project, sdk string, install bool) error {
	if !install {
		return removeLaunchers(ctx, p, sdk)
	}
	sdkDir := filepath.Join(p.Directory, ".workshop", sdk)
	metadata, err := readLauncherMetadata(sdkDir)
	if err != nil {
		return err
	}

	paths, err := launcherPathsFor(p)
	if err != nil {
		return err
	}
	directory, err := paths.lock(true)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()

	id := launcherPrefix + sdk
	desktopName := id + ".desktop"
	desktopPath := filepath.Join(paths.applications, desktopName)
	status, err := paths.status(desktopPath)
	if err != nil {
		return err
	}
	if status == launcherForeign {
		return foreignLauncherError(desktopPath)
	}
	if err := paths.checkDesktopIDCollisions(desktopName, desktopPath); err != nil {
		return err
	}

	iconPath := filepath.Join(paths.icons, id+".png")
	workshop, err := exec.LookPath("workshop")
	if err != nil {
		return fmt.Errorf("find Workshop executable: %w", err)
	}
	workshop, err = filepath.Abs(workshop)
	if err != nil {
		return err
	}
	// GLib validates the executable path before expanding Exec field codes.
	if strings.Contains(workshop, "%") {
		return fmt.Errorf("Workshop executable path contains %% and cannot be used in a desktop launcher: %s", workshop)
	}
	icon, err := os.ReadFile(filepath.Join(sdkDir, metadata.Icon))
	if err != nil {
		return fmt.Errorf("read launcher icon: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(iconPath), 0o755); err != nil {
		return err
	}
	if err := writeLauncherFile(iconPath, icon); err != nil {
		return err
	}
	entry := fmt.Sprintf("[Desktop Entry]\n%s\nType=Application\nName=%s\nExec=%s run apps -- %s\nPath=%s\nIcon=%s\nTerminal=false\n",
		paths.owner, desktopValue(metadata.Name), desktopExec(workshop), metadata.Action,
		desktopValue(p.Directory), desktopValue(iconPath))
	if metadata.StartupWMClass != "" {
		entry += "StartupWMClass=" + desktopValue(metadata.StartupWMClass) + "\n"
	}
	if err := writeLauncherFile(desktopPath, []byte(entry)); err != nil {
		return err
	}
	logging.Result(ctx, fmt.Sprintf("Installed desktop launcher at %q.", desktopPath))
	return nil
}

func readLauncherMetadata(sdkDir string) (launcherMetadata, error) {
	metadataPath := filepath.Join(sdkDir, "launcher.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return launcherMetadata{}, fmt.Errorf("read launcher metadata: %w", err)
	}
	var metadata launcherMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return launcherMetadata{}, fmt.Errorf("parse %s: %w", metadataPath, err)
	}
	if !launcherName.MatchString(metadata.Action) ||
		strings.TrimSpace(metadata.Name) == "" || !filepath.IsLocal(metadata.Icon) ||
		filepath.Base(metadata.Icon) != metadata.Icon || filepath.Ext(metadata.Icon) != ".png" {
		return launcherMetadata{}, fmt.Errorf("invalid launcher metadata: %s", metadataPath)
	}
	return metadata, nil
}

type launcherPaths struct {
	dataHome     string
	applications string
	icons        string
	owner        string
}

func launcherPathsFor(p host.Project) (launcherPaths, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(dataHome) {
		home, err := os.UserHomeDir()
		if err != nil {
			return launcherPaths{}, err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	projectKey := fmt.Sprintf("%x", sha256.Sum256([]byte(p.Directory)))
	return launcherPaths{
		dataHome:     dataHome,
		applications: filepath.Join(dataHome, "applications"),
		icons:        filepath.Join(dataHome, "workshop-desktop", projectKey),
		owner:        "X-Workshop-Project=" + projectKey,
	}, nil
}

func (p launcherPaths) lock(create bool) (*os.File, error) {
	if create {
		if err := os.MkdirAll(p.applications, 0o755); err != nil {
			return nil, err
		}
	}
	directory, err := os.Open(p.applications)
	if err != nil {
		return nil, err
	}
	// Serialize updates even when two checkouts claim the same desktop ID.
	if err := syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = directory.Close()
		return nil, fmt.Errorf("lock launcher directory: %w", err)
	}
	return directory, nil
}

type launcherStatus int

const (
	launcherMissing launcherStatus = iota
	launcherOwned
	launcherForeign
)

func (p launcherPaths) status(path string) (launcherStatus, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return launcherMissing, nil
	}
	if err != nil {
		return launcherMissing, err
	}
	if !info.Mode().IsRegular() {
		return launcherForeign, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return launcherMissing, err
	}
	if strings.HasPrefix(string(data), "[Desktop Entry]\n"+p.owner+"\n") {
		return launcherOwned, nil
	}
	return launcherForeign, nil
}

func foreignLauncherError(path string) error {
	return fmt.Errorf("launcher belongs to another installation: %s", path)
}

func removeLaunchers(ctx context.Context, p host.Project, sdk string) error {
	paths, err := launcherPathsFor(p)
	if err != nil {
		return err
	}
	directory, err := paths.lock(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()

	names := []string{launcherPrefix + sdk + ".desktop"}
	if sdk == "" {
		names, err = directory.Readdirnames(-1)
		if err != nil {
			return err
		}
	}
	var failures []error
	for _, name := range names {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		id, desktop := strings.CutSuffix(name, ".desktop")
		suffix, prefixed := strings.CutPrefix(id, launcherPrefix)
		if !desktop || !prefixed || !launcherName.MatchString(suffix) {
			continue
		}
		if err := paths.removeLauncher(ctx, id, sdk != ""); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (p launcherPaths) removeLauncher(ctx context.Context, id string, explicit bool) error {
	desktop := filepath.Join(p.applications, id+".desktop")
	status, err := p.status(desktop)
	if err != nil {
		return err
	}
	if status == launcherForeign && explicit {
		return foreignLauncherError(desktop)
	}
	if status != launcherOwned {
		return nil
	}
	icon := filepath.Join(p.icons, id+".png")
	if err := os.Remove(icon); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(desktop); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	logging.Result(ctx, fmt.Sprintf("Removed desktop launcher at %q.", desktop))
	return nil
}

func (p launcherPaths) checkDesktopIDCollisions(name, desktopPath string) error {
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, dataDir := range append([]string{p.dataHome}, filepath.SplitList(dataDirs)...) {
		if !filepath.IsAbs(dataDir) {
			continue
		}
		applications := filepath.Join(dataDir, "applications")
		allowed := ""
		if applications == p.applications {
			allowed = desktopPath
		}
		if err := checkDesktopID(applications, name, allowed); err != nil {
			return err
		}
	}
	return nil
}

// Each directory separator in a relative desktop filename becomes a hyphen
// in its XDG desktop file ID. Only descend into directories matching a prefix.
func checkDesktopID(directory, id, allowed string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if entry.Name() == id && !entry.IsDir() && path != allowed {
			return fmt.Errorf("launcher would shadow an existing desktop entry: %s", path)
		}
		suffix, prefixed := strings.CutPrefix(id, entry.Name()+"-")
		if !prefixed || (!entry.IsDir() && entry.Type()&os.ModeSymlink == 0) {
			continue
		}
		if err := checkDesktopID(path, suffix, allowed); err != nil {
			return err
		}
	}
	return nil
}

func desktopValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, " ", `\s`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(value)
}

func desktopExec(path string) string {
	// Exec quoting is interpreted after the generic desktop string escapes.
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(path)
	return desktopValue(`"` + quoted + `"`)
}

func writeLauncherFile(path string, contents []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".workshop-launcher-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(contents)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
