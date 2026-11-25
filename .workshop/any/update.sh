#!/bin/bash
set -euo pipefail

setup_hook=$(dirname -- "${BASH_SOURCE[0]}")/hooks/setup-base
nightly_base=https://s3.eu-central-1.amazonaws.com/anytype-anyui-nightly/desktop/any

if ! latest=$(curl --fail --silent --show-error --retry 3 --connect-timeout 15 \
  "$nightly_base/latest.json" |
  jq -ser --arg base "$nightly_base" '
    select(length == 1) | .[0]
    | .version as $version
    | select($version | strings | test("\\A[0-9]+\\.[0-9]+\\.[0-9]+-nightly\\.[0-9]+\\.[0-9]+\\z"))
    | select(.platforms["linux-x86_64"].url ==
        "\($base)/\($version)/linux-x86_64-Any%20Nightly_\($version)_amd64.AppImage")
    | $version
  '); then
  echo 'Could not read a supported Linux amd64 release from the Any nightly manifest.' >&2
  exit 1
fi

if ! current=$(grep '^any_version=' "$setup_hook") || [[ $current == *$'\n'* ]]; then
  echo 'Expected exactly one any_version assignment in the Any setup hook.' >&2
  exit 1
fi
current=${current#any_version=}
if [[ $current == "$latest" ]]; then
  printf 'Any nightly is already pinned to %s.\n' "$latest"
  exit 0
fi

updated_hook=$(mktemp "$setup_hook.XXXXXXXX")
trap 'rm -f -- "$updated_hook"' EXIT
sed "s/^any_version=.*/any_version=$latest/" "$setup_hook" > "$updated_hook"
chmod --reference="$setup_hook" "$updated_hook"
mv -fT -- "$updated_hook" "$setup_hook"
printf 'Updated the Any nightly pin from %s to %s.\n' "$current" "$latest"
