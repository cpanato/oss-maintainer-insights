#!/usr/bin/env bash
set -euo pipefail
script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/snapshot-publication-common.sh
source "$script_directory/snapshot-publication-common.sh"
snapshot_validate_revision "${SOURCE_REVISION:?SOURCE_REVISION is required}"
snapshot_validate_revision "${DATA_REVISION:?DATA_REVISION is required}"
snapshot_read_refs
if [[ "$snapshot_main" == "$SOURCE_REVISION" && "$snapshot_data" == "$DATA_REVISION" ]]; then
  snapshot_output current true
else
  printf 'Source or data advanced during the build; skip this Pages deployment.\n'
  snapshot_output current false
fi
