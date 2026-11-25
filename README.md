# Desktop apps in Workshop

A Wayland-only experiment in sharing selected host desktop services
with a Workshop container. Use `dev` to build and test the bridge and `apps`
to run Firefox, Any nightly, and Codex. The shared desktop
integration provides a filtered session D-Bus connection,
PipeWire audio through a security-context socket, Workshop's desktop and camera
interfaces, and a guest `xdg-open` wrapper that opens URLs through the host desktop portal.

The [desktop integration guide](docs/desktop-integration.md) covers the
capability boundaries, host and guest lifecycle, SDK setup,
and [live acceptance checks](docs/desktop-integration.md#live-acceptance-checks).

## Build and run

Build the bridge before launching or refreshing the apps workshop:

```sh
workshop launch dev
workshop run dev -- build
```

See [development workflow](AGENTS.md#development-workflow) for build output,
testing, and how to apply source changes.

The host needs Workshop/LXD with nested Snap support, a Wayland session,
a working XDG Desktop Portal, `xdg-dbus-proxy`, `pw-container`, and `stdbuf`.
Host PipeWire must support SecurityContext and have a running session manager such as WirePlumber. See the [PipeWire socket requirements](docs/desktop-integration.md#audio).

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

The available handlers are `dbus` and `pipewire`. You can also run
them separately:

```sh
./bin/workshop-desktop host dbus
./bin/workshop-desktop host pipewire
```

Wait for the handlers to report ready, then launch apps from another terminal:

```sh
workshop run apps -- firefox https://example.com
workshop run apps -- any
workshop run apps -- codex  # ChatGPT desktop
workshop exec apps -- codex  # Codex CLI
```

To update Any, close the app and stop the host helpers. From the repository
root on the host, run the updater (requires `curl` and `jq`), then refresh
and reconnect:

```sh
.workshop/any/update.sh
workshop refresh apps
./bin/workshop-desktop host connect
```

Restart the host helpers and relaunch Any. The updater checks the
[Linux amd64 nightly release feed](https://s3.eu-central-1.amazonaws.com/anytype-anyui-nightly/desktop/any/latest.json)
and edits the version pin in `.workshop/any/hooks/setup-base` when it changes.

The [Codex desktop SDK](.workshop/codex-desktop) installs ChatGPT from OpenAI’s
signed APT repository. See the [guest SDK setup](docs/desktop-integration.md#guest-sdk-setup)
for install and launch behavior, and [Any state across refreshes](docs/desktop-integration.md#any-state-across-refreshes)
for data and settings.

## Open on the host

```sh
workshop exec apps -- xdg-open https://example.com
workshop exec apps -- xdg-open mailto:someone@example.com
```

Run the host D-Bus helper before opening URLs. The wrapper accepts a single
non-file URI and reports success when the portal returns a request handle.
See [host URL opening](docs/desktop-integration.md#host-url-opening) for details.

## Upgrade from the dedicated opener

When upgrading from the dedicated opener bridge, stop any standalone
`workshop-desktop host open` process before refreshing the apps workshop.
Use `host dbus` or `host serve` for URL opening after the upgrade.

Follow the [refresh workflow](AGENTS.md#apply-source-changes) after rebuilding.

## Troubleshoot audio

If audio clients fail to reconnect after restarting the host helper, restart
the guest service and check its log.

```sh
workshop exec apps -- systemctl --user restart workshop-pipewire-pulse
workshop exec apps -- journalctl --user -u workshop-pipewire-pulse --no-pager -n 80
```
