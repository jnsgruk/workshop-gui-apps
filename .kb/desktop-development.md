# Preface

Project-wide development workflow, Go conventions, and verification for the
Workshop desktop bridge.

IMPORTANT: Read the `.kb/agents.md` file.


# Laws

## §1 - Commits

Always use Conventional Commits for Git commit messages. Write subjects as
`type(scope): description` or `type: description` when a scope adds no context.
Use `!` before the colon or a `BREAKING CHANGE:` footer for breaking changes.

## §2 - Go style

Separate standard-library imports from project imports with a blank line.
Handle errors according to their cause. Preserve the underlying error when
adding context, and distinguish expected conditions from unexpected failures.

Use the innermost test or subtest's `t.Context()` for test-scoped work. Pass
context explicitly as the first parameter to helpers that need it. Prefer
`slices.Equal` and `maps.Equal` for simple collection comparisons when nil and
empty collections are equivalent.

## §3 - Host subprocesses and logging

Use context-aware `log/slog` calls for host diagnostics. Keep CLI help on its
designated output stream. Give subprocess stdout and stderr separate log
writers, and flush trailing output after the subprocess finishes. Use
`logging.NewSubprocessOutput` for paired streams with stderr captured for
error messages. Oversized lines are split into records with at most 64 KiB
of message data.

Use `exec.CommandContext` with `WaitDelay` for host commands that capture child
output. PipeWire reads its socket announcement from the first stdout line and
logs subsequent lines as diagnostics. Preserve the distinction between EOF,
read errors, and malformed announcements.

## §4 - Control flow

- Use early returns for errors and guard clauses for cases that can exit or
  skip an iteration. Keep the main path at the lowest practical indentation.
- Extract a named operation when it lets the caller read the workflow in order.
  Keep lock scope, cleanup order, and side effects visible where they are
  coordinated.
- Represent distinct outcomes explicitly when callers handle them differently.
  For example, a launcher can be missing, owned by this project, or owned by
  another installation. Return I/O errors separately. Keep related decisions
  together, such as a mount repair and whether it needs a new connection.
- During review, look for nested loops and conditionals, repeated checks, and
  flags whose meaning depends on earlier branches. Use nesting depth and
  cyclomatic complexity as review signals. Favor the version that makes the
  behavior easiest to trace, including failure and cancellation paths.


# Concepts

## Development workflow

The [development workshop](../.workshop/dev.yaml) uses Go 1.27 to build one
static `workshop-desktop` binary with host and guest subcommands. The CLI parser
uses the `github.com/canonical/go-flags` module.

```sh
workshop launch dev
workshop run dev -- build
```

Build writes `bin/workshop-desktop` and stages the same binary for the
[host integration setup hook](../.workshop/host-integration/hooks/setup-base).
Build before launching or refreshing the [apps workshop](../.workshop/apps.yaml).
Set `WORKSHOP_DESKTOP_VERSION` in the build action environment to embed a
release version. The default is `dev`. Run `bin/workshop-desktop version` to
check the staged build. The hook requires the staged binary. Its architecture
must match the host and guest. The runtime SDKs need neither Go nor Python.

## Apply source changes

Rebuild after Go changes. For guest SDK changes, stop the helpers, build, run
`workshop refresh apps`, reconnect with `bin/workshop-desktop host connect`,
then restart the helpers and relaunch your apps. Go-only host changes take
effect after rebuilding and restarting the host helpers. When changing the CLI
and guest wrappers together, build first and refresh `apps` to install the
staged binary and wrappers together.

## Testing

`workshop run dev -- fmt` formats the Go source with golangci-lint’s bundled gofumpt.
`workshop run dev -- lint` runs golangci-lint with its standard linters and checks
gofumpt formatting. Both actions use the same pinned golangci-lint version and
download it as needed. `workshop run dev -- test` runs all Go tests, including
shell-wrapper checks. Use `go fix -diff ./...` in the dev workshop to preview
standard-library and language modernization suggestions.

- Host lifecycle tests cover capability locks, symlink and file rejection,
  child cancellation, unexpected exits, and structured output. [Tests](../internal/host/host_test.go)
- PipeWire tests check that publishing the socket preserves its inode and
  rejects non-sockets. [Tests](../internal/pipewire/socket_test.go)
- Connection tests use a fake Workshop command to check mount paths, plug
  connections, command order, failed remounts, and inherited output pipes. [Tests](../internal/hostcmd/connect_test.go)
- Host serving tests cover handler selection, locks, cancellation, and cleanup
  when a sibling fails. [Tests](../internal/hostcmd/serve_test.go)
- CLI tests cover routing, help, and exit codes. [Tests](../cmd/workshop-desktop/main_test.go)
- Logging tests cover split lines, trailing child output, and oversized records. [Tests](../internal/logging/logging_test.go)
- Guest launch tests observe the session bus at Snap startup and after Firefox’s
  bus switch. They also check host-mode launches and missing prerequisites. [Tests](../internal/guestcmd/launch_test.go)
- Host endpoint tests exercise D-Bus proxy startup, PipeWire socket publication,
  early helper exits, invalid PipeWire announcements, and cleanup using
  temporary Unix sockets. [Tests](../internal/hostcmd/endpoints_test.go)
- SDK tests cover audio setup, URL routing and errors, the build prerequisite,
  and repeated installation with a real package diversion in a disposable root. [Tests](../tests/sdk_test.go)

Tests do not open real host applications. D-Bus filtering, Firefox installation and
confinement, host URL handlers, and audio and camera behavior need live
validation.

Use the [live acceptance checks](../docs/desktop-integration.md#live-acceptance-checks)
to verify desktop behavior in a connected workshop.
