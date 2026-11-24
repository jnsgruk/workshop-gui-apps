# Desktop apps in Workshop

A Wayland-only experiment in sharing selected host desktop services
with a Workshop container. Use `dev` to build and test the bridge and `apps`
to run Firefox, Any nightly, and Codex. The shared desktop
integration provides a filtered session D-Bus connection,
PipeWire audio through a security-context socket, Workshop's desktop and camera
interfaces, and a guest `xdg-open` client that forwards requests to the host.

The [desktop integration guide](docs/desktop-integration.md) covers the
capability boundaries, host and guest lifecycle, SDK setup,
and [live acceptance checks](docs/desktop-integration.md#live-acceptance-checks).

## Build and run

The [development workshop](.workshop/dev.yaml) uses Go 1.27 and the standard
library to build one static `workshop-desktop` binary with host and guest subcommands:

```sh
workshop launch dev
workshop run dev -- build
```

Build writes `bin/workshop-desktop` and stages the same binary for the
[host integration setup hook](.workshop/host-integration/hooks/setup-base).
Build before launching or refreshing the [apps workshop](.workshop/apps.yaml).
The hook requires the staged binary. Its architecture must match the host and
guest. The runtime SDKs need neither Go nor Python.

The host needs Workshop/LXD with nested Snap support, a Wayland session,
`xdg-dbus-proxy`, `pw-container`, `stdbuf`, and `xdg-open`. Host PipeWire must support
SecurityContext and have a running session manager such as WirePlumber. See the [PipeWire socket requirements](docs/desktop-integration.md#audio).

```sh
workshop launch apps
./bin/workshop-desktop host connect
```

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

The available handlers are `dbus`, `pipewire`, and `open`. You can also run
them separately:

```sh
./bin/workshop-desktop host dbus
./bin/workshop-desktop host pipewire
./bin/workshop-desktop host open
```

Wait for the handlers to report ready, then launch apps from another terminal:

```sh
workshop run apps -- firefox https://example.com
workshop run apps -- any
workshop run apps -- codex  # ChatGPT desktop
workshop exec apps -- codex  # Codex CLI
```

Any setup requires access to the [Linux amd64 nightly release feed](https://s3.eu-central-1.amazonaws.com/anytype-anyui-nightly/desktop/any/latest.json).
The [Codex desktop SDK](.workshop/codex-desktop) installs ChatGPT from OpenAI’s
signed APT repository. See the [guest SDK setup](docs/desktop-integration.md#guest-sdk-setup)
for install and launch behavior, and [Any state across refreshes](docs/desktop-integration.md#any-state-across-refreshes)
for data and settings.

## Open on the host

```sh
workshop exec apps -- xdg-open https://example.com
workshop exec apps -- xdg-open mailto:someone@example.com
workshop exec apps -- xdg-open /project/README.md
```

You can also run `workshop-desktop guest open URL|FILE` directly in the guest.

Files must be shared under `/project`. For supported callers and how requests
are handled, see the [host opener](docs/desktop-integration.md#host-opener).

## Automated tests and limits

`workshop run dev -- fmt` formats the Go source. `workshop run dev -- test` runs
all Go tests, including shell-wrapper checks.

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

Use the [live acceptance checks](docs/desktop-integration.md#live-acceptance-checks)
to verify desktop behavior in a connected workshop.

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
