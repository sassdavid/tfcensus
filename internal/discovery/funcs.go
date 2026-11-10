package discovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// ErrUnsupportedFunction is returned for terragrunt functions the fast path can't evaluate
// without running a command or calling AWS.
var ErrUnsupportedFunction = errors.New("function needs terragrunt render")

const (
	maxParentFolders = 100 // terragrunt's default for find_in_parent_folders
	argsWithFallback = 2   // functions whose last, optional argument is a fallback or default
)

// functions returns the functions available while evaluating in scope s.
func (s *scope) functions() map[string]function.Function {
	fns := map[string]function.Function{
		"abs": stdlib.AbsoluteFunc, "ceil": stdlib.CeilFunc, "chomp": stdlib.ChompFunc,
		"chunklist": stdlib.ChunklistFunc, "coalesce": stdlib.CoalesceFunc,
		"coalescelist": stdlib.CoalesceListFunc, "compact": stdlib.CompactFunc,
		"concat": stdlib.ConcatFunc, "contains": stdlib.ContainsFunc, "csvdecode": stdlib.CSVDecodeFunc,
		"distinct": stdlib.DistinctFunc, "element": stdlib.ElementFunc, "flatten": stdlib.FlattenFunc,
		"floor": stdlib.FloorFunc, "format": stdlib.FormatFunc, "formatdate": stdlib.FormatDateFunc,
		"formatlist": stdlib.FormatListFunc, "indent": stdlib.IndentFunc, "index": stdlib.IndexFunc,
		"join": stdlib.JoinFunc, "jsondecode": stdlib.JSONDecodeFunc, "jsonencode": stdlib.JSONEncodeFunc,
		"keys": stdlib.KeysFunc, "log": stdlib.LogFunc, "lookup": stdlib.LookupFunc,
		"lower": stdlib.LowerFunc, "max": stdlib.MaxFunc, "merge": stdlib.MergeFunc, "min": stdlib.MinFunc,
		"parseint": stdlib.ParseIntFunc, "pow": stdlib.PowFunc, "range": stdlib.RangeFunc,
		"regex": stdlib.RegexFunc, "regexall": stdlib.RegexAllFunc, "reverse": stdlib.ReverseListFunc,
		"setintersection": stdlib.SetIntersectionFunc, "setproduct": stdlib.SetProductFunc,
		"setsubtract": stdlib.SetSubtractFunc, "setunion": stdlib.SetUnionFunc,
		"signum": stdlib.SignumFunc, "slice": stdlib.SliceFunc, "sort": stdlib.SortFunc,
		"split": stdlib.SplitFunc, "strrev": stdlib.ReverseFunc, "substr": stdlib.SubstrFunc,
		"timeadd": stdlib.TimeAddFunc, "title": stdlib.TitleFunc, "trim": stdlib.TrimFunc,
		"trimprefix": stdlib.TrimPrefixFunc, "trimspace": stdlib.TrimSpaceFunc,
		"trimsuffix": stdlib.TrimSuffixFunc, "upper": stdlib.UpperFunc, "values": stdlib.ValuesFunc,
		"zipmap":   stdlib.ZipmapFunc,
		"tostring": stdlib.MakeToFunc(cty.String), "tonumber": stdlib.MakeToFunc(cty.Number),
		"tobool": stdlib.MakeToFunc(cty.Bool), "tolist": stdlib.MakeToFunc(cty.List(cty.DynamicPseudoType)),
		"tomap": stdlib.MakeToFunc(cty.Map(cty.DynamicPseudoType)),
		"toset": stdlib.MakeToFunc(cty.Set(cty.DynamicPseudoType)),
		"try":   tryfunc.TryFunc, "can": tryfunc.CanFunc,
		"length":   lengthFunc(),
		"replace":  replaceFunc(),
		"basename": stringFunc(func(p string) (string, error) { return filepath.Base(p), nil }),
		"dirname":  stringFunc(func(p string) (string, error) { return filepath.Dir(p), nil }),
		"abspath":  stringFunc(filepath.Abs),
		"file":     stringFunc(s.readFile),
		"fileexists": function.New(&function.Spec{
			Params: []function.Parameter{{Name: "path", Type: cty.String}},
			Type:   function.StaticReturnType(cty.Bool),
			Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
				fi, err := os.Stat(s.relPath(args[0].AsString()))
				return cty.BoolVal(err == nil && !fi.IsDir()), nil
			},
		}),

		"find_in_parent_folders":      optStringsFunc(0, argsWithFallback, s.findInParentFolders),
		"path_relative_to_include":    optStringsFunc(0, 1, s.pathRelativeToInclude),
		"path_relative_from_include":  optStringsFunc(0, 1, s.pathRelativeFromInclude),
		"get_parent_terragrunt_dir":   optStringsFunc(0, 1, s.parentTerragruntDir),
		"get_env":                     optStringsFunc(1, argsWithFallback, s.getEnv),
		"get_terragrunt_dir":          constFunc(func() (string, error) { return filepath.Dir(s.configPath), nil }),
		"get_original_terragrunt_dir": constFunc(func() (string, error) { return filepath.Dir(s.unitPath), nil }),
		"get_repo_root":               constFunc(s.repoRoot),
		"get_path_from_repo_root":     constFunc(s.pathFromRepoRoot),
		"get_path_to_repo_root":       constFunc(s.pathToRepoRoot),
		"get_platform":                constFunc(func() (string, error) { return runtime.GOOS, nil }),
		"read_terragrunt_config":      s.readTerragruntConfigFunc(),
		"get_aws_account_id":          s.awsAccountIDFunc(),
	}
	for _, name := range []string{
		"run_cmd", "sops_decrypt_file", "get_aws_account_alias",
		"get_aws_caller_identity_arn", "get_aws_caller_identity_user_id", "get_terraform_command",
		"get_terraform_cli_args", "read_tfvars_file", "mark_as_read", "constraint_check",
		"get_default_retryable_errors", "get_working_dir",
	} {
		fns[name] = unsupportedFunc(name)
	}
	return fns
}

// awsAccountIDFunc is get_aws_account_id: the account of the default AWS credentials.
func (s *scope) awsAccountIDFunc() function.Function {
	if s.ev.accountID == nil {
		return unsupportedFunc("get_aws_account_id")
	}
	return constFunc(func() (string, error) {
		s.unit.usedAccount = true
		id, err := s.ev.callerAccount()
		if err != nil {
			return "", fmt.Errorf("get_aws_account_id: %w", err)
		}
		return id, nil
	})
}

func unsupportedFunc(name string) function.Function {
	return function.New(&function.Spec{
		VarParam: &function.Parameter{Name: "args", Type: cty.DynamicPseudoType, AllowNull: true},
		Type:     function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func([]cty.Value, cty.Type) (cty.Value, error) {
			return cty.NilVal, fmt.Errorf("%s: %w", name, ErrUnsupportedFunction)
		},
	})
}

func stringFunc(f func(string) (string, error)) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "s", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			out, err := f(args[0].AsString())
			if err != nil {
				return cty.NilVal, err
			}
			return cty.StringVal(out), nil
		},
	})
}

func constFunc(f func() (string, error)) function.Function {
	return function.New(&function.Spec{
		Type: function.StaticReturnType(cty.String),
		Impl: func([]cty.Value, cty.Type) (cty.Value, error) {
			out, err := f()
			if err != nil {
				return cty.NilVal, err
			}
			return cty.StringVal(out), nil
		},
	})
}

// optStringsFunc takes between minArgs and maxArgs string arguments.
func optStringsFunc(minArgs, maxArgs int, f func([]string) (string, error)) function.Function {
	return function.New(&function.Spec{
		VarParam: &function.Parameter{Name: "args", Type: cty.String},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			if len(args) < minArgs || len(args) > maxArgs {
				return cty.NilVal, fmt.Errorf("want %d to %d arguments, got %d", minArgs, maxArgs, len(args))
			}
			strs := make([]string, len(args))
			for i, a := range args {
				strs[i] = a.AsString()
			}
			out, err := f(strs)
			if err != nil {
				return cty.NilVal, err
			}
			return cty.StringVal(out), nil
		},
	})
}

func lengthFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType}},
		Type:   function.StaticReturnType(cty.Number),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			if args[0].Type() == cty.String {
				return stdlib.Strlen(args[0])
			}
			return stdlib.Length(args[0])
		},
	})
}

// replaceFunc is Terraform's replace: a substring wrapped in slashes is a regular expression.
func replaceFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "str", Type: cty.String}, {Name: "substr", Type: cty.String}, {Name: "replace", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			sub := args[1].AsString()
			if len(sub) > 1 && strings.HasPrefix(sub, "/") && strings.HasSuffix(sub, "/") {
				return stdlib.RegexReplace(args[0], cty.StringVal(sub[1:len(sub)-1]), args[2])
			}
			return stdlib.Replace(args[0], args[1], args[2])
		},
	})
}

// relPath resolves p against the directory of the config being evaluated, as terragrunt does.
func (s *scope) relPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(s.configPath), p)
}

func (s *scope) readFile(p string) (string, error) {
	b, err := os.ReadFile(s.relPath(p))
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(b), nil
}

func (s *scope) findInParentFolders(args []string) (string, error) {
	name := "terragrunt.hcl"
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	}
	dir := filepath.Dir(s.configPath)
	for range maxParentFolders {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		candidate := filepath.Join(parent, name)
		if s.ev.files.exists(candidate) {
			return candidate, nil
		}
		dir = parent
	}
	if len(args) == argsWithFallback {
		return args[1], nil
	}
	return "", fmt.Errorf("find_in_parent_folders: no %s above %s", name, filepath.Dir(s.configPath))
}

// includePath returns the included file path_relative_to_include and friends refer to.
func (s *scope) includePath(args []string) (string, bool, error) {
	if s.included != "" {
		return s.included, true, nil
	}
	if len(s.includes) == 0 {
		return "", false, nil
	}
	if len(args) > 0 && args[0] != "" {
		for _, inc := range s.includes {
			if inc.name == args[0] {
				return inc.path, true, nil
			}
		}
		return "", false, fmt.Errorf("no include block named %q", args[0])
	}
	if len(s.includes) == 1 {
		return s.includes[0].path, true, nil
	}
	return "", false, errors.New("more than one include block: name the one you mean")
}

func (s *scope) pathRelativeToInclude(args []string) (string, error) {
	inc, ok, err := s.includePath(args)
	if err != nil || !ok {
		return ".", err
	}
	rel, err := filepath.Rel(filepath.Dir(inc), filepath.Dir(s.configPath))
	if err != nil {
		return "", fmt.Errorf("path_relative_to_include: %w", err)
	}
	return filepath.ToSlash(rel), nil
}

func (s *scope) pathRelativeFromInclude(args []string) (string, error) {
	inc, ok, err := s.includePath(args)
	if err != nil || !ok {
		return ".", err
	}
	rel, err := filepath.Rel(filepath.Dir(s.configPath), filepath.Dir(inc))
	if err != nil {
		return "", fmt.Errorf("path_relative_from_include: %w", err)
	}
	return filepath.ToSlash(rel), nil
}

func (s *scope) parentTerragruntDir(args []string) (string, error) {
	rel, err := s.pathRelativeFromInclude(args)
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(filepath.Dir(s.configPath), rel)), nil
}

func (s *scope) getEnv(args []string) (string, error) {
	if v, ok := s.ev.lookupEnv(args[0]); ok {
		return v, nil
	}
	if len(args) == argsWithFallback {
		return args[1], nil
	}
	return "", fmt.Errorf("get_env: %s is not set and has no default", args[0])
}

func (s *scope) repoRoot() (string, error) {
	dir := filepath.Dir(s.unitPath)
	for {
		if s.ev.files.exists(filepath.Join(dir, ".git")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("get_repo_root: not in a git repository")
		}
		dir = parent
	}
}

func (s *scope) pathFromRepoRoot() (string, error) {
	root, err := s.repoRoot()
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, filepath.Dir(s.unitPath))
	if err != nil {
		return "", fmt.Errorf("get_path_from_repo_root: %w", err)
	}
	return filepath.ToSlash(rel), nil
}

func (s *scope) pathToRepoRoot() (string, error) {
	root, err := s.repoRoot()
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(filepath.Dir(s.unitPath), root)
	if err != nil {
		return "", fmt.Errorf("get_path_to_repo_root: %w", err)
	}
	return filepath.ToSlash(rel), nil
}

func (s *scope) readTerragruntConfigFunc() function.Function {
	return function.New(&function.Spec{
		Params:   []function.Parameter{{Name: "path", Type: cty.String}},
		VarParam: &function.Parameter{Name: "default", Type: cty.DynamicPseudoType, AllowNull: true},
		Type:     function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			if len(args) > argsWithFallback {
				return cty.NilVal, errors.New("read_terragrunt_config: want 1 or 2 arguments")
			}
			target := s.relPath(args[0].AsString())
			if fi, err := os.Stat(target); err == nil && fi.IsDir() {
				target = filepath.Join(target, "terragrunt.hcl")
			}
			if !s.ev.files.exists(target) {
				if len(args) == argsWithFallback {
					return args[1], nil
				}
				return cty.NilVal, fmt.Errorf("read_terragrunt_config: %s does not exist", target)
			}
			return s.readConfig(filepath.Clean(target))
		},
	})
}
