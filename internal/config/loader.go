package config

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LoadOptions selects the environment. A nil Environ uses os.Environ;
// an empty slice deliberately supplies no environment variables.
type LoadOptions struct {
	Environ []string
}

type fieldSpec struct {
	index        int
	env          string
	typ          reflect.Type
	defaultValue any
	hasDefault   bool
	secret       bool
}

// Load resolves built-in defaults and process environment, then sizes and
// validates the effective configuration.
func Load(options LoadOptions) (Config, error) {
	specs, err := configFieldSpecs()
	if err != nil {
		return Config{}, err
	}
	known := make(map[string]bool, len(specs))
	var cfg Config
	value := reflect.ValueOf(&cfg).Elem()
	for _, spec := range specs {
		known[spec.env] = true
		if spec.secret {
			known[spec.env+"_FILE"] = true
		}
		if spec.hasDefault {
			value.Field(spec.index).Set(reflect.ValueOf(spec.defaultValue))
		}
	}
	environ := options.Environ
	if environ == nil {
		environ = os.Environ()
	}
	values, err := environmentValues(environ, known)
	if err != nil {
		return Config{}, err
	}
	for _, spec := range specs {
		// Shared vendor settings, including secret mounts, belong only to the
		// explicitly selected provider. Fanout-owned secrets always resolve.
		if provider := providerForEnvironment(spec.env); provider != "" && provider != values["FANOUT_AI_PROVIDER"] {
			continue
		}
		text := values[spec.env]
		if spec.secret {
			name := spec.env + "_FILE"
			if path := values[name]; path != "" {
				if text != "" {
					return Config{}, fmt.Errorf("%s and %s (%q) must not both be set", spec.env, name, path)
				}
				content, err := os.ReadFile(path)
				if err != nil {
					return Config{}, fmt.Errorf("read %s file %q: %w", name, path, err)
				}
				text = string(content)
				if strings.HasSuffix(text, "\r\n") {
					text = strings.TrimSuffix(text, "\r\n")
				} else {
					text = strings.TrimSuffix(text, "\n")
				}
				if text == "" {
					return Config{}, fmt.Errorf("%s file %q is empty", name, path)
				}
			}
		}
		if text == "" {
			continue
		}
		parsed, err := parseTextValue(text, spec.typ)
		if err != nil {
			return Config{}, fmt.Errorf("environment variable %s %w", spec.env, err)
		}
		value.Field(spec.index).Set(reflect.ValueOf(parsed))
	}
	// Validation judges the sizing the process will actually use.
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
	envs := make(map[string]string, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !field.IsExported() {
			continue
		}
		name := field.Tag.Get("env")
		if name == "" {
			return nil, fmt.Errorf("configuration field %s must declare an env tag", field.Name)
		}
		if !strings.HasPrefix(name, "FANOUT_") && providerForEnvironment(name) == "" {
			return nil, fmt.Errorf("environment variable %s is outside the supported configuration contract", name)
		}
		secret := field.Tag.Get("secret") == "true"
		names := []string{name}
		if secret {
			names = append(names, name+"_FILE")
		}
		for _, env := range names {
			if previous, exists := envs[env]; exists {
				return nil, fmt.Errorf("configuration fields %s and %s share environment variable %s", previous, field.Name, env)
			}
			envs[env] = field.Name
		}
		defaultText, hasDefault := field.Tag.Lookup("default")
		var defaultValue any
		if hasDefault {
			var err error
			defaultValue, err = parseTextValue(defaultText, field.Type)
			if err != nil {
				return nil, fmt.Errorf("environment variable %s has invalid default: %w", name, err)
			}
		}
		specs = append(specs, fieldSpec{
			index:        index,
			env:          name,
			typ:          field.Type,
			defaultValue: defaultValue,
			hasDefault:   hasDefault,
			secret:       secret,
		})
	}
	return specs, nil
}

func environmentValues(environ []string, known map[string]bool) (map[string]string, error) {
	values := make(map[string]string)
	var unknown []string
	for _, assignment := range environ {
		name, value, found := strings.Cut(assignment, "=")
		isKnown := known[name]
		if !isKnown && (!strings.HasPrefix(name, "FANOUT_") || isPlatformInjectedEnvironment(name)) {
			continue
		}
		if !found {
			return nil, fmt.Errorf("environment entry %s must use NAME=value form", name)
		}
		if !isKnown {
			unknown = append(unknown, name)
			continue
		}
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("duplicate environment variable %s", name)
		}
		values[name] = value
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown Fanout environment variables: %s", strings.Join(unknown, ", "))
	}
	return values, nil
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
			return nil, fmt.Errorf("must be a Go duration")
		}
		return parsed, nil
	}
	switch typ.Kind() {
	case reflect.String:
		return value, nil
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("must be true or false")
		}
		return parsed, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, typ.Bits())
		if err != nil {
			return nil, fmt.Errorf("must be an integer")
		}
		result := reflect.New(typ).Elem()
		result.SetInt(parsed)
		return result.Interface(), nil
	default:
		return nil, fmt.Errorf("unsupported configuration type %s", typ)
	}
}
