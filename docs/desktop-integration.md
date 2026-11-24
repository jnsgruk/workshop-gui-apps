# Desktop integration in Workshop

[Setup and commands](../README.md) · [Capability boundaries](#capability-boundaries) · [Host and guest lifecycle](#host-and-guest-lifecycle) · [Guest SDK setup](#guest-sdk-setup) · [Live acceptance checks](#live-acceptance-checks)

The desktop application runs as the unprivileged Workshop user. Firefox also
runs under Snap confinement. Display, desktop services, device access, audio,
and host application launching each have their own transport and policy.

## Capability boundaries

| Capability | Transport | Boundary |
| --- | --- | --- |
| Window presentation | Wayland | The shared SDK’s desktop plug supplies the display socket and environment. |
| Notifications and desktop services | Filtered session D-Bus | The host proxy exposes selected service names to guest clients. |
| Camera | V4L2 and media device nodes | Workshop camera interface, Snap camera plug, then website permission. |
| Microphone and speakers | PulseAudio protocol backed by PipeWire | Guest compatibility server uses a host security-context socket. |
| Open URLs | Filtered session D-Bus | The host desktop portal dispatches requests to a host URL handler. |

Video playback uses the application's codecs and display, with sound through
the audio bridge. Camera capture and screen sharing use distinct paths.
Screen sharing requires separate screencast and portal integration. GPU access
supports rendering. Hardware decoding and DRM need independent validation.

[Firefox SDK definition](../.workshop/firefox/sdk.yaml) · [Apps workshop actions](../.workshop/apps.yaml) · [Workshop desktop interface](https://ubuntu.com/workshop/docs/explanation/interfaces/desktop-interface/)

### D-Bus and notifications

The logged-in desktop user runs `xdg-dbus-proxy` with `--filter`. The
allowlist includes `org.freedesktop.Notifications` for direct notification
calls, `org.freedesktop.portal.Desktop` and `org.freedesktop.portal.Documents`
for portal calls, and `org.freedesktop.secrets` for Any's keychain. The Secret
Service grant applies to every client using the forwarded session bus, subject
to the host keyring's own access controls.
Guest snapd and the user manager continue using the guest's own buses.

Firefox creates its systemd scope on the local session bus. Its Snap command
chain then switches to the host proxy mounted read-only under
`/run/user/1000/snap.firefox/hostbus`. The launcher checks for the socket
before starting. Authentication still needs a live check. An unconfined guest
application can opt in by setting
`DBUS_SESSION_BUS_ADDRESS=unix:path=/mnt/workshop-host/session/bus`.

Guest-only icon paths, notification action routing, portal application identity,
and usable document mounts all need separate handling or validation.

[Proxy allowlist](../internal/hostcmd/dbus.go) · [Firefox launcher](../.workshop/firefox/bin/workshop-firefox) · [Snap runtime bind mount](../.workshop/firefox/systemd/run-user-1000-snap.firefox-hostbus.mount) · [Desktop Notifications protocol](https://specifications.freedesktop.org/notification/latest/protocol.html) · [xdg-dbus-proxy](https://github.com/flatpak/xdg-dbus-proxy)

### Camera

The Workshop camera interface exposes matching `/dev/video*` and
`/dev/media*` devices and tracks hotplug. The connection covers matching
devices at workshop scope. The guest user needs device-node access, Snap needs
`firefox:camera`, and Firefox prompts each website for permission.

[Camera declaration](../.workshop/firefox/sdk.yaml) · [Snap connections](../.workshop/firefox/hooks/setup-base) · [Workshop camera interface](https://ubuntu.com/workshop/docs/explanation/interfaces/camera-interface/)

### Audio

The host helper keeps `pw-container` alive and publishes its security-context
socket using `rename`. The source socket and host mount directory need to
share a filesystem. The host mount directory is the `pipewire/` subdirectory
of the [private host root](#host-and-guest-lifecycle). The directory mount preserves the file descriptor passing
required by the PipeWire native protocol. The helper labels the context
`org.mozilla.firefox` and supplies an instance ID. These labels describe the
shared connection, and multiple guest applications can use it through the same
compatibility server.

The guest's `workshop-pipewire-pulse.service` connects to the host PipeWire
graph. The service and guest launcher use these settings.

| Component | Setting |
| --- | --- |
| Guest service | `PIPEWIRE_REMOTE=/mnt/workshop-host/pipewire/pipewire-0` |
| Guest service | `XDG_RUNTIME_DIR=/run/user/1000` |
| Guest service | `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus` |
| Guest launcher | `PULSE_SERVER=unix:/run/user/1000/pulse/native` |

The host session manager controls device access and routing. Playback,
microphone capture, and speaker-monitor capture are distinct permissions. A
source ending in `.monitor` captures speaker output. Website consent applies inside Firefox.

[Host helper](../internal/hostcmd/pipewire.go) · [Guest service](../.workshop/host-integration/systemd/workshop-pipewire-pulse.service) · [Audio wrapper](../.workshop/host-integration/bin/workshop-host-audio) · [pw-container](https://docs.pipewire.org/page_man_pw-container_1.html) · [PipeWire native protocol](https://docs.pipewire.org/page_native_protocol.html) · [WirePlumber client access](https://pipewire.pages.freedesktop.org/wireplumber/policies/client_access.html)

### Host URL opening

The guest `/usr/bin/xdg-open` wrapper takes one non-file URI and uses
`gdbus` to call `org.freedesktop.portal.OpenURI.OpenURI` on the filtered host
session bus at `/mnt/workshop-host/session/bus`. Run `host dbus` or
`host serve` to publish that socket. The portal selects the host handler.
A successful exit means the portal returned a request handle. Application
launch and any user choice happen asynchronously.

The wrapper rejects file paths and `file:` URIs. Toolkit calls, `gio open`,
direct D-Bus calls, and Snap-bundled utilities can bypass the wrapper and
use their own bus selection and confinement rules.

[Guest wrapper](../.workshop/host-integration/bin/xdg-open) · [Portal specification](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.OpenURI.html)

## Host and guest lifecycle

The [command entry point](../cmd/workshop-desktop/main.go) dispatches host and
guest roles. Guest dispatch runs before host initialization. The dev build
stages the same static binary for the
host-integration SDK to install at `/usr/local/bin/workshop-desktop`.

[Connection setup](../internal/hostcmd/connect.go) stops the guest, remounts
the endpoint directories, restarts the guest, and connects desktop and camera.
It refuses to reconnect while a helper holds a capability lock. The private host
root is `/tmp/workshop-desktop-<HOST-UID>-<PROJECT-ID>/`.

| Host subdirectory | SDK mount | Guest endpoint |
| --- | --- | --- |
| `session` | `host-integration:host-session` | `/mnt/workshop-host/session/bus` |
| `pipewire` | `host-integration:host-pipewire` | `/mnt/workshop-host/pipewire/pipewire-0` |

Host directories use mode 0700. Read-only mounts map ownership to guest UID
and GID 1000 and allow clients to connect to the sockets. Service policy
controls the operations available over those connections. Mounting under
`/mnt` keeps the endpoints outside the guest's boot-managed runtime directory.

Each handler reports readiness and owns its own lock and cleanup.
[Grouped serving](../internal/hostcmd/serve.go) runs all handlers by default.
`--only` accepts a comma-separated list of handler names and runs each named
handler once. A filtered server and individual helpers can run together when
their capabilities do not overlap. If one handler fails, the server stops the
others and waits for cleanup. On shutdown, D-Bus and PipeWire
children receive SIGTERM and are forcibly terminated if they exceed the wait
limit. Reconnect after the host clears `/tmp` and restart affected guest clients
as needed. Runtime diagnostics and child-process output use Go's `slog` text
handler on stderr. Help and version output use plain text.

[Host dispatch](../internal/hostcmd/host.go) · [Host lifecycle utilities](../internal/host) · [Development build](../.workshop/dev.yaml)

## Guest SDK setup

The [apps workshop](../.workshop/apps.yaml) composes the shared host-integration
SDK with `firefox`, `any`, the Codex CLI, and
[Codex desktop](../.workshop/codex-desktop). The shared SDK declares the
desktop and GPU plugs and groups setup by capability.

| Shared capability | Guest setup |
| --- | --- |
| Wayland | The SDK installs `wayland-info` so the Go launcher can verify that the display is reachable before starting an app. |
| D-Bus | The SDK declares the filtered session-bus mount. App launchers select the host or local session bus through `guest launch`. |
| Audio | The setup hook installs `dbus-user-session` and `pipewire-pulse`. It installs a compatibility service that starts on demand through the Go guest launcher, with systemd handling retries. It masks distribution PipeWire, Pulse socket activation, and WirePlumber units to prevent competing services. |
| Host URL opening | The setup hook installs `gdbus` from `libglib2.0-bin` and the wrapper. `dpkg-divert` preserves the packaged opener at `/usr/bin/xdg-open.distrib` when present and diverts future installs. |

The shared hook checks for the staged binary before changing the guest.
The Firefox SDK installs `snapd` and `dbus-user-session`, owns the native camera
plug and Snap permissions, and persists the Firefox profile across refreshes.
The [`any` setup hook](../.workshop/any/hooks/setup-base) installs the
application libraries and resolves the latest Linux amd64 AppImage from the
nightly manifest. It downloads to a temporary file in `/opt/any` and renames
it to `/opt/any/Any.AppImage` after a successful download. The launcher uses
that installed path on every run. An incremental refresh can reuse the SDK
without downloading a new build. The launcher forwards arguments and uses
`--appimage-extract-and-run` to run without FUSE and leaves the application’s
sandbox defaults in effect.

Both app actions use `workshop-desktop guest launch`. Any selects
`--session-bus=host`. Firefox selects `--session-bus=local` so Snap can
create its systemd scope and then switches to the filtered host bus inside
confinement. The launch command requires the Wayland connection and the
mounted PipeWire socket. The compatibility command `workshop-host-audio`
starts the audio service and sets `PULSE_SERVER` for its command.

The Firefox setup hook enables snapd, waits for seeding, and installs Firefox from
`latest/stable` if absent. An existing install keeps its channel. The desktop
and Wayland plugs grant access to desktop resources and the display socket.
Workshop GPU and Snap `opengl` cover rendering across both confinement
boundaries. Snap `audio-playback` and `audio-record` declare the intended
capabilities while the host session manager controls actual access. The setup hook
also connects the Snap `desktop-legacy` plug.

The Firefox SDK owns the late bind mount and delayed bus selection. After
`/run/user/1000` exists, a prerequisite unit creates the Snap runtime parent
with guest ownership before the user manager starts. Mount `DirectoryMode`
alone would create a root-owned parent. The launcher preserves arguments and
uses `--no-remote` for instance isolation. The fixed UID matches the mount
ownership, systemd runtime, and Snap policy path.

[Shared SDK setup hook](../.workshop/host-integration/hooks/setup-base) · [Firefox hook](../.workshop/firefox/hooks/setup-base) · [Snap desktop policy](https://raw.githubusercontent.com/canonical/snapd/master/interfaces/builtin/desktop.go) · [Snap legacy desktop policy](https://raw.githubusercontent.com/canonical/snapd/master/interfaces/builtin/desktop_legacy.go) · [pipewire-pulse](https://docs.pipewire.org/page_man_pipewire-pulse_1.html)

### Any state across refreshes

The Any SDK declares three private, writable mounts for data and settings
in the `apps` workshop.

| State | Guest mount target |
| --- | --- |
| Backend database, device identity, local records, and attachments | `/home/workshop/.any-nightly` |
| Desktop application data and WebKit profile | `/home/workshop/.local/share/io.anyproto.any.nightly` |
| Desktop configuration | `/home/workshop/.config/io.anyproto.any.nightly` |

Workshop provisions host-backed directories for the mount plugs and keeps
their connections across refreshes. If you use custom `ANY_DATA_DIR` or XDG
paths, set the SDK mount targets to the paths used by Any. Remembered sign-in
uses the host Secret Service. Keep the same host keyring available when you
relaunch Any.

[Workshop SDK mounts](https://ubuntu.com/workshop/docs/reference/sdks/#mount-interface) · [Connections across refresh and restore](https://ubuntu.com/workshop/docs/explanation/workshops/concepts/#connections-across-refresh-and-restore)

## Live acceptance checks

Run these checks in the connected `apps` workshop with the host handlers
running. The [automated tests](../AGENTS.md#testing) cover
the helpers and wrappers.

| Capability | What to verify in a live session |
| --- | --- |
| D-Bus | Authenticate to the proxy, exercise an allowed service and confirm denial for a disallowed one. Display, update, and close a notification and receive an action callback. Verify local systemd and snapd operation and usable portal document mounts. |
| Camera | Enumerate devices, capture visible frames in Firefox, and test format negotiation, contention, website denial, hotplug, and disconnect. |
| Audio | Verify real sinks and non-monitor sources, hear playback, record intelligible microphone audio, and test host denial and revocation. |
| URL opening | Exercise HTTPS and `mailto:` with the guest wrapper and verify the host browser and mail handler. Check rejection of file paths and `file:` URIs, and verify the error when the host bus proxy is stopped. Check toolkit and Snap callers separately. |
| Video | Play browser video with sound. Check screen sharing, hardware decode, and DRM independently where relevant. |
| Lifecycle | Stop and restart the helpers, refresh or reboot the guest, and reconnect after host reboot. Confirm cleanup, recovery, and actionable errors on both sides. |
