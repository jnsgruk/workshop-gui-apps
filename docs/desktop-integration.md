# Desktop integration in Workshop

[Setup and commands](../README.md) · [Capability boundaries](#capability-boundaries) · [Host and guest lifecycle](#host-and-guest-lifecycle) · [Guest SDK audit](#guest-sdk-audit) · [Native interface proposal](#native-interface-proposal) · [Live acceptance checks](#live-acceptance-checks)

The desktop application runs as the unprivileged Workshop user. Firefox also
runs under Snap confinement. Display, desktop services, device access, audio,
and host application launching each have their own transport and policy.

## Capability boundaries

| Capability | Transport | Boundary |
| --- | --- | --- |
| Window presentation | Wayland | Workshop desktop plug supplies the display socket and environment. |
| Notifications and desktop services | Filtered session D-Bus | The host proxy exposes selected service names to guest clients. |
| Camera | V4L2 and media device nodes | Workshop camera interface, Snap camera plug, then website permission. |
| Microphone and speakers | PulseAudio protocol backed by PipeWire | Guest compatibility server uses a host security-context socket. |
| Open URLs and shared files | JSON over a Unix socket | Guest opener forwards to the host's default application. |

Video playback uses the application's codecs and display, with sound through
the audio bridge. Camera capture and screen sharing use distinct paths.
Screen sharing requires separate screencast and portal integration. GPU access
supports rendering. Hardware decoding and DRM need independent validation.

[Firefox SDK definition](../.workshop/firefox-desktop/sdk.yaml) · [Firefox workshop actions](../.workshop/firefox.yaml) · [Workshop desktop interface](https://ubuntu.com/workshop/docs/explanation/interfaces/desktop-interface/)

### D-Bus and notifications

The logged-in desktop user runs `xdg-dbus-proxy` with `--filter`. The
allowlist includes `org.freedesktop.Notifications` for direct notification
calls and `org.freedesktop.portal.Desktop` and
`org.freedesktop.portal.Documents` for portal calls. A portal service-name
grant covers more than a single operation such as displaying a notification.
Guest snapd and the user manager continue using the guest's own buses.

Firefox creates its systemd scope on the local session bus. Its Snap command
chain then switches to the host proxy mounted read-only under
`/run/user/1000/snap.firefox/hostbus`. The launcher checks for the socket
before starting. Authentication still needs a live check. An unconfined guest
application can opt in by setting
`DBUS_SESSION_BUS_ADDRESS=unix:path=/mnt/workshop-host/session/bus`.

Guest-only icon paths, notification action routing, portal application identity,
and usable document mounts all need separate handling or validation.

[Proxy allowlist](../internal/hostcmd/dbus.go) · [Firefox launcher](../.workshop/firefox-desktop/bin/workshop-firefox) · [Snap runtime bind mount](../.workshop/firefox-desktop/systemd/run-user-1000-snap.firefox-hostbus.mount) · [Desktop Notifications protocol](https://specifications.freedesktop.org/notification/latest/protocol.html) · [xdg-dbus-proxy](https://github.com/flatpak/xdg-dbus-proxy)

### Camera

The Workshop camera interface exposes matching `/dev/video*` and
`/dev/media*` devices and tracks hotplug. The connection covers matching
devices at workshop scope. The guest user needs device-node access, Snap needs
`firefox:camera`, and Firefox prompts each website for permission.

A future portal-based camera path would need portal authorization and a
PipeWire stream. The existing D-Bus service grant supplies access to portal
calls.

[Camera declaration](../.workshop/firefox-desktop/sdk.yaml) · [Snap connections](../.workshop/firefox-desktop/hooks/setup-base) · [Workshop camera interface](https://ubuntu.com/workshop/docs/explanation/interfaces/camera-interface/)

### Audio

The host helper keeps `pw-container` alive and publishes its security-context
socket using `rename`. The source socket and host mount directory need to
share a filesystem. The directory mount preserves the file descriptor passing
required by the PipeWire native protocol. The helper labels the context
`org.mozilla.firefox` and supplies an instance ID. These labels describe the
shared connection, and multiple guest applications can use it through the same
compatibility server.

The guest's `workshop-pipewire-pulse.service` connects to the host PipeWire
graph. Its environment and the client wrapper use these settings.

| Component | Setting |
| --- | --- |
| Guest service | `PIPEWIRE_REMOTE=/mnt/workshop-host/pipewire/pipewire-0` |
| Guest service | `XDG_RUNTIME_DIR=/run/user/1000` |
| Guest service | `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus` |
| Client wrapper | `PULSE_SERVER=unix:/run/user/1000/pulse/native` |

The host session manager controls device access and routing. Playback,
microphone capture, and speaker-monitor capture are distinct permissions for
a native interface to enforce. A source ending in `.monitor` captures speaker
output. Website consent applies inside Firefox.

[Host helper](../internal/hostcmd/pipewire.go) · [Guest service](../.workshop/host-audio/systemd/workshop-pipewire-pulse.service) · [Audio wrapper](../.workshop/host-audio/bin/workshop-host-audio) · [pw-container](https://docs.pipewire.org/page_man_pw-container_1.html) · [PipeWire native protocol](https://docs.pipewire.org/page_native_protocol.html) · [WirePlumber client access](https://pipewire.pages.freedesktop.org/wireplumber/policies/client_access.html)

### Host opener

The guest normalizes paths and sends one bounded JSON request. The host checks
the peer UID, rejects control characters, resolves shared files under the host
checkout, and rejects symlink escapes. It invokes its registered `xdg-open`
with one argument and without a shell. All non-file URI schemes go to the
host's handlers. Guest-only files need to be copied into `/project` first.

The request and response limit is 16 KiB. The guest has a five-second overall
timeout and the host has a three-second per-connection I/O deadline.
`ok: true` reports that the launcher passed its immediate failure window
(200 ms). Actual application opening requires live confirmation. The UID
check identifies the socket peer at user scope.

The wrapper handles PATH and absolute-path calls to the guest utility. Toolkit
calls, `gio open`, direct D-Bus calls, portals, and Snap-bundled utilities can
bypass it. The portal offers `OpenURI` for non-file URIs and `OpenFile` for file
descriptors. A native broker needs a shared-file translation or export
contract, parent-window activation, and a way to report request results.

[Host server](../internal/opener/opener.go) · [Guest client](../internal/opener/client.go) · [Guest command](../internal/guestcmd/open.go) · [Portal specification](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.OpenURI.html)

## Host and guest lifecycle

The [command entry point](../cmd/workshop-desktop/main.go) dispatches host and
guest roles. Guest dispatch runs before host initialization, so the guest
opener can run independently of the host lockfile, project directory setup,
and desktop-user check. The dev build stages the same static binary for the
host-open SDK to install at `/usr/local/bin/workshop-desktop`.

[Connection setup](../internal/hostcmd/connect.go) stops the guest, remounts
the endpoint directories, restarts the guest, and connects desktop and camera.
It refuses to reconnect while a helper holds a capability lock. The private host
root is `/tmp/workshop-desktop-<HOST-UID>-<PROJECT-ID>/`.

| Host subdirectory | SDK mount | Guest endpoint |
| --- | --- | --- |
| `session` | `host-dbus:host-session` | `/mnt/workshop-host/session/bus` |
| `pipewire` | `host-audio:host-pipewire` | `/mnt/workshop-host/pipewire/pipewire-0` |
| `open` | `host-open:host-open` | `/mnt/workshop-host/open/socket` |

Host directories use mode 0700. Read-only mounts map ownership to guest UID
and GID 1000 and allow clients to connect to the sockets. Service policy
controls the operations available over those connections. Mounting under
`/mnt` keeps the endpoints outside the guest's boot-managed runtime directory.

Each handler owns its own lock and cleanup. [Grouped serving](../internal/hostcmd/serve.go)
stops all selected handlers when one fails. On shutdown, D-Bus and PipeWire
children receive SIGTERM and are forcibly terminated if they exceed the wait
limit. Reconnect after the host clears `/tmp` and restart affected guest clients
as needed. Existing state from the earlier combined workshop or profile
requires manual migration.

[Host dispatch](../internal/hostcmd/host.go) · [Host lifecycle utilities](../internal/host) · [Development build](../.workshop/dev.yaml)

## Guest SDK audit

This audit covers the four SDKs used by the Firefox workshop. The
[AppImage workshop](../README.md#appimage-workshop-with-codex) reuses them and
adds its own launcher and dependencies.

| SDK | Packages and runtime responsibilities |
| --- | --- |
| [host-dbus](../.workshop/host-dbus/sdk.yaml) | Provides the generic filtered session-bus mount. Application-specific bus selection lives in the application SDK. |
| [host-audio](../.workshop/host-audio/sdk.yaml) | Installs `dbus-user-session` and `pipewire-pulse`. Starts one compatibility service on demand through `workshop-host-audio COMMAND`, with systemd handling retries. Masks distribution PipeWire, Pulse socket activation, and WirePlumber units to prevent competing services. |
| [host-open](../.workshop/host-open/sdk.yaml) | Installs the staged static tool and wrapper. `dpkg-divert` preserves the packaged opener at `/usr/bin/xdg-open.distrib` when present and diverts future installs. |
| [firefox-desktop](../.workshop/firefox-desktop/sdk.yaml) | Installs `snapd` and `dbus-user-session`, owns the native interfaces and Snap permissions, and persists the Firefox profile across refreshes. |

The Firefox action composes `workshop-host-audio` with `workshop-firefox`.
The install hook enables snapd, waits for seeding, and installs Firefox from
`latest/stable` if absent. An existing install keeps its channel. The desktop
and Wayland plugs grant access to desktop resources and the display socket.
Workshop GPU and Snap `opengl` cover rendering across both confinement
boundaries. Snap `audio-playback` and `audio-record` declare the intended
capabilities while the host session manager controls actual access. The
`desktop-legacy` grant remains provisional pending live checks against the
installed Firefox and snapd versions.

The Firefox SDK owns the late bind mount and delayed bus selection. After
`/run/user/1000` exists, a prerequisite unit creates the Snap runtime parent
with guest ownership before the user manager starts. Mount `DirectoryMode`
alone would create a root-owned parent. The launcher preserves arguments and
uses `--no-remote` for instance isolation. The fixed UID matches the mount
ownership, systemd runtime, and Snap policy path.

The audit removed explicit installs of `xdg-utils`, `dbus-bin`, `v4l-utils`,
`pulseaudio-utils`, `pipewire-bin`, and extra fonts from the Firefox stack.
The opener wrapper supplies the needed entry point, and `pipewire-pulse`
brings its package dependencies. The audit also removed the host system-bus
proxy, host keyring and Firefox name-ownership grants, the X11 fallback, and
startup diagnostic probes. The audio wrapper now starts its service on demand
and sets `PULSE_SERVER` for the application. Unit reloads occur at installation
or refresh.

[Audio hook](../.workshop/host-audio/hooks/setup-base) · [Opener hook](../.workshop/host-open/hooks/setup-base) · [Firefox hook](../.workshop/firefox-desktop/hooks/setup-base) · [Snap desktop policy](https://raw.githubusercontent.com/canonical/snapd/master/interfaces/builtin/desktop.go) · [Snap legacy desktop policy](https://raw.githubusercontent.com/canonical/snapd/master/interfaces/builtin/desktop_legacy.go) · [pipewire-pulse](https://docs.pipewire.org/page_man_pipewire-pulse_1.html)

## Native interface proposal

Workshop could manage endpoint creation, mount ownership, guest environment,
confinement adapters, readiness checks, reconnect, and cleanup. Each grant
would define its scope and revocation behavior. The current sockets and
compatibility server mostly operate at workshop scope. App ID strings and Unix
UIDs alone cannot establish per-app isolation.

| Capability | Proposed contract |
| --- | --- |
| Desktop services | Declare session and system service grants individually, preserve local buses, and publish managed endpoints for app launchers. Treat notifications, portals, and secrets as distinct grants. |
| Notifications | Attribute messages to the workshop or verified app, transfer icons safely, route actions, support update and close, and revoke permission. |
| Camera | Offer explicit direct device grants and explore selected-device or portal capture with host consent, hotplug, active-use visibility, and tested disconnect behavior. |
| Audio | Manage the security context and compatibility service. Define separate playback, microphone, and monitor capture grants and their scope. |
| Host opening | Broker requests from utility, toolkit, and portal callers. Define URI policy, file translation or export, activation, failures, and asynchronous results. |

Moving mount and session lifecycle into Workshop would let a supported
confinement adapter handle the runtime paths and bus-switch timing currently
owned by the Firefox SDK. The proposed interfaces still need a decision on
whether each grant applies to the workshop, an SDK, or a verified application.

## Live acceptance checks

The [automated tests and validation history](../README.md#automated-tests-and-limits)
cover the helpers and wrappers. Earlier verification also built the static
binary and smoke-tested help and version output with a temporary Go 1.26 module.
Live Workshop and Firefox integration remains unverified.

| Capability | What to verify in a live session |
| --- | --- |
| D-Bus | Authenticate to the proxy, exercise an allowed service and confirm denial for a disallowed one. Display, update, and close a notification and receive an action callback. Verify local systemd and snapd operation and usable portal document mounts. |
| Camera | Enumerate devices, capture visible frames in Firefox, and test format negotiation, contention, website denial, hotplug, and disconnect. |
| Audio | Verify real sinks and non-monitor sources, hear playback, record intelligible microphone audio, and test host denial and revocation. |
| Opening | Exercise a web URL, mail handler, relative shared file, and local file URI. Verify rejection of non-shared paths and symlink escapes. Check toolkit, Snap, and portal callers separately. |
| Video | Play browser video with sound. Check screen sharing, hardware decode, and DRM independently where relevant. |
| Lifecycle | Stop and restart the helpers, refresh or reboot the guest, and reconnect after host reboot. Confirm cleanup, recovery, and actionable errors on both sides. |
