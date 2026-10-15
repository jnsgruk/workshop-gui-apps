# Desktop apps in Workshop

A Firefox-only, Wayland-only experiment in sharing selected host desktop services
with a Workshop container. It exercises a filtered session D-Bus connection,
PipeWire audio through a security-context socket, Workshop's desktop and camera
interfaces, and a guest `xdg-open` client that forwards requests to the host.

The [integration document](desktop-integration.html) describes the namespace
boundaries, guest SDK audit, and proposed native mediation. Automated tests cover
the helpers and wrappers; they do not establish that Firefox's desktop, portal,
audio, or camera integration works end to end.

## Build and run

The [development workshop](.workshop/dev.yaml) uses Go 1.27 and the standard
library to build one static `workshop-desktop` binary with host and guest subcommands:

```sh
workshop launch dev
workshop run dev -- build
workshop run dev -- test
workshop run dev -- fmt
```

Build writes `bin/workshop-desktop` and stages the same binary for the
[opener install hook](.workshop/host-open/hooks/setup-base). Build before launching
or refreshing the [Firefox workshop](.workshop/firefox.yaml); the hook refuses to
install without the binary. The build architecture must match the host and guest.
The runtime SDKs need neither Go nor Python.

The host needs Workshop/LXD with nested Snap support, a Wayland session,
`xdg-dbus-proxy`, `pw-container`, `stdbuf`, and `xdg-open`. Host PipeWire must support
SecurityContext and have a running session manager such as WirePlumber. Socket
publication uses `rename`, so the socket created by `pw-container` and the
`/tmp/workshop-desktop-<HOST-UID>-<PROJECT-ID>/pipewire/` destination must be on
the same filesystem; cross-filesystem publication fails.

```sh
workshop launch firefox
./bin/workshop-desktop host connect
```

The Firefox setup hook installs Firefox from `latest/stable` if it is absent
and connects its required Snap plugs; it does not change an existing install’s
channel. `connect` stops the workshop, remounts three
private directories, restarts it, and connects its desktop and camera interfaces.
Close guest GUI apps and stop running helpers before reconnecting.
Host commands default to the current directory, which must contain
`.workshop.lock` from a Workshop launch. `host COMMAND --project PATH` selects
another project directory; `host connect --workshop NAME` selects a workshop
using the same SDK and plug names as the Firefox definition. It does not change
SDK composition.

Run all handlers in a host graphical-session terminal, without sudo:

```sh
./bin/workshop-desktop host serve
```

To run a subset, use a comma-separated filter:

```sh
./bin/workshop-desktop host serve --only=pipewire,dbus
```

The available handlers are `dbus`, `pipewire`, and `open`. An omitted `--only`
runs all three; empty or unknown names are rejected. Duplicate names run once.
`serve` runs the selected handlers concurrently in one process. It stops all
selected handlers if one fails, and waits for their cleanup.
You can also run helpers separately:

```sh
./bin/workshop-desktop host dbus
./bin/workshop-desktop host pipewire
./bin/workshop-desktop host open
```

Each helper reports readiness, locks its own capability, and cleans up on
Ctrl-C/SIGTERM. Only selected capabilities are locked, so filtered `serve` and
individual helpers can run together when their capabilities do not overlap.
Runtime diagnostics, readiness messages, and child-process output use Go's `slog`
text handler on stderr, with command, handler, and process fields where applicable.
Help and version output remain plain text.
Keep the handlers running, then launch Firefox from another terminal:

```sh
workshop run firefox -- firefox https://example.com
```

## Parts of the experiment

| Guest SDK | Responsibility | Host implementation |
| --- | --- | --- |
| [host-dbus](.workshop/host-dbus) | Mount a filtered host session bus; retain the guest's local buses | [D-Bus proxy and allowlist](internal/hostcmd/dbus.go) |
| [host-audio](.workshop/host-audio) | Run guest `pipewire-pulse` against the host security-context socket; select it through an application wrapper | [PipeWire helper](internal/hostcmd/pipewire.go), [socket publication](internal/pipewire/socket.go) |
| [host-open](.workshop/host-open) | Install the guest client as `/usr/bin/xdg-open` using `dpkg-divert` | [Host server](internal/hostcmd/open.go), [guest CLI](internal/guestcmd/open.go), [protocol and launcher](internal/opener/opener.go) |
| [firefox-desktop](.workshop/firefox-desktop) | Install Firefox, configure its profile and Snap permissions, and adapt its launch environment | Workshop's native desktop/GPU/camera interfaces |

[Connection setup](internal/hostcmd/connect.go) manages mounts and interfaces separately
from the servers. [Host utilities](internal/host) manage private paths, locks,
signals, and child processes. Endpoints live under
`/tmp/workshop-desktop-<HOST-UID>-<PROJECT-ID>/`; reconnect after reboot if `/tmp`
was cleared.

The D-Bus proxy allows Notifications, Desktop portal, and Documents portal.
Firefox uses the local guest bus for Snap's systemd scope, then switches to the
host proxy through a read-only directory bind mount at
`/run/user/1000/snap.firefox/hostbus`. The writable Snap runtime stays separate,
a boot service creates its parent with UID/GID 1000 ownership, and Firefox
checks that the proxy socket exists before starting. This check does not prove
that a connection or D-Bus authentication will succeed. Other unconfined guest
apps can opt in with `DBUS_SESSION_BUS_ADDRESS=unix:path=/mnt/workshop-host/session/bus`.

Microphone and speakers use guest PulseAudio compatibility through the host
PipeWire socket. Capture permissions depend on host session-manager policy.
Camera uses Workshop's camera interface and Firefox's Snap camera plug.
There is no X11 fallback.

## Open on the host

```sh
workshop exec firefox -- xdg-open https://example.com
workshop exec firefox -- xdg-open mailto:someone@example.com
workshop exec firefox -- xdg-open /project/README.md
```

You can also run `workshop-desktop guest open URL|FILE` directly in the guest.

The [client](internal/opener/client.go) sends a bounded JSON request to the host.
[URI handling](internal/opener/uri.go) maps existing files under `/project` to the
host checkout and rejects symlink escapes. Other absolute URI schemes are
forwarded without a scheme allowlist. Requests and responses are bounded to
16 KiB; the guest has a five-second overall timeout, and the host gives each
connection a three-second I/O deadline. The host invokes its real `xdg-open`
with one argument and no shell. Success means no immediate launcher failure;
it does not confirm that an application opened the target.

Snap-bundled utilities, toolkit openers, and portal calls may bypass this client.
The guest `/usr/bin/xdg-open` wrapper runs
`/usr/local/bin/workshop-desktop guest open "$@"`. Host requests still invoke the
host’s regular `xdg-open` utility.

## Automated tests and limits

`workshop run dev -- test` runs all Go tests, including shell-wrapper checks:

- [Host lifecycle](internal/host/host_test.go): capability locks, symlink/file
  rejection, child cancellation, unexpected process exit, and structured child
  output.
- [Host opener](internal/opener/opener_test.go): URI validation, project path
  boundaries, rejection of mismatched UIDs, launcher arguments and exit errors,
  and server cancellation.
- [Guest protocol](internal/opener/client_test.go): file normalization, an
  in-memory client/server exchange, invalid or oversized messages, host errors,
  and canceled requests.
- [Guest CLI](internal/guestcmd/open_test.go): help/version output, argument
  validation, argument preservation, and exit codes.
- [PipeWire socket](internal/pipewire/socket_test.go): reject non-sockets and
  preserve the socket inode when publishing it.
- [Connection setup](internal/hostcmd/connect_test.go): command order, mount paths,
  Firefox desktop/camera plugs, existing connections, and stopping on remount
  failure, using a fake Workshop command.
- [Host serving](internal/hostcmd/serve_test.go): handler filtering, capability
  locks, cancellation, and cleanup when a sibling fails.
- [Command dispatch](cmd/workshop-desktop/main_test.go): host and guest routing,
  help without host initialization, and invalid command exit codes.
- [Logging](internal/logging/logging_test.go): split output lines and flushing
  trailing child output.
- [SDK wrappers](tests/sdk_test.go): audio service environment, application
  arguments and exit status, guest opener forwarding, Firefox’s delayed bus
  switch and missing-socket check, and refusing installation without a built
  binary, using fake commands rather than installing services.

Tests do not open real host applications. Unix-socket tests skip when the sandbox
prohibits binding. Actual peer-credential extraction, D-Bus filtering, Firefox
installation and confinement, portal file access, and audio/camera behavior still
need live validation.

On 15 October 2026, all Go test packages passed in a fresh run with the local
Go 1.26.7 toolchain and a temporary module file targeting Go 1.26. HTML structure,
local documentation links/anchors, and all six SDK shell scripts’ syntax checks
also passed. This does not verify the committed Go 1.27 requirement. Workshop
still fails at `snap-confine`, so Go 1.27 and live desktop validation remain
outstanding. Earlier race checks were blocked by an unavailable C compiler.

## Apply changes

Rebuild after Go changes. For guest SDK changes, stop the helpers, build, run
`workshop refresh firefox`, reconnect with `bin/workshop-desktop host connect`,
then restart the helpers and Firefox. Go-only host changes take effect after
rebuilding and restarting the host helpers; refresh the guest as well when it
needs the updated shared binary. If audio clients fail to reconnect after
restarting the host helper:

```sh
workshop exec firefox -- systemctl --user restart workshop-pipewire-pulse
workshop exec firefox -- journalctl --user -u workshop-pipewire-pulse --no-pager -n 80
```
