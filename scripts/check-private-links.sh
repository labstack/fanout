#!/usr/bin/env bash
# Fails when tracked files, commit messages or pull request text name a private
# AI session link or a local workstation path. This repository is public.
#
#   scripts/check-private-links.sh [<base> <head>]
#
# With <base> and <head>, commit messages in base..head are checked too.
# PR_TEXT, when set, is the pull request title and description.
set -euo pipefail

pattern='claude\.ai/(code|chat|share|project)|Claude-Session:|chatgpt\.com/(c|share|codex)/|/Users/[A-Za-z0-9_.-]+/|/home/[A-Za-z0-9_.-]+/'
found=0

if git grep -nIE "$pattern" -- . ':!scripts/check-private-links.sh'; then
  echo "error: tracked files name a private session link or local path" >&2
  found=1
fi

if [[ $# -eq 2 ]]; then
  while read -r sha; do
    if git log -1 --format=%B "$sha" | grep -qE "$pattern"; then
      echo "error: commit $sha message names a private session link or local path" >&2
      found=1
    fi
  done < <(git rev-list "$1..$2")
fi

if [[ -n "${PR_TEXT:-}" ]] && grep -qE "$pattern" <<<"$PR_TEXT"; then
  echo "error: pull request title or description names a private session link or local path" >&2
  found=1
fi

exit "$found"
