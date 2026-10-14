#!/usr/bin/env bash
set -euo pipefail

# Read the same job token's core allowance without consuming its primary quota.
# This is a bounded preflight; other consumers can still use allowance afterward.
maximum=${PAGES_QUOTA_MAX_WAIT_SECONDS:-3900}
if [[ $# != 0 || ! "$maximum" =~ ^[0-9]{1,4}$ ]] || (( 10#$maximum < 1 || 10#$maximum > 3900 )); then
  printf 'Invalid Pages quota wait bound.\n' >&2
  exit 1
fi
maximum=$((10#$maximum))
deadline=$(($(date +%s) + maximum))
while true; do
  quota=$(gh api rate_limit --jq '.resources.core | [.remaining, .reset] | @tsv')
  IFS=$'\t' read -r remaining reset extra <<< "$quota"
  if [[ ! "$remaining" =~ ^[0-9]{1,10}$ || ! "$reset" =~ ^[0-9]{1,10}$ || -n "$extra" ]]; then
    printf 'Invalid GitHub quota response; refusing deployment.\n' >&2
    exit 1
  fi
  remaining=$((10#$remaining))
  reset=$((10#$reset))
  if (( remaining >= 100 )); then
    printf 'Pages quota ready: %d core requests remain.\n' "$remaining"
    exit 0
  fi
  now=$(date +%s)
  delay=$((reset - now + 5))
  if (( delay < 1 )); then delay=60; fi
  if (( now + delay > deadline )); then
    printf 'GitHub quota cannot recover within the Pages wait bound; preserve the current site.\n' >&2
    exit 1
  fi
  printf 'Pages quota low (%d remaining); waiting %d seconds for reset before rechecking.\n' "$remaining" "$delay"
  sleep "$delay"
done
