package config

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	koanfenv "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// LoadOptions selects the explicit configuration inputs. A nil Environ uses
// os.Environ; an empty slice deliberately supplies no environment variables.
type LoadOptions struct {
	Path    string
	Environ []string
}

type fieldSpec struct {
	key          string
	env          string
	defaultValue string
	hasDefault   bool
}

// Load resolves defaults, an optional YAML document, and process environment
// in that order, then sizes and validates the effective configuration.
func Load(options LoadOptions) (Config, error) {
	specs, err := configFieldSpecs()
	if err != nil {
		return Config{}, err
	}

	knownKeys := make(map[string]struct{}, len(specs))
	keyByEnv := make(map[string]string, len(specs))
	defaults := make(map[string]any)
	for _, spec := range specs {
		knownKeys[spec.key] = struct{}{}
		keyByEnv[spec.env] = spec.key
		if spec.hasDefault {
			defaults[spec.key] = spec.defaultValue
		}
	}

	k := koanf.New(".")
	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return Config{}, fmt.Errorf("load configuration defaults: %w", err)
	}

	if options.Path != "" {
		fromFile := koanf.New(".")
		if err := fromFile.Load(file.Provider(options.Path), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load config %q: %w", options.Path, err)
		}
		if unknown := unknownKeys(fromFile.Keys(), knownKeys); len(unknown) > 0 {
			return Config{}, fmt.Errorf("config %q contains unknown keys: %s", options.Path, strings.Join(unknown, ", "))
		}
		if err := k.Merge(fromFile); err != nil {
			return Config{}, fmt.Errorf("merge config %q: %w", options.Path, err)
		}
	}

	environ := options.Environ
	if environ == nil {
		environ = os.Environ()
	}
	if unknown := unknownEnvironment(environ, keyByEnv); len(unknown) > 0 {
		return Config{}, fmt.Errorf("unknown Fanout environment variables: %s", strings.Join(unknown, ", "))
	}
	if err := k.Load(koanfenv.Provider(".", koanfenv.Opt{
		Prefix: "FANOUT_",
		EnvironFunc: func() []string {
			return environ
		},
		TransformFunc: func(name, value string) (string, any) {
			return keyByEnv[name], value
		},
	}), nil); err != nil {
		return Config{}, fmt.Errorf("load environment configuration: %w", err)
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{Tag: "koanf", FlatPaths: true}); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}

	// Size before validating: validation should judge the configuration the
	// process will actually run with, not the holes left for it to fill.
	cfg.resolveSizing()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate configuration: %w", err)
	}
	return cfg, nil
}

func configFieldSpecs() ([]fieldSpec, error) {
	typ := reflect.TypeOf(Config{})
	specs := make([]fieldSpec, 0, typ.NumField())
	keys := make(map[string]string, typ.NumField())
	envs := make(map[string]string, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !field.IsExported() {
			continue
		}
		key := field.Tag.Get("koanf")
		envName := field.Tag.Get("env")
		if key == "" || envName == "" {
			return nil, fmt.Errorf("configuration field %s must declare koanf and env tags", field.Name)
		}
		if previous, exists := keys[key]; exists {
			return nil, fmt.Errorf("configuration fields %s and %s share key %q", previous, field.Name, key)
		}
		if previous, exists := envs[envName]; exists {
			return nil, fmt.Errorf("configuration fields %s and %s share environment variable %q", previous, field.Name, envName)
		}
		if !strings.HasPrefix(envName, "FANOUT_") {
			return nil, fmt.Errorf("configuration field %s environment variable %q lacks FANOUT_ prefix", field.Name, envName)
		}
		defaultValue, hasDefault := field.Tag.Lookup("default")
		specs = append(specs, fieldSpec{key: key, env: envName, defaultValue: defaultValue, hasDefault: hasDefault})
		keys[key] = field.Name
		envs[envName] = field.Name
	}
	return specs, nil
}

func unknownKeys(keys []string, known map[string]struct{}) []string {
	var unknown []string
	for _, key := range keys {
		if _, exists := known[key]; !exists {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func unknownEnvironment(environ []string, known map[string]string) []string {
	var unknown []string
	for _, assignment := range environ {
		name, _, _ := strings.Cut(assignment, "=")
		if !strings.HasPrefix(name, "FANOUT_") {
			continue
		}
		if _, exists := known[name]; !exists {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}
