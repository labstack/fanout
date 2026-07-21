#!/usr/bin/env bash

set -euo pipefail

mode="${1:?usage: check-files.sh MODE [FILE ...]}"
shift

is_text_file() {
  local file="$1"
  local stat added deleted path

  if ! stat=$(git diff --cached --numstat -- "$file"); then
    printf 'failed to inspect staged file: %s\n' "$file" >&2
    return 2
  fi
  IFS=$'\t' read -r added deleted path <<< "$stat"
  [[ "$added" != "-" && "$deleted" != "-" ]]
}

skip_non_text() {
  local file="$1"
  local status
  if is_text_file "$file"; then
    return 1
  else
    status=$?
  fi
  if (( status == 1 )); then
    return 0
  fi
  exit "$status"
}

collect_go_packages() {
  go_packages=()
  local file dir package existing seen
  for file in "$@"; do
    dir=$(dirname "$file")
    package="./$dir"
    [[ "$dir" == "." ]] && package="."
    seen=false
    for existing in "${go_packages[@]}"; do
      if [[ "$existing" == "$package" ]]; then
        seen=true
        break
      fi
    done
    if [[ "$seen" == false ]]; then
      go_packages+=("$package")
    fi
  done
}

case "$mode" in
  trailing-whitespace)
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      skip_non_text "$file" && continue
      perl -pi -e 's/[ \t]+(?=\r?\n$)//' -- "$file"
    done
    ;;

  end-of-file)
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      skip_non_text "$file" && continue
      perl -0777 -pi -e 's/(?:\r?\n)*\z/\n/' -- "$file"
    done
    ;;

  yaml)
    bun -e '
      for (const file of Bun.argv.slice(1)) {
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
      skip_non_text "$file" && continue
      if grep -nE '^(<<<<<<< |>>>>>>> )' -- "$file"; then
        printf 'merge conflict marker found in %s\n' "$file" >&2
        status=1
      fi
    done
    exit "$status"
    ;;

  mixed-line-ending)
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      skip_non_text "$file" && continue
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

  go-vet)
    collect_go_packages "$@"
    CGO_ENABLED=1 go vet "${go_packages[@]}"
    ;;

  go-lint)
    collect_go_packages "$@"
    golangci-lint run "${go_packages[@]}"
    ;;

  *)
    printf 'unknown check mode: %s\n' "$mode" >&2
    exit 2
    ;;
esac
