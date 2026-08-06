package config

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
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
	typ          reflect.Type
	defaultValue any
	hasDefault   bool
	secret       bool
}

// Load resolves defaults, an optional YAML document, and process environment
// in that order, then sizes and validates the effective configuration.
func Load(options LoadOptions) (Config, error) {
	specs, err := configFieldSpecs()
	if err != nil {
		return Config{}, err
	}

	specByKey := make(map[string]fieldSpec, len(specs))
	specByEnv := make(map[string]fieldSpec, len(specs))
	defaults := make(map[string]any)
	for _, spec := range specs {
		specByKey[spec.key] = spec
		specByEnv[spec.env] = spec
		if spec.hasDefault {
			defaults[spec.key] = spec.defaultValue
		}
	}

	k := koanf.New(".")
	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return Config{}, fmt.Errorf("load configuration defaults: %w", err)
	}

	if options.Path != "" {
		info, statErr := os.Stat(options.Path)
		if statErr != nil {
			return Config{}, fmt.Errorf("load config %q: %w", options.Path, statErr)
		}
		if !info.Mode().IsRegular() {
			return Config{}, fmt.Errorf("load config %q: not a regular file", options.Path)
		}
		fromFile := koanf.New(".")
		if err := fromFile.Load(file.Provider(options.Path), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load config %q: %w", options.Path, err)
		}
		if err := validateYAMLKeys(fromFile, specByKey); err != nil {
			return Config{}, fmt.Errorf("config %q: %w", options.Path, err)
		}
		if containsSecretValues(fromFile, specs) && info.Mode().Perm()&0o077 != 0 {
			return Config{}, fmt.Errorf("config %q contains secrets and must not be accessible by group or others (mode is %04o)", options.Path, info.Mode().Perm())
		}
		if err := k.Merge(fromFile); err != nil {
			return Config{}, fmt.Errorf("merge config %q: %w", options.Path, err)
		}
	}

	environ := options.Environ
	if environ == nil {
		environ = os.Environ()
	}
	overrides, err := environmentOverrides(environ, specByEnv)
	if err != nil {
		return Config{}, err
	}
	if err := k.Load(confmap.Provider(overrides, "."), nil); err != nil {
		return Config{}, fmt.Errorf("load environment configuration: %w", err)
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
		Tag:       "koanf",
		FlatPaths: true,
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook:       mapstructure.StringToTimeDurationHookFunc(),
			ErrorUnused:      true,
			WeaklyTypedInput: false,
		},
	}); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	cfg.MCPPublicURL = strings.TrimSpace(cfg.MCPPublicURL)

	// Size before validating: validation should judge the configuration the
	// process will actually run with, not the holes left for it to fill.
	cfg.resolveSizing()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate configuration: %w", err)
	}
	return cfg, nil
}

func configFieldSpecs() ([]fieldSpec, error) {
	return fieldSpecs(reflect.TypeOf(Config{}))
}

func fieldSpecs(typ reflect.Type) ([]fieldSpec, error) {
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("configuration schema must be a struct, got %s", typ)
	}
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
		defaultText, hasDefault := field.Tag.Lookup("default")
		var defaultValue any
		if hasDefault {
			var err error
			defaultValue, err = parseTextValue(defaultText, field.Type)
			if err != nil {
				return nil, fmt.Errorf("configuration field %s has invalid default: %w", field.Name, err)
			}
		}
		specs = append(specs, fieldSpec{
			key:          key,
			env:          envName,
			typ:          field.Type,
			defaultValue: defaultValue,
			hasDefault:   hasDefault,
			secret:       field.Tag.Get("secret") == "true",
		})
		keys[key] = field.Name
		envs[envName] = field.Name
	}
	return specs, nil
}

func validateYAMLKeys(k *koanf.Koanf, known map[string]fieldSpec) error {
	var unknown []string
	var emptyContainers []string
	for _, key := range k.Keys() {
		value := k.Get(key)
		if spec, exists := known[key]; exists {
			if value == nil {
				return fmt.Errorf("key %s must not be null", key)
			}
			if err := validateYAMLValue(value, spec.typ); err != nil {
				return fmt.Errorf("key %s %w", key, err)
			}
			continue
		}
		if isSchemaContainer(key, known) && isEmptyContainer(value) {
			emptyContainers = append(emptyContainers, key)
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) == 0 {
		for _, key := range emptyContainers {
			k.Delete(key)
		}
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("contains unknown keys: %s", strings.Join(unknown, ", "))
}

func isSchemaContainer(key string, known map[string]fieldSpec) bool {
	prefix := key + "."
	for candidate := range known {
		if strings.HasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

func validateYAMLValue(value any, typ reflect.Type) error {
	if typ == reflect.TypeOf(time.Duration(0)) {
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a duration string")
		}
		if _, err := time.ParseDuration(text); err != nil {
			return fmt.Errorf("must be a Go duration: %w", err)
		}
		return nil
	}
	kind := reflect.TypeOf(value).Kind()
	switch typ.Kind() {
	case reflect.String:
		if kind != reflect.String {
			return fmt.Errorf("must be a string")
		}
	case reflect.Bool:
		if kind != reflect.Bool {
			return fmt.Errorf("must be true or false")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if kind < reflect.Int || kind > reflect.Int64 {
			return fmt.Errorf("must be an integer")
		}
	default:
		return fmt.Errorf("uses unsupported configuration type %s", typ)
	}
	return nil
}

func isEmptyContainer(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	return (rv.Kind() == reflect.Map || rv.Kind() == reflect.Slice) && rv.Len() == 0
}

func containsSecretValues(k *koanf.Koanf, specs []fieldSpec) bool {
	for _, spec := range specs {
		if !spec.secret {
			continue
		}
		value := k.Get(spec.key)
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		return true
	}
	return false
}

func environmentOverrides(environ []string, known map[string]fieldSpec) (map[string]any, error) {
	overrides := make(map[string]any)
	var unknown []string
	for _, assignment := range environ {
		name, value, found := strings.Cut(assignment, "=")
		_, isKnown := known[name]
		if !found {
			if isKnown || strings.HasPrefix(name, "FANOUT_") {
				return nil, fmt.Errorf("environment entry %s must use NAME=value form", name)
			}
			continue
		}
		if !strings.HasPrefix(name, "FANOUT_") {
			continue
		}
		spec, exists := known[name]
		if !exists {
			if isPlatformInjectedEnvironment(name) {
				continue
			}
			unknown = append(unknown, name)
			continue
		}
		// An explicitly empty environment value means "no override", so it cannot
		// erase a safe default or silently disable a feature.
		if value == "" {
			continue
		}
		parsed, err := parseTextValue(value, spec.typ)
		if err != nil {
			return nil, fmt.Errorf("environment variable %s has an invalid value for %s: %w", name, spec.key, err)
		}
		overrides[spec.key] = parsed
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown Fanout environment variables: %s", strings.Join(unknown, ", "))
	}
	return overrides, nil
}

func isPlatformInjectedEnvironment(name string) bool {
	return name == "FANOUT_SERVICE_HOST" ||
		name == "FANOUT_SERVICE_PORT" ||
		strings.HasPrefix(name, "FANOUT_SERVICE_PORT_") ||
		name == "FANOUT_PORT" ||
		strings.HasPrefix(name, "FANOUT_PORT_")
}

func parseTextValue(value string, typ reflect.Type) (any, error) {
	if typ == reflect.TypeOf(time.Duration(0)) {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return nil, fmt.Errorf("must be a Go duration: %w", err)
		}
		return parsed, nil
	}
	switch typ.Kind() {
	case reflect.String:
		return value, nil
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("must be true or false: %w", err)
		}
		return parsed, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, typ.Bits())
		if err != nil {
			return nil, fmt.Errorf("must be an integer: %w", err)
		}
		result := reflect.New(typ).Elem()
		result.SetInt(parsed)
		return result.Interface(), nil
	default:
		return nil, fmt.Errorf("unsupported configuration type %s", typ)
	}
}
