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

#### Scenario: Empty environment value
- **WHEN** a recognized `FANOUT_` variable is present with an empty value
- **THEN** Fanout treats it as absent and preserves the lower-precedence value

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
Only documented `FANOUT_` environment variables SHALL configure Fanout. Other
process environment variables SHALL be ignored. Unknown variables within the
`FANOUT_` namespace SHALL be startup errors except for standard
service-discovery variables injected by Kubernetes and Docker link-style
networking.

#### Scenario: Recognized override
- **WHEN** a documented `FANOUT_` variable is present with a valid value
- **THEN** Fanout applies it to its corresponding setting

#### Scenario: Unknown namespaced variable
- **WHEN** the process environment contains an unknown name beginning with `FANOUT_`
- **THEN** Fanout exits unsuccessfully and identifies the unknown variable by name

#### Scenario: Platform-injected service variables
- **WHEN** Kubernetes or Docker link-style networking injects standard service-discovery variables for a Service named `fanout`
- **THEN** Fanout ignores those platform-owned variables rather than treating them as configuration typos

#### Scenario: Environment variable outside the namespace
- **WHEN** a process environment variable does not begin with `FANOUT_`
- **THEN** it does not affect Fanout configuration or startup

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

#### Scenario: Null YAML value
- **WHEN** a recognized YAML leaf has a null value
- **THEN** Fanout exits unsuccessfully and identifies the null key instead of replacing its default with a zero value

#### Scenario: Empty YAML section
- **WHEN** a recognized YAML section is empty or contains only comments
- **THEN** Fanout accepts the section and preserves defaults for its leaves

#### Scenario: Strict scalar representation
- **WHEN** YAML supplies a fractional number for an integer or a non-boolean scalar for a boolean
- **THEN** Fanout rejects the value rather than weakly converting it

#### Scenario: Invalid merged configuration
- **WHEN** individually parsed settings produce an invalid combination after precedence is applied
- **THEN** Fanout exits unsuccessfully with a validation error before startup side effects

#### Scenario: Explicit empty authentication mode
- **WHEN** YAML explicitly sets `auth.mode` to an empty string
- **THEN** Fanout rejects it instead of silently selecting an authentication mechanism

### Requirement: Secret-safe diagnostics
Fanout SHALL NOT include configured credential values in startup logs or
configuration error messages.

#### Scenario: Invalid configuration contains credentials
- **WHEN** configuration validation fails after an API key, password, client secret, token, or authentication secret has been supplied
- **THEN** emitted logs and errors do not contain the credential value

#### Scenario: YAML document contains credentials
- **WHEN** a selected YAML document contains a credential and is accessible by group or others
- **THEN** Fanout exits unsuccessfully and identifies the unsafe file mode without printing the credential
