#!/usr/bin/env bash
# Preserve caller build tags while requiring the pinned DuckDB native binding.
duckdb_build_tags() {
	local remaining="${GOFLAGS:-}" flags='' tags='duckdb_use_static_lib'
	local token raw quote value i command='' run=false stop=false
	local positions=() prefixes=() position_count=0
	duckdb_go_args=("$@")
	while [[ -n "$remaining" ]]; do
		remaining="${remaining#"${remaining%%[!$' \t\r\n']*}"}"
		[[ -n "$remaining" ]] || break
		case "$remaining" in
		\'* | \"*)
			quote="${remaining:0:1}"
			remaining="${remaining:1}"
			if [[ "$remaining" != *"$quote"* ]]; then
				echo 'Unterminated quote in GOFLAGS.' >&2
				return 2
			fi
			token="${remaining%%"$quote"*}"
			raw="$quote$token$quote"
			remaining="${remaining#*"$quote"}"
			;;
		*)
			token="${remaining%%[$' \t\r\n']*}"
			raw="$token"
			remaining="${remaining:${#token}}"
			;;
		esac
		if [[ "$token" == -tags=* ]]; then
			value="${token#-tags=}"
			tags+=",${value// /,}"
		else
			flags+=" $raw"
		fi
	done
	# Go command-line flags override GOFLAGS. Merge explicit tags as well.
	if [[ "${duckdb_go_args[0]##*/}" == go ]]; then
		for ((i = 1; i < ${#duckdb_go_args[@]}; i++)); do
			token="${duckdb_go_args[i]}"
			if [[ -z "$command" ]]; then
				case "$token" in
				-C)
					((i += 1))
					continue
					;;
				-C=*) continue ;;
				*)
					command="$token"
					[[ "$command" != run ]] || run=true
					continue
					;;
				esac
			fi
			[[ "$stop" == false ]] || continue
			case "$token" in
			-- | -args) stop=true ;;
			-tags=*)
				tags+=",${token#-tags=}"
				positions+=("$i")
				prefixes+=("-tags=")
				((position_count += 1))
				;;
			-tags)
				((i += 1))
				if ((i >= ${#duckdb_go_args[@]})); then
					echo 'Missing value for -tags.' >&2
					return 2
				fi
				tags+=",${duckdb_go_args[i]// /,}"
				positions+=("$i")
				prefixes+=("")
				((position_count += 1))
				;;
			-C | -o | -p | -asmflags | -buildmode | -compiler | -covermode | -coverpkg | -gccgoflags | -gcflags | -installsuffix | -ldflags | -mod | -modfile | -overlay | -pgo | -pkgdir | -toolexec | -run | -bench | -fuzz | -skip | -benchtime | -blockprofile | -blockprofilerate | -count | -coverprofile | -cpu | -cpuprofile | -fuzztime | -fuzzminimizetime | -list | -memprofile | -memprofilerate | -mutexprofile | -mutexprofilefraction | -outputdir | -parallel | -shuffle | -timeout | -trace | -vet)
				((i += 1))
				;;
			-*) ;;
			*) [[ "$run" == false ]] || stop=true ;;
			esac
		done
		# Replace only build flags, never flags forwarded to a program/test binary.
		for ((i = 0; i < position_count; i++)); do
			duckdb_go_args[${positions[i]}]="${prefixes[i]}${tags// /,}"
		done
	fi
	export GOFLAGS="$flags -tags=${tags// /,}"
}
