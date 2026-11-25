# Preface

Guidance for adding or changing desktop app SDKs, guest launch actions, and
host desktop entries under `.workshop`.

IMPORTANT: Read the `.kb/agents.md` file.


# Laws

## §1 - Register the SDK and action

- Give each app SDK a directory with a matching `name` in `sdk.yaml`. Add its
  project SDK to [apps.yaml](../apps.yaml) using the `project-<sdk>` name.
- Add an action in [apps.yaml](../apps.yaml) that starts the installed guest
  executable and forwards arguments with `"$@"`. Set any toolkit-specific
  Wayland options that belong to the guest launch in the action or its wrapper.

## §2 - Add host launcher metadata

- For apps that should appear in the host menu, put `launcher.json` and a PNG
  icon from the app package beside `sdk.yaml`.
- Set `name` to the host menu label, `action` to the key in `apps.yaml`, and
  `icon` to the PNG basename. The installer derives
  `workshop-apps-<sdk>.desktop` from the SDK directory. Keep that prefix to
  avoid colliding with native desktop entries.
- Use the optional `startup_wm_class` for the package's verified window
  identity. Check the packaged desktop entry and, when possible, observe the
  running Wayland app ID. A visible icon in app search only verifies the
  desktop entry. Check the dock and overview with a fresh app process.


# Concepts

## Existing examples

[Any](../any/launcher.json) uses an AppImage wrapper, [Firefox](../firefox/launcher.json)
uses a Snap wrapper, and [ChatGPT](../codex-desktop/launcher.json) maps SDK
`codex-desktop` to action `codex`. The ChatGPT action selects native Wayland
with `--ozone-platform=wayland` in [apps.yaml](../apps.yaml).

## Verification

After applying SDK changes, run `workshop run apps -- <action>` on a
connected host. Exercise `workshop-desktop host launchers install <sdk>`,
launch from app search, and check the icon and grouping in the dock and
overview. Use `workshop-desktop host launchers remove <sdk>` to verify cleanup.
See the [development workflow](../../.kb/desktop-development.md#development-workflow)
and [live acceptance checks](../../docs/desktop-integration.md#live-acceptance-checks).
