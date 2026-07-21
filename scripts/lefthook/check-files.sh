#!/usr/bin/env bash

set -euo pipefail

mode="${1:?usage: check-files.sh MODE [FILE ...]}"
shift

is_text_file() {
  local file="$1"
  local added deleted

  read -r added deleted < <(git diff --cached --numstat -- "$file") || return 1
  [[ "$added" != "-" && "$deleted" != "-" ]]
}

case "$mode" in
  trailing-whitespace)
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      is_text_file "$file" || continue
      perl -pi -e 's/[ \t]+(?=\r?\n$)//' -- "$file"
    done
    ;;

  end-of-file)
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      is_text_file "$file" || continue
      perl -0777 -pi -e 's/(?:\r?\n)*\z/\n/' -- "$file"
    done
    ;;

  yaml)
    bun -e '
      for (const file of Bun.argv.slice(2)) {
        try {
          Bun.YAML.parse(await Bun.file(file).text());
        } catch (error) {
          console.error(`${file}: ${error.message}`);
          process.exitCode = 1;
        }
      }
    ' -- "$@"
    ;;

  merge-conflict)
    status=0
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      is_text_file "$file" || continue
      if grep -nE '^(<<<<<<< |=======|>>>>>>> )' -- "$file"; then
        printf 'merge conflict marker found in %s\n' "$file" >&2
        status=1
      fi
    done
    exit "$status"
    ;;

  mixed-line-ending)
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      is_text_file "$file" || continue
      perl -pi -e 's/\r\n?/\n/g' -- "$file"
    done
    ;;

  added-large-files)
    status=0
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      [[ "$(git diff --cached --name-only --diff-filter=A -- "$file")" == "$file" ]] || continue
      size=$(wc -c < "$file")
      if (( size > 1024000 )); then
        printf '%s is %d bytes (maximum is 1024000 bytes)\n' "$file" "$size" >&2
        status=1
      fi
    done
    exit "$status"
    ;;

  *)
    printf 'unknown check mode: %s\n' "$mode" >&2
    exit 2
    ;;
esac
