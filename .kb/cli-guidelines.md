# Preface

Use these conventions when changing `workshop-desktop` or its shell wrappers. They summarize Canonical CLI standard DE013 for this project.

IMPORTANT: Read the `.kb/agents.md` file.


# Laws

## §1 - Commands and arguments

- Group commands by workflow. Keep one command level beneath `host` and `guest`. Use verbs for actions and add a noun when it clarifies the object, as in `install-launcher`. Prefer `list` for primary objects and a plural noun for listing secondary objects. Use `status` to show current state.
- Use positional arguments when their role and order are obvious. Use descriptive double-dash flags for modifiers. Reserve single-letter options for frequent, obvious actions, allow them to be combined where applicable, and give each option one documented spelling.
- Accept whitespace between a flag and its value. Equals syntax is optional. Make flag order immaterial. For multiple values, use a repeatable singular flag such as `--workshop NAME` or a plural flag with a comma-separated list such as `--handlers dbus,pipewire`.
- Require `--` before an executable and pass every subsequent argument verbatim, including empty strings and flag-like arguments.

```sh
workshop-desktop host start --workshop apps --handlers dbus,pipewire
workshop-desktop guest launch --session-bus local -- my-app --help
```

## §2 - Help and feedback

- Register commands, flags, and descriptions in the shared parser. Support `--help`, `help [group [command]]`, and `version`. Resolve help and validate arguments before loading project state, probing hardware, or starting subprocesses.
- Send requested help, version, and normal results to stdout. Send errors, warnings, and foreground progress to stderr. Keep child streams separate and preserve the launched program’s exit status. Use status 2 for CLI usage errors and 1 for ordinary CLI operational failures.
- Use concise, specific messages and consistent desktop terminology. Prefix errors with `error:`, identify the invalid value or missing input, and give a scoped help hint for usage errors. Use “cannot” for operation failures and avoid contractions.
- Default to brief feedback, including warnings. Quiet shows only CLI errors and preserves application output. Verbose adds structured diagnostics with ISO 8601 timestamps. Reject conflicting verbosity options regardless of order.
- Keep output plain by default. If adding color, check the destination stream’s terminal capabilities and honor `NO_COLOR`. Use text to convey meaning and only rewrite progress lines on an interactive terminal.

## §3 - Table output

For new tabular commands, use aligned columns with headers enabled by default and a `--no-headers` option. Put identifying fields first and an optional notes column last. Keep rows near 80 columns when practical. For an empty interactive result, write a short message to stderr and exit successfully. For machine-readable output, emit the format’s empty result and omit the message.

## §4 - Verification

Test stdout, stderr, and exit status separately. Cover help and validation without desktop initialization, flag ordering, and exact argument forwarding. See the [development workflow](desktop-development.md#testing) and [live acceptance checks](../docs/desktop-integration.md#live-acceptance-checks).
