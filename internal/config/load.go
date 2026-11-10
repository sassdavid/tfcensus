package config

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/pflag"

	"github.com/sassdavid/tfcensus/internal/yamlschema"
)

// Environment variable names.
const (
	EnvPrefix     = "TFCENSUS_"
	EnvConfigFile = EnvPrefix + "CONFIG"
)

const delim = "."

// SourceKind says which layer set a value.
type SourceKind string

// Config layers, lowest precedence first.
const (
	SourceDefault SourceKind = "default"
	SourceUser    SourceKind = "user"
	SourceProject SourceKind = "project"
	SourceEnv     SourceKind = "env"
	SourceFlag    SourceKind = "flag"
)

// Source is where an effective value came from.
type Source struct {
	Kind SourceKind `json:"kind"`
	Name string     `json:"name,omitempty"` // path, env var or flag
}

func (s Source) String() string {
	switch s.Kind {
	case SourceUser, SourceProject:
		return string(s.Kind) + " file " + s.Name
	case SourceEnv:
		return "env " + s.Name
	case SourceFlag:
		return "flag --" + s.Name
	case SourceDefault:
	}
	return string(s.Kind)
}

// File is a config file location and whether it exists.
type File struct {
	Kind     SourceKind `json:"kind"`
	Path     string     `json:"path"`
	Found    bool       `json:"found"`
	Explicit bool       `json:"explicit,omitempty"` // set by --config or TFCENSUS_CONFIG
}

// Options controls [Load].
type Options struct {
	Environ []string
	Cwd     string
	GOOS    string // defaults to runtime.GOOS

	// ExplicitFile replaces project-file discovery. TFCENSUS_CONFIG is used when empty.
	ExplicitFile string

	// FlagKeys maps flag names to config keys. Only flags the user set are applied.
	Flags    *pflag.FlagSet
	FlagKeys map[string]string
}

// Result is the effective configuration with provenance.
type Result struct {
	Config Config
	Files  []File
	// UnknownEnv lists ignored TFCENSUS_* variables that match no key.
	UnknownEnv []string

	sources map[string]Source
	k       *koanf.Koanf
}

// Keys returns every effective leaf key, sorted.
func (r *Result) Keys() []string {
	keys := r.k.Keys()
	slices.Sort(keys)
	return keys
}

// Value returns the effective value of a leaf key.
func (r *Result) Value(key string) any { return r.k.Get(key) }

// Raw returns the effective configuration as a nested map.
func (r *Result) Raw() map[string]any { return r.k.Raw() }

// Source returns where key's value came from. A key without its own record inherits
// the source of its nearest parent.
func (r *Result) Source(key string) Source {
	for k := key; k != ""; {
		if s, ok := r.sources[k]; ok {
			return s
		}
		i := strings.LastIndex(k, delim)
		if i < 0 {
			break
		}
		k = k[:i]
	}
	return Source{Kind: SourceDefault}
}

type loader struct {
	opts    Options
	schema  *yamlschema.Schema
	k       *koanf.Koanf
	sources map[string]Source
	res     *Result
}

// Load builds the effective configuration.
func Load(opts Options) (*Result, error) {
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	s, err := loadSchema()
	if err != nil {
		return nil, err
	}
	l := &loader{
		opts:    opts,
		schema:  s,
		k:       koanf.New(delim),
		sources: map[string]Source{},
		res:     &Result{},
	}
	if err = l.loadAll(); err != nil {
		return nil, err
	}
	if err = l.k.UnmarshalWithConf("", &l.res.Config, koanf.UnmarshalConf{Tag: "json"}); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	l.res.sources = l.sources
	l.res.k = l.k
	return l.res, nil
}

func (l *loader) loadAll() error {
	defaults, err := toMap(Default())
	if err != nil {
		return err
	}
	if err = l.merge(defaults, Source{Kind: SourceDefault}); err != nil {
		return err
	}
	if err = l.loadFiles(); err != nil {
		return err
	}
	if err = l.loadEnv(); err != nil {
		return err
	}
	if err = l.loadFlags(); err != nil {
		return err
	}
	return l.validateEffective()
}

// Locate returns the user and project config files that Load reads. The project entry
// has an empty Path when none was found.
func Locate(opts Options) ([]File, error) {
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	lookup := lookupIn(opts.Environ)
	userPath, err := UserConfigPath(lookup, opts.GOOS)
	if err != nil {
		return nil, err
	}
	userFound, err := isFile(userPath)
	if err != nil {
		return nil, err
	}
	files := []File{{Kind: SourceUser, Path: userPath, Found: userFound}}

	explicit := opts.ExplicitFile
	if explicit == "" {
		explicit, _ = lookup(EnvConfigFile)
	}
	if explicit != "" {
		found, statErr := isFile(explicit)
		if statErr != nil {
			return nil, statErr
		}
		return append(files, File{Kind: SourceProject, Path: explicit, Found: found, Explicit: true}), nil
	}
	projectPath, err := FindProjectFile(opts.Cwd)
	if err != nil {
		return nil, err
	}
	return append(files, File{Kind: SourceProject, Path: projectPath, Found: projectPath != ""}), nil
}

func lookupIn(environ []string) LookupFunc {
	return func(key string) (string, bool) {
		for _, kv := range slices.Backward(environ) {
			k, v, ok := strings.Cut(kv, "=")
			if ok && k == key {
				return v, true
			}
		}
		return "", false
	}
}

func (l *loader) loadFiles() error {
	files, err := Locate(l.opts)
	if err != nil {
		return err
	}
	l.res.Files = files
	for _, f := range files {
		switch {
		case f.Found:
			if err = l.loadFile(f.Kind, f.Path); err != nil {
				return err
			}
		case f.Explicit:
			return fmt.Errorf("%w: config file %s does not exist", ErrInvalid, f.Path)
		}
	}
	return nil
}

// ValidateFile checks one config file against the schema.
func ValidateFile(path string) error {
	s, err := loadSchema()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: read config file: %w", ErrInvalid, err)
	}
	_, err = parseFile(s, path, b)
	return err
}

func (l *loader) loadFile(kind SourceKind, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: read config file: %w", ErrInvalid, err)
	}
	pf, err := parseFile(l.schema, path, b)
	if err != nil {
		return err
	}
	return l.merge(pf.data, Source{Kind: kind, Name: path})
}

// loadEnv maps an env var named by [EnvVarName] to its key. "__" also marks nesting, for
// keys that contain "_". Values are converted to the schema type.
func (l *loader) loadEnv() error {
	byEnv := map[string]string{}
	for _, key := range envLeaves(l.schema) {
		byEnv[EnvVarName(key)] = key
	}
	known := map[string]bool{}
	for _, key := range envLeaves(l.schema) {
		known[key] = true
	}
	names := map[string]string{} // key to env var
	var problems []Problem
	p := env.Provider(delim, env.Opt{
		Prefix:      EnvPrefix,
		EnvironFunc: func() []string { return l.opts.Environ },
		TransformFunc: func(name, value string) (string, any) {
			if name == EnvConfigFile {
				return "", nil
			}
			key, ok := byEnv[name]
			if !ok && strings.Contains(name, "__") {
				key = strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(name, EnvPrefix), "__", delim))
				ok = known[key]
			}
			if !ok {
				l.res.UnknownEnv = append(l.res.UnknownEnv, name)
				return "", nil
			}
			typ := l.schema.TypeAt(strings.Split(key, delim))
			if value == "" && typ != typeString && typ != typeArray {
				return "", nil // VAR= means unset for numbers and booleans
			}
			v, err := coerce(typ, value)
			if err != nil {
				problems = append(problems, Problem{Origin: "env " + name, Key: key, Message: err.Error()})
				return "", nil
			}
			names[key] = name
			return key, v
		},
	})
	m, err := p.Read()
	if err != nil {
		return fmt.Errorf("read environment: %w", err)
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	slices.Sort(l.res.UnknownEnv)
	if err = l.k.Load(mapProvider(m), nil); err != nil {
		return fmt.Errorf("load environment: %w", err)
	}
	for key, name := range names {
		l.sources[key] = Source{Kind: SourceEnv, Name: name}
	}
	return nil
}

// EnvVarName returns the environment variable for a dotted key: "." and "-" become "_", so
// cache.ttl is TFCENSUS_CACHE_TTL.
func EnvVarName(key string) string {
	return EnvPrefix + strings.ToUpper(strings.NewReplacer(delim, "_", "-", "_").Replace(key))
}

func coerce(typ, value string) (any, error) {
	switch typ {
	case "integer":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("wrong type: %q is not an integer", value)
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("wrong type: %q is not a number", value)
		}
		return f, nil
	case "boolean":
		b, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("wrong type: %q is not a boolean (true/false/1/0)", value)
		}
		return b, nil
	case typeArray:
		if strings.TrimSpace(value) == "" {
			return []any{}, nil
		}
		parts := strings.Split(value, ",")
		out := make([]any, len(parts))
		for i, part := range parts {
			out[i] = strings.TrimSpace(part)
		}
		return out, nil
	default:
		return value, nil
	}
}

func (l *loader) loadFlags() error {
	if l.opts.Flags == nil {
		return nil
	}
	set := map[string]string{} // key to flag name
	p := posflag.ProviderWithFlag(l.opts.Flags, delim, nil, func(f *pflag.Flag) (string, any) {
		key, ok := l.opts.FlagKeys[f.Name]
		if !ok || !f.Changed {
			return "", nil
		}
		set[key] = f.Name
		return key, posflag.FlagVal(l.opts.Flags, f)
	})
	if err := l.k.Load(p, nil); err != nil {
		return fmt.Errorf("load flags: %w", err)
	}
	for key, name := range set {
		l.sources[key] = Source{Kind: SourceFlag, Name: name}
	}
	return nil
}

// validateEffective catches bad values from env vars and flags. Files were checked on load.
func (l *loader) validateEffective() error {
	inst, err := yamlschema.ToJSONValue(l.k.Raw())
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	leaves, err := l.schema.Validate(inst)
	if err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if len(leaves) == 0 {
		return nil
	}
	problems := make([]Problem, 0, len(leaves))
	for _, lf := range leaves {
		key := strings.Join(lf.Loc, delim)
		problems = append(problems, Problem{Origin: l.sourceOf(key).String(), Key: key, Message: lf.Message})
	}
	return &ValidationError{Problems: problems}
}

func (l *loader) sourceOf(key string) Source {
	r := Result{sources: l.sources}
	return r.Source(key)
}

func (l *loader) merge(m map[string]any, src Source) error {
	if err := l.k.Load(mapProvider(m), nil); err != nil {
		return fmt.Errorf("merge %s: %w", src, err)
	}
	for _, key := range flattenKeys(m, "") {
		l.sources[key] = src
	}
	return nil
}

// flattenKeys lists leaf keys. Empty maps count as leaves, as in koanf.
func flattenKeys(m map[string]any, prefix string) []string {
	var out []string
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + delim + k
		}
		if sub, ok := v.(map[string]any); ok && len(sub) > 0 {
			out = append(out, flattenKeys(sub, key)...)
			continue
		}
		out = append(out, key)
	}
	return out
}

type mapProvider map[string]any

func (mapProvider) ReadBytes() ([]byte, error) {
	return nil, errors.New("map provider does not support ReadBytes")
}

func (m mapProvider) Read() (map[string]any, error) { return m, nil }
