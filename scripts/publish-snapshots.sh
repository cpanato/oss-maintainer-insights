#!/usr/bin/env bash
set -euo pipefail
script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/snapshot-publication-common.sh
source "$script_directory/snapshot-publication-common.sh"
: "${SOURCE_REVISION:?SOURCE_REVISION is required}"
: "${DATA_BASE_REVISION?DATA_BASE_REVISION must be set, including for a cold start}"
snapshot_validate_revision "$SOURCE_REVISION"
if [[ -n "$DATA_BASE_REVISION" ]]; then snapshot_validate_revision "$DATA_BASE_REVISION"; fi

snapshot_read_refs
if [[ "$snapshot_main" != "$SOURCE_REVISION" || "$snapshot_data" != "$DATA_BASE_REVISION" ]]; then
  printf 'Source or data advanced since collection; discard this publication.\n'
  snapshot_output current false
  exit 0
fi

worktree=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/snapshot-data.XXXXXX")
trap 'git worktree remove --force "$worktree" >/dev/null 2>&1 || true' EXIT
if [[ -n "$DATA_BASE_REVISION" ]]; then
  snapshot_git fetch "$snapshot_remote" refs/heads/data
  if [[ "$(git rev-parse FETCH_HEAD)" != "$DATA_BASE_REVISION" ]]; then
    printf 'Data advanced while fetching; discard this publication.\n'
    snapshot_output current false
    exit 0
  fi
  git worktree add --detach "$worktree" "$DATA_BASE_REVISION"
else
  git worktree add --detach "$worktree" "$SOURCE_REVISION"
  git -C "$worktree" switch --orphan snapshot-data
fi

mkdir -p "$worktree/snapshots" "$worktree/checkpoints"
rsync -ac --delete "${PUBLIC_DATA_DIRECTORY:-generated-data}/" "$worktree/snapshots/"
rsync -ac --delete "${CHECKPOINT_DIRECTORY:-.cache/snapshots}/" "$worktree/checkpoints/"
printf '%s\n' "$SOURCE_REVISION" > "$worktree/source-revision.txt"
git -C "$worktree" add snapshots checkpoints source-revision.txt
if ! git -C "$worktree" diff --cached --quiet; then
  git -C "$worktree" -c user.name='github-actions[bot]' \
    -c user.email='41898282+github-actions[bot]@users.noreply.github.com' \
    -c commit.gpgsign=false commit -m 'data: refresh repository snapshots'
fi
revision=$(git -C "$worktree" rev-parse HEAD)

snapshot_read_refs
if [[ "$snapshot_main" != "$SOURCE_REVISION" || "$snapshot_data" != "$DATA_BASE_REVISION" ]]; then
  printf 'Source or data advanced while preparing the commit; discard this publication.\n'
  snapshot_output current false
  exit 0
fi
# The exact lease also protects the interval between the ref check and the push.
if ! snapshot_git -C "$worktree" push \
  "--force-with-lease=refs/heads/data:$DATA_BASE_REVISION" \
  "$snapshot_remote" HEAD:refs/heads/data; then
  snapshot_read_refs
  if [[ "$snapshot_data" != "$DATA_BASE_REVISION" ]]; then
    printf 'A concurrent data update won the lease; preserve it.\n'
    snapshot_output current false
    exit 0
  fi
  printf 'Data push failed without a concurrent update.\n' >&2
  exit 1
fi

snapshot_read_refs
if [[ "$snapshot_main" != "$SOURCE_REVISION" || "$snapshot_data" != "$revision" ]]; then
  printf 'Revisions advanced after publication; do not deploy this artifact.\n'
  snapshot_output current false
  exit 0
fi
snapshot_output revision "$revision"
snapshot_output current true
