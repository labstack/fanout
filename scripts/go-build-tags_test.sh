#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/go-build-tags.sh"

GOFLAGS='-mod=readonly -tags=custom'
duckdb_build_tags go test -tags=integration ./...
[[ "$GOFLAGS" == ' -mod=readonly -tags=duckdb_use_static_lib,custom,integration' ]]
[[ "${duckdb_go_args[2]}" == '-tags=duckdb_use_static_lib,custom,integration' ]]

GOFLAGS="'-ldflags=-s -w' '-tags=custom other'"
duckdb_build_tags go test ./... -tags 'integration transportbench' -args -tags=runtime
[[ "$GOFLAGS" == " '-ldflags=-s -w' -tags=duckdb_use_static_lib,custom,other,integration,transportbench" ]]
[[ "${duckdb_go_args[4]}" == 'duckdb_use_static_lib,custom,other,integration,transportbench' ]]
[[ "${duckdb_go_args[6]}" == '-tags=runtime' ]]

GOFLAGS=''
duckdb_build_tags go run -ldflags '-s -w' -tags=custom ./cmd/fanout -tags=runtime
[[ "${duckdb_go_args[4]}" == '-tags=duckdb_use_static_lib,custom' ]]
[[ "${duckdb_go_args[6]}" == '-tags=runtime' ]]

GOFLAGS=''
duckdb_build_tags go -C /tmp test -tags=one -tags=two ./...
[[ "${duckdb_go_args[4]}" == '-tags=duckdb_use_static_lib,one,two' ]]
[[ "${duckdb_go_args[5]}" == '-tags=duckdb_use_static_lib,one,two' ]]

GOFLAGS='-tags=custom'
duckdb_build_tags golangci-lint run
[[ "$GOFLAGS" == ' -tags=duckdb_use_static_lib,custom' ]]
[[ "${duckdb_go_args[*]}" == 'golangci-lint run' ]]

GOFLAGS=''
duckdb_build_tags go test -tags=integration ./... -- -tags=runtime
[[ "${duckdb_go_args[5]}" == '-tags=runtime' ]]

GOFLAGS=''
duckdb_build_tags go test ./...
[[ "$GOFLAGS" == ' -tags=duckdb_use_static_lib' ]]

GOFLAGS=''
duckdb_build_tags go test -run '-tags=pattern' -tags=integration ./...
[[ "${duckdb_go_args[3]}" == '-tags=pattern' ]]
[[ "${duckdb_go_args[4]}" == '-tags=duckdb_use_static_lib,integration' ]]

GOFLAGS='--tags=custom'
duckdb_build_tags go test --tags=integration ./...
[[ "${duckdb_go_args[2]}" == '-tags=duckdb_use_static_lib,custom,integration' ]]
GOFLAGS=''
duckdb_build_tags go test --tags integration ./...
[[ "${duckdb_go_args[3]}" == 'duckdb_use_static_lib,integration' ]]

GOFLAGS="'-tags=unclosed"
if duckdb_build_tags go test ./... 2>/dev/null; then
	echo 'Accepted unterminated GOFLAGS quote.' >&2
	exit 1
fi
echo 'DuckDB build-tag tests passed.'
