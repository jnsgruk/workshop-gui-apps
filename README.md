# Desktop apps in Workshop

A Wayland-only experiment in sharing selected host desktop services
with a Workshop container. Use `dev` to build and test the bridge and `apps`
to run Firefox, Any nightly, and Codex. The shared desktop
integration provides a filtered session D-Bus connection,
PipeWire audio through a security-context socket, Workshop's desktop and camera
interfaces, and a guest `xdg-open` client that forwards requests to the host.

The [desktop integration guide](docs/desktop-integration.md) covers the
capability boundaries, host and guest lifecycle, and guest SDK audit. It also
records the [native interface proposal](docs/desktop-integration.md#native-interface-proposal)
and the [live acceptance checks](docs/desktop-integration.md#live-acceptance-checks).

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
[host integration setup hook](.workshop/host-integration/hooks/setup-base).
Build before launching or refreshing the [apps workshop](.workshop/apps.yaml).
The hook requires the staged binary. Its architecture must match the host and
guest. The runtime SDKs need neither Go nor Python.

The host needs Workshop/LXD with nested Snap support, a Wayland session,
`xdg-dbus-proxy`, `pw-container`, `stdbuf`, and `xdg-open`. Host PipeWire must support
SecurityContext and have a running session manager such as WirePlumber. Socket
publication uses `rename`, so the socket created by `pw-container` and the
`/tmp/workshop-desktop-<HOST-UID>-<PROJECT-ID>/pipewire/` destination must be on
the same filesystem; cross-filesystem publication fails.

```sh
workshop launch apps
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
with the same SDK and plug names as the apps definition. The default is `apps`.

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
workshop run apps -- firefox https://example.com
```

## Any nightly and Codex

The [`any` SDK](.workshop/any) downloads the latest Linux amd64 nightly when
Workshop installs or reinstalls the SDK. It installs the executable
at `/opt/any/Any.AppImage` inside the guest. Setup requires network access to
the [nightly release feed](https://s3.eu-central-1.amazonaws.com/anytype-anyui-nightly/desktop/any/latest.json).
An incremental refresh can reuse the existing SDK without downloading a new
build. See the [Any persistence findings](docs/desktop-integration.md#any-state-across-refreshes)
before refreshing an installation with local data.

After connecting `apps` and starting the host handlers, run:

```sh
workshop run apps -- any
workshop run apps -- codex
workshop exec apps -- codex  # Codex CLI
```

The [Codex desktop SDK](.workshop/codex-desktop) installs the `chatgpt` package
from OpenAI’s signed APT repository. The `codex` action starts ChatGPT desktop.
See the [official Linux desktop instructions](https://learn.chatgpt.com/docs/linux/linux-app).

Arguments after `workshop run apps -- any` are passed to the application.
The launcher validates the Wayland connection and requires host D-Bus and
PipeWire sockets. It selects Wayland explicitly, uses
`--appimage-extract-and-run` to run without a FUSE device, and retains the
application's sandbox defaults. These launch instructions require live host
validation.

## Parts of the experiment

The [host-integration SDK](.workshop/host-integration/sdk.yaml) provides the
shared desktop and GPU plugs and three socket mounts. Its
[setup hook](.workshop/host-integration/hooks/setup-base) groups guest setup
by capability.

| Capability | Guest setup | Host implementation |
| --- | --- | --- |
| D-Bus | Mount the filtered session bus. Apps opt in and keep their local buses. | [D-Bus proxy and allowlist](internal/hostcmd/dbus.go) |
| Audio | Install `pipewire-pulse`, the compatibility service, and its launcher wrapper. | [PipeWire helper](internal/hostcmd/pipewire.go), [socket publication](internal/pipewire/socket.go) |
| Host opening | Install the guest client and divert `/usr/bin/xdg-open`. | [Host server](internal/hostcmd/open.go), [guest CLI](internal/guestcmd/open.go), [protocol and launcher](internal/opener/opener.go) |

The [Firefox SDK](.workshop/firefox) owns the Firefox install, profile,
camera plug, and Snap-specific setup. Firefox and the AppImage launcher both
use the shared host integration.

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
workshop exec apps -- xdg-open https://example.com
workshop exec apps -- xdg-open mailto:someone@example.com
workshop exec apps -- xdg-open /project/README.md
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
  the shared desktop plug, the Firefox camera plug, and stopping on remount
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

The test suite passes with Go 1.27.4. See the
[live acceptance checks](docs/desktop-integration.md#live-acceptance-checks)
for the remaining desktop validation.

## Apply changes

Rebuild after Go changes. For guest SDK changes, stop the helpers, build, run
`workshop refresh apps`, reconnect with `bin/workshop-desktop host connect`,
then restart the helpers and relaunch your apps. Go-only host changes take
effect after rebuilding and restarting the host helpers. Refresh the guest
when it needs the updated shared binary. If audio clients fail to reconnect after
restarting the host helper:

```sh
workshop exec apps -- systemctl --user restart workshop-pipewire-pulse
workshop exec apps -- journalctl --user -u workshop-pipewire-pulse --no-pager -n 80
```

Existing `firefox` and `appimage` workshops are separate from the new `apps`
workshop. The new workshop does not automatically migrate their Firefox profiles.
