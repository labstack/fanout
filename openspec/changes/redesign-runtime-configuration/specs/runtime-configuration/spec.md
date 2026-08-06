## Purpose

Defines the public contract by which operators configure Fanout predictably
across local binaries, process supervisors, and container platforms.

## ADDED Requirements

### Requirement: Configuration sources and precedence
Fanout SHALL resolve runtime configuration from built-in defaults, an optional
explicitly selected YAML document, and `FANOUT_` process environment variables,
in that ascending precedence order.

#### Scenario: Defaults without a document
- **WHEN** Fanout starts without `--config` and without a recognized `FANOUT_` override
- **THEN** it uses the documented built-in defaults

#### Scenario: YAML overrides a default
- **WHEN** a selected YAML document supplies a recognized setting
- **THEN** the YAML value replaces the built-in default for that setting

#### Scenario: Environment overrides YAML
- **WHEN** both the selected YAML document and its recognized `FANOUT_` environment variable supply a setting
- **THEN** the environment value is effective

### Requirement: Explicit configuration document
Fanout SHALL read a YAML configuration document only when its path is supplied
with `--config` and SHALL NOT discover configuration or dotenv files from the
working directory.

#### Scenario: Selected document is loaded
- **WHEN** Fanout is invoked with `--config /path/to/fanout.yaml`
- **THEN** it loads exactly that document before resolving environment overrides

#### Scenario: Selected document cannot be read
- **WHEN** the `--config` path is missing, unreadable, or not valid YAML
- **THEN** Fanout exits unsuccessfully before opening data files or network listeners

#### Scenario: Dotenv files are present
- **WHEN** `.env` or `.env.<name>` exists in Fanout's working directory but is not the selected `--config` document
- **THEN** its contents do not affect runtime configuration

### Requirement: Namespaced environment contract
Only documented `FANOUT_` environment variables SHALL configure Fanout. The
previous unprefixed names SHALL NOT act as aliases, and unknown variables within
the `FANOUT_` namespace SHALL be startup errors.

#### Scenario: Recognized override
- **WHEN** a documented `FANOUT_` variable is present with a valid value
- **THEN** Fanout applies it to its corresponding setting

#### Scenario: Unknown namespaced variable
- **WHEN** the process environment contains an unknown name beginning with `FANOUT_`
- **THEN** Fanout exits unsuccessfully and identifies the unknown variable by name

#### Scenario: Legacy variable remains present
- **WHEN** only a previous unprefixed environment variable is present
- **THEN** it does not affect the resolved configuration

### Requirement: Strict typed validation
Fanout SHALL reject unknown YAML keys, values that cannot be converted to the
documented type, and merged configurations that violate runtime invariants.
Validation SHALL complete before Fanout opens data files or network listeners.

#### Scenario: Unknown YAML key
- **WHEN** a selected document contains a key outside the documented schema
- **THEN** Fanout exits unsuccessfully and identifies the unknown key

#### Scenario: Invalid typed value
- **WHEN** YAML or environment supplies a value that cannot be converted to the setting's documented type
- **THEN** Fanout exits unsuccessfully and identifies the setting without starting the server

#### Scenario: Invalid merged configuration
- **WHEN** individually parsed settings produce an invalid combination after precedence is applied
- **THEN** Fanout exits unsuccessfully with a validation error before startup side effects

### Requirement: Secret-safe diagnostics
Fanout SHALL NOT include configured credential values in startup logs or
configuration error messages.

#### Scenario: Invalid configuration contains credentials
- **WHEN** configuration validation fails after an API key, password, client secret, token, or authentication secret has been supplied
- **THEN** emitted logs and errors do not contain the credential value
