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

See [development workflow](.kb/desktop-development.md#development-workflow) for build output,
testing, and how to apply source changes.

The host needs Workshop/LXD with nested Snap support, a Wayland session,
a working XDG Desktop Portal, `xdg-dbus-proxy`, `pw-container`, and `stdbuf`.
Host PipeWire must support SecurityContext and have a running session manager such as WirePlumber. See the [PipeWire socket requirements](docs/desktop-integration.md#audio).

```sh
workshop launch apps
```

For app menu launches, follow [Host app launchers](#host-app-launchers).
For terminal-driven launches, run `./bin/workshop-desktop host start` in a host graphical-session terminal, without sudo. It checks the
desktop integration for `apps`, repairs missing connections, and serves the
host endpoints in the foreground. If a mount needs repair, it may stop and
restart the workshop. Close guest GUI apps before running it after a mount
change. An already connected, running workshop keeps running.

Host commands default to the current directory, which must contain
`.workshop.lock` from a Workshop launch. Use `--project PATH` to select another
project or repeat `--workshop NAME` to prepare multiple launched workshops
with the same SDKs and plugs as `apps`.

Both `host start` and `host serve` accept a handler filter such as
`--handlers pipewire`. Use `host connect` for explicit reconnection and
`host serve` to run the helpers on their own.

The available handlers are `dbus` and `pipewire`. You can also run
them separately:

```sh
./bin/workshop-desktop host serve --handlers dbus
./bin/workshop-desktop host serve --handlers pipewire
```

Wait for the handlers to report ready, then launch apps from another terminal:

```sh
workshop run apps -- firefox https://example.com
workshop run apps -- any
workshop run apps -- codex  # ChatGPT desktop
workshop exec apps -- codex  # Codex CLI
```

Use `workshop-desktop help host start` for command help and
`workshop-desktop version` for the build version. Host operations report
concise progress by default. Use `--quiet` to show only errors from the CLI
or `--verbose` for structured diagnostics.

## Host app launchers

After building and launching the workshop, install the launchers you want as
your desktop user. Use `--all` to install every SDK with launcher metadata:

```sh
./bin/workshop-desktop host install-launcher --all
```

To install individual entries, pass the SDK name:

```sh
./bin/workshop-desktop host install-launcher any
./bin/workshop-desktop host install-launcher firefox
./bin/workshop-desktop host install-launcher codex-desktop
```

| SDK | App menu label | Desktop file |
| --- | --- | --- |
| [Any](.workshop/any/launcher.json) | Any (Workshop) | `workshop-apps-any.desktop` |
| [Firefox](.workshop/firefox/launcher.json) | Firefox (Workshop) | `workshop-apps-firefox.desktop` |
| [Codex desktop](.workshop/codex-desktop/launcher.json) | ChatGPT (Workshop) | `workshop-apps-codex-desktop.desktop` |

The installer writes entries under
`${XDG_DATA_HOME:-$HOME/.local/share}/applications` and copies the icons
shipped alongside the SDK metadata. The icons come from the Any nightly
AppImage, Firefox Snap, and ChatGPT Debian package. Each launcher runs its
existing `apps` action in this checkout. On GNOME Wayland, the first click
starts a shared bridge for the project and waits for D-Bus and PipeWire before
running the action. Concurrent clicks share startup. The bridge stays up after
the app closes and stops when GNOME ends the session or you run
`./bin/workshop-desktop host stop`. Startup errors appear as desktop
notifications when available. The error includes the project’s runtime log path
for supervisor failures.

If a foreground `host start` or `host serve` is already running, stop it before
switching to launcher-managed startup.

Run the same command again to update an entry. Use `host remove-launcher SDK`
to uninstall one entry or `host remove-launcher --all` to uninstall all entries
for this checkout. Before moving the checkout, remove the entries and reinstall
them from the new path.

On the target desktop, search for each installed app, launch it, and check
that its running window groups under the pinned launcher. Dock grouping
depends on how the desktop shell matches Wayland windows to launcher entries.
The installer rejects a filename already used by another installation in
the user's application directory or by a matching entry in the host's XDG
data directories.

## Update Any

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
Use `host serve --handlers dbus` or `host serve` for URL opening after the upgrade.

Follow the [refresh workflow](.kb/desktop-development.md#apply-source-changes) after rebuilding.

## Troubleshoot audio

If audio clients fail to reconnect after restarting the host helper, restart
the guest service and check its log.

```sh
workshop exec apps -- systemctl --user restart workshop-pipewire-pulse
workshop exec apps -- journalctl --user -u workshop-pipewire-pulse --no-pager -n 80
```
