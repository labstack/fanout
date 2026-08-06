## 1. Build the configuration loader

- [x] 1.1 Rename `internal/env` to `internal/config` and make the typed config declaration the source of YAML keys, `FANOUT_` names, and defaults. Evidence: `internal/config/config.go` tags and package imports across runtime consumers.
- [x] 1.2 Compose defaults, an optional explicit YAML document, and process environment in the specified order using Koanf. Evidence: `internal/config/loader.go` and `TestLoadLayering`.
- [x] 1.3 Reject unknown YAML keys and namespaced environment variables, return secret-safe errors, then resolve sizing and validate the effective config. Evidence: loader allowlists plus `TestLoadRejectsUnknownInputs`, `TestLoadRejectsInvalidFilesAndValues`, and `TestLoadErrorsDoNotContainSecrets`.
- [x] 1.4 Add `--config` with the standard flag package and keep process exit and startup logging in `cmd/fanout`. Evidence: `parseCommandLine`, `Config.LogStartup`, and `cmd/fanout/main_test.go`.

## 2. Cover the public contract

- [x] 2.1 Test defaults, YAML loading, environment precedence, absent config, missing/invalid files, unknown keys and variables, legacy-variable isolation, type errors, and invariant failures. Evidence: `internal/config/config_test.go`.
- [x] 2.2 Preserve and update sizing, security warning, and runtime consumer tests after the package rename. Evidence: `internal/config/sizing_test.go` and the green full Go suite.

## 3. Align operator documentation

- [x] 3.1 Add a complete commented `fanout.example.yaml` without embedded credential values. Evidence: `fanout.example.yaml` and `TestExampleConfigurationMatchesSchema`, which loads the file and checks every environment name is documented.
- [x] 3.2 Update README quick starts and configuration guidance for YAML, `--config`, and `FANOUT_` environment names. Evidence: README `Quick start`, `Configuration`, and `Advanced DuckDB sizing`.
- [x] 3.3 Update Docker defaults, benchmark reproduction commands, contribution guidance, and code comments to remove the old contract. Evidence: `Dockerfile`, `docs/benchmarks/two-vcpu.md`, `CONTRIBUTING.md`, `SECURITY.md`, and regenerated diagrams.

## 4. Verify

- [x] 4.1 Run gofmt and focused configuration/command tests. Evidence: `gofmt -w .` and green `go test ./internal/config ./cmd/fanout`.
- [x] 4.2 Run the full Go test suite and relevant static checks. Evidence: green `go test ./...`, `go vet ./...`, and `golangci-lint run`.
- [x] 4.3 Run strict OpenSpec validation and confirm no old configuration names or dotenv behavior remain in current operator guidance. Evidence: `openspec validate redesign-runtime-configuration --strict --no-interactive`, targeted repository searches, and the green `just check` gate.
