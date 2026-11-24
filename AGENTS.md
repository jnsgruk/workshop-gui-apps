# Repository instructions

Always use Conventional Commits for Git commit messages. Write subjects as
`type(scope): description` or `type: description` when a scope adds no context.
Use `!` before the colon or a `BREAKING CHANGE:` footer for breaking changes.

## Development workflow

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

### Apply source changes

Rebuild after Go changes. For guest SDK changes, stop the helpers, build, run
`workshop refresh apps`, reconnect with `bin/workshop-desktop host connect`,
then restart the helpers and relaunch your apps. Go-only host changes take
effect after rebuilding and restarting the host helpers. Refresh the guest
when it needs the updated shared binary.

## Testing

`workshop run dev -- fmt` formats the Go source. `workshop run dev -- test` runs
all Go tests, including shell-wrapper checks.

- Host lifecycle tests cover capability locks, symlink and file rejection,
  child cancellation, unexpected exits, and structured output. [Tests](internal/host/host_test.go)
- PipeWire tests check that publishing the socket preserves its inode and
  rejects non-sockets. [Tests](internal/pipewire/socket_test.go)
- Connection tests use a fake Workshop command to check mount paths, plug
  connections, command order, and handling of a failed remount. [Tests](internal/hostcmd/connect_test.go)
- Host serving tests cover handler selection, locks, cancellation, and cleanup
  when a sibling fails. [Tests](internal/hostcmd/serve_test.go)
- CLI tests cover routing, help, and exit codes. [Tests](cmd/workshop-desktop/main_test.go)
- Logging tests cover split lines and trailing child output. [Tests](internal/logging/logging_test.go)
- Guest launch tests observe the session bus at Snap startup and after Firefox’s
  bus switch. They also check host-mode launches and missing prerequisites. [Tests](internal/guestcmd/launch_test.go)
- Host endpoint tests exercise D-Bus proxy startup, PipeWire socket publication,
  early helper exits, and cleanup using temporary Unix sockets. [Tests](internal/hostcmd/endpoints_test.go)
- SDK tests cover audio setup, URL routing and errors, the build prerequisite,
  and repeated installation with a real package diversion in a disposable root. [Tests](tests/sdk_test.go)

Tests do not open real host applications. D-Bus filtering, Firefox installation and
confinement, host URL handlers, and audio and camera behavior need live
validation.

Use the [live acceptance checks](docs/desktop-integration.md#live-acceptance-checks)
to verify desktop behavior in a connected workshop.
