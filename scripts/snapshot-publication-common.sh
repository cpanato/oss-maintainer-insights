#!/usr/bin/env bash
# Shared ref checks use Git transport without consuming the REST API allowance.
snapshot_remote=${SNAPSHOT_REMOTE_URL:-https://github.com/${GH_REPO:?GH_REPO is required}.git}

snapshot_git() {
  git -c credential.helper='!gh auth git-credential' "$@"
}

snapshot_read_refs() {
  local refs revision ref
  refs=$(snapshot_git ls-remote "$snapshot_remote" refs/heads/main refs/heads/data)
  snapshot_main=''
  snapshot_data=''
  while read -r revision ref; do
    case "$ref" in
      refs/heads/main) snapshot_main=$revision ;;
      refs/heads/data) snapshot_data=$revision ;;
    esac
  done <<< "$refs"
}

snapshot_output() {
  printf '%s=%s\n' "$1" "$2" >> "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
}

snapshot_validate_revision() {
  if [[ ! "$1" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]]; then
    printf 'Invalid Git revision.\n' >&2
    return 1
  fi
}
