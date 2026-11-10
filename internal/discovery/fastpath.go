package discovery

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

const (
	maxReadDepth    = 10 // nested read_terragrunt_config calls before tfcensus assumes a cycle
	mergeDeep       = "deep"
	mergeNone       = "no_merge"
	valuesFileName  = "terragrunt.values.hcl"
	unitFileName    = "terragrunt.hcl"
	rootFileName    = "root.hcl"
	stackFileName   = "terragrunt.stack.hcl"
	generatedStacks = ".terragrunt-stack"
	varLocal        = "local"
	blockLocals     = "locals"
)

// evaluator is the fast path: it evaluates the parts of terragrunt.hcl that locate state, with
// the common terragrunt functions and without running anything. It is safe for concurrent use.
type evaluator struct {
	files     *fileCache
	lookupEnv func(string) (string, bool)
}

func newEvaluator(lookupEnv func(string) (string, bool)) *evaluator {
	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}
	return &evaluator{files: newFileCache(), lookupEnv: lookupEnv}
}

// fileCache parses each file once and remembers which paths exist, because units in one repo
// share their root.hcl and account.hcl files.
type fileCache struct {
	mu     sync.Mutex
	bodies map[string]parsedFile
	exist  map[string]bool
}

type parsedFile struct {
	body *hclsyntax.Body
	err  error
}

func newFileCache() *fileCache {
	return &fileCache{bodies: map[string]parsedFile{}, exist: map[string]bool{}}
}

func (c *fileCache) body(path string) (*hclsyntax.Body, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.bodies[path]; ok {
		return p.body, p.err
	}
	var p parsedFile
	src, err := os.ReadFile(path)
	if err != nil {
		p.err = fmt.Errorf("read config: %w", err)
	} else {
		f, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
		if diags.HasErrors() {
			p.err = diagError(diags)
		} else {
			p.body, _ = f.Body.(*hclsyntax.Body)
		}
	}
	c.bodies[path] = p
	return p.body, p.err
}

func (c *fileCache) exists(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ok, seen := c.exist[path]; seen {
		return ok
	}
	_, err := os.Stat(path)
	c.exist[path] = err == nil
	return err == nil
}

// scope evaluates one file on behalf of one unit, as terragrunt does: get_terragrunt_dir and
// find_in_parent_folders work from the unit even inside an included root.hcl.
type scope struct {
	ev         *evaluator
	path       string    // the file being evaluated
	configPath string    // the config terragrunt considers current
	unitPath   string    // the unit's terragrunt.hcl
	included   string    // set while evaluating an included file
	includes   []include // the unit file's include blocks
	values     cty.Value
	depth      int
	locals     map[string]cty.Value
	localErrs  map[string]error
	fns        map[string]function.Function
}

type include struct {
	name   string
	path   string
	expose bool
	merge  string
	body   *hclsyntax.Body
	scope  *scope
}

func (ev *evaluator) newScope(path, configPath, unitPath string, depth int) *scope {
	s := &scope{
		ev: ev, path: path, configPath: configPath, unitPath: unitPath, depth: depth,
		locals: map[string]cty.Value{}, localErrs: map[string]error{}, values: cty.NilVal,
	}
	s.fns = s.functions()
	return s
}

func (s *scope) evalContext() *hcl.EvalContext {
	vars := map[string]cty.Value{varLocal: cty.ObjectVal(s.locals)}
	exposed := map[string]cty.Value{}
	for _, inc := range s.includes {
		if inc.expose && inc.name != "" {
			exposed[inc.name] = cty.ObjectVal(map[string]cty.Value{blockLocals: cty.ObjectVal(inc.scope.locals)})
		}
	}
	if len(exposed) > 0 {
		vars["include"] = cty.ObjectVal(exposed)
	}
	if s.values != cty.NilVal {
		vars["values"] = s.values
	}
	return &hcl.EvalContext{Variables: vars, Functions: s.fns}
}

// value evaluates expr. A reference to a local that failed returns that local's error, which says
// more than HCL's "unsupported attribute".
func (s *scope) value(expr hcl.Expression) (cty.Value, error) {
	for _, tr := range expr.Variables() {
		if err := s.refError(tr); err != nil {
			return cty.NilVal, err
		}
	}
	v, diags := expr.Value(s.evalContext())
	if diags.HasErrors() {
		return cty.NilVal, diagError(diags)
	}
	return v, nil
}

func (s *scope) refError(tr hcl.Traversal) error {
	names := traversalNames(tr)
	switch {
	case len(names) >= 2 && names[0] == varLocal:
		if err := s.localErrs[names[1]]; err != nil {
			return err
		}
	case len(names) >= 4 && names[0] == "include" && names[2] == blockLocals:
		for _, inc := range s.includes {
			if inc.name == names[1] {
				if err := inc.scope.localErrs[names[3]]; err != nil {
					return fmt.Errorf("include.%s.%w", inc.name, err)
				}
			}
		}
	}
	return nil
}

func traversalNames(tr hcl.Traversal) []string {
	var names []string
	for _, step := range tr {
		switch t := step.(type) {
		case hcl.TraverseRoot:
			names = append(names, t.Name)
		case hcl.TraverseAttr:
			names = append(names, t.Name)
		default:
			return names
		}
	}
	return names
}

// evalLocals evaluates every local it can. Locals that fail, or depend on one that failed, are
// kept in localErrs so that only the expressions that use them fail.
func (s *scope) evalLocals(body *hclsyntax.Body) {
	pending := map[string]*hclsyntax.Attribute{}
	for _, blk := range body.Blocks {
		if blk.Type == blockLocals {
			maps.Copy(pending, blk.Body.Attributes)
		}
	}
	for progress := true; progress && len(pending) > 0; {
		progress = false
		for _, name := range sortedKeys(pending) {
			attr := pending[name]
			if dependsOnPending(attr.Expr, pending, name) {
				continue
			}
			if v, err := s.value(attr.Expr); err != nil {
				s.localErrs[name] = fmt.Errorf("local.%s: %w", name, err)
			} else {
				s.locals[name] = v
			}
			delete(pending, name)
			progress = true
		}
	}
	for name := range pending {
		s.localErrs[name] = fmt.Errorf("local.%s: reference cycle", name)
	}
}

func dependsOnPending(expr hcl.Expression, pending map[string]*hclsyntax.Attribute, self string) bool {
	for _, tr := range expr.Variables() {
		names := traversalNames(tr)
		if len(names) >= 2 && names[0] == varLocal && names[1] != self {
			if _, ok := pending[names[1]]; ok {
				return true
			}
		}
	}
	return false
}

// readConfig is read_terragrunt_config: the target's locals and inputs, evaluated in the target's
// own directory. Like terragrunt, it fails when any of them fails.
func (s *scope) readConfig(target string) (cty.Value, error) {
	if s.depth >= maxReadDepth {
		return cty.NilVal, fmt.Errorf("read_terragrunt_config: more than %d nested reads", maxReadDepth)
	}
	body, err := s.ev.files.body(target)
	if err != nil {
		return cty.NilVal, err
	}
	t := s.ev.newScope(target, target, s.unitPath, s.depth+1)
	t.evalLocals(body)
	for _, name := range sortedKeys(t.localErrs) {
		return cty.NilVal, fmt.Errorf("read_terragrunt_config(%s): %w", filepath.Base(target), t.localErrs[name])
	}
	out := map[string]cty.Value{blockLocals: cty.ObjectVal(t.locals)}
	if attr, ok := body.Attributes["inputs"]; ok {
		v, inputsErr := t.value(attr.Expr)
		if inputsErr != nil {
			return cty.NilVal, fmt.Errorf("read_terragrunt_config(%s): inputs: %w", filepath.Base(target), inputsErr)
		}
		out["inputs"] = v
	}
	return cty.ObjectVal(out), nil
}

// unitConfig is what the fast path needs from one unit, after includes are merged.
type unitConfig struct {
	backend   string
	config    map[string]any
	configErr map[string]error
	generated map[string]generated
	inputs    map[string]any
}

type generated struct {
	path     string
	contents string
}

// errNoRemoteState means neither the unit nor its includes have a remote_state block.
var errNoRemoteState = errors.New("no remote_state block in the unit or its includes")

func (ev *evaluator) evalUnit(unitPath string) (*unitConfig, error) {
	body, err := ev.files.body(unitPath)
	if err != nil {
		return nil, err
	}
	u := ev.newScope(unitPath, unitPath, unitPath, 0)
	if err = u.loadValues(); err != nil {
		return nil, err
	}
	if err = u.loadIncludes(body); err != nil {
		return nil, err
	}
	u.evalLocals(body)

	cfg := &unitConfig{generated: map[string]generated{}, inputs: map[string]any{}}
	layers := make([]layer, 0, len(u.includes)+1)
	for _, inc := range u.includes {
		if inc.merge != mergeNone {
			layers = append(layers, layer{scope: inc.scope, body: inc.body, deep: inc.merge == mergeDeep})
		}
	}
	layers = append(layers, layer{scope: u, body: body})
	for _, l := range layers {
		l.collectGenerated(cfg.generated)
		l.collectInputs(cfg.inputs)
	}
	if err = cfg.mergeRemoteState(layers); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *scope) loadValues() error {
	path := filepath.Join(filepath.Dir(s.unitPath), valuesFileName)
	if !s.ev.files.exists(path) {
		return nil
	}
	body, err := s.ev.files.body(path)
	if err != nil {
		return err
	}
	vals := map[string]cty.Value{}
	for name, attr := range body.Attributes {
		v, valueErr := s.value(attr.Expr)
		if valueErr != nil {
			return fmt.Errorf("%s: %w", valuesFileName, valueErr)
		}
		vals[name] = v
	}
	s.values = cty.ObjectVal(vals)
	return nil
}

func (s *scope) loadIncludes(body *hclsyntax.Body) error {
	for _, blk := range body.Blocks {
		if blk.Type != "include" {
			continue
		}
		inc, err := s.parseInclude(blk)
		if err != nil {
			return fmt.Errorf("include %q: %w", inc.name, err)
		}
		s.includes = append(s.includes, inc)
	}
	return nil
}

func (s *scope) parseInclude(blk *hclsyntax.Block) (include, error) {
	inc := include{merge: "shallow"}
	if len(blk.Labels) > 0 {
		inc.name = blk.Labels[0]
	}
	path, ok, err := stringAttr(s, blk.Body, "path")
	if err != nil {
		return inc, err
	}
	if !ok {
		return inc, errors.New("no path")
	}
	inc.path = s.relPath(path)
	if inc.expose, err = boolAttr(s, blk.Body, "expose"); err != nil {
		return inc, err
	}
	merge, hasMerge, err := stringAttr(s, blk.Body, "merge_strategy")
	if err != nil {
		return inc, err
	}
	if hasMerge {
		inc.merge = merge
	}
	if inc.body, err = s.ev.files.body(inc.path); err != nil {
		return inc, err
	}
	inc.scope = s.ev.newScope(inc.path, s.unitPath, s.unitPath, 0)
	inc.scope.included = inc.path
	inc.scope.values = s.values
	inc.scope.evalLocals(inc.body)
	return inc, nil
}

func stringAttr(s *scope, body *hclsyntax.Body, name string) (string, bool, error) {
	attr, ok := body.Attributes[name]
	if !ok {
		return "", false, nil
	}
	v, err := s.value(attr.Expr)
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", name, err)
	}
	if v.Type() != cty.String || v.IsNull() {
		return "", false, fmt.Errorf("%s is not a string", name)
	}
	return v.AsString(), true, nil
}

func boolAttr(s *scope, body *hclsyntax.Body, name string) (bool, error) {
	attr, ok := body.Attributes[name]
	if !ok {
		return false, nil
	}
	v, err := s.value(attr.Expr)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	if v.Type() != cty.Bool || v.IsNull() {
		return false, fmt.Errorf("%s is not a bool", name)
	}
	return v.True(), nil
}

// layer is one file in the merge order: each include, then the unit itself.
type layer struct {
	scope *scope
	body  *hclsyntax.Body
	deep  bool
}

func (l layer) collectGenerated(out map[string]generated) {
	for _, blk := range l.body.Blocks {
		if blk.Type != "generate" || len(blk.Labels) == 0 {
			continue
		}
		name := blk.Labels[0]
		if disabled, err := boolAttr(l.scope, blk.Body, "disable"); err != nil || disabled {
			delete(out, name)
			continue
		}
		g := generated{}
		if attr, ok := blk.Body.Attributes["path"]; ok {
			if v, err := l.scope.value(attr.Expr); err == nil && v.Type() == cty.String {
				g.path = v.AsString()
			}
		}
		if attr, ok := blk.Body.Attributes["contents"]; ok {
			if v, err := l.scope.value(attr.Expr); err == nil && v.Type() == cty.String {
				g.contents = v.AsString()
			}
		}
		out[name] = g
	}
}

// collectInputs keeps the inputs that evaluate. They only feed the target-account fallback, so a
// failing input is skipped.
func (l layer) collectInputs(out map[string]any) {
	attr, ok := l.body.Attributes["inputs"]
	if !ok {
		return
	}
	vals, _, err := l.scope.objectValues(attr.Expr)
	if err != nil {
		return
	}
	maps.Copy(out, vals)
}

// objectValues evaluates an object expression. Object literals are evaluated item by item, so one
// failing item doesn't hide the others.
func (s *scope) objectValues(expr hcl.Expression) (map[string]any, map[string]error, error) {
	vals, errs := map[string]any{}, map[string]error{}
	if obj, ok := expr.(*hclsyntax.ObjectConsExpr); ok {
		for _, item := range obj.Items {
			kv, diags := item.KeyExpr.Value(nil)
			if diags.HasErrors() || kv.Type() != cty.String {
				continue
			}
			key := kv.AsString()
			v, err := s.value(item.ValueExpr)
			if err != nil {
				errs[key] = err
				continue
			}
			if vals[key], err = toGo(v); err != nil {
				errs[key] = err
			}
		}
		return vals, errs, nil
	}
	v, err := s.value(expr)
	if err != nil {
		return nil, nil, err
	}
	g, err := toGo(v)
	if err != nil {
		return nil, nil, err
	}
	m, ok := g.(map[string]any)
	if !ok {
		return nil, nil, errors.New("not an object")
	}
	return m, errs, nil
}

func (cfg *unitConfig) mergeRemoteState(layers []layer) error {
	found := false
	for _, l := range slices.Backward(layers) {
		backend, config, errs, ok, err := l.remoteState()
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if !found {
			found = true
			cfg.backend, cfg.config, cfg.configErr = backend, config, errs
			continue
		}
		if !l.deep {
			break
		}
		// A deep-merged include fills in the keys the unit doesn't set.
		for k, v := range config {
			if _, set := cfg.config[k]; !set {
				cfg.config[k] = v
			}
		}
		for k, e := range errs {
			if _, set := cfg.config[k]; !set {
				cfg.configErr[k] = e
			}
		}
	}
	if !found {
		return errNoRemoteState
	}
	return nil
}

// remoteState reads the remote_state block or attribute of one layer.
func (l layer) remoteState() (string, map[string]any, map[string]error, bool, error) {
	backendExpr, configExpr, found, err := remoteStateExprs(l.body)
	if err != nil || !found {
		return "", nil, nil, false, err
	}
	bv, err := l.scope.value(backendExpr)
	if err != nil {
		return "", nil, nil, false, fmt.Errorf("remote_state backend: %w", err)
	}
	if bv.Type() != cty.String || bv.IsNull() {
		return "", nil, nil, false, errors.New("remote_state backend is not a string")
	}
	config, errs := map[string]any{}, map[string]error{}
	if configExpr != nil {
		if config, errs, err = l.scope.objectValues(configExpr); err != nil {
			return "", nil, nil, false, fmt.Errorf("remote_state config: %w", err)
		}
	}
	return bv.AsString(), config, errs, true, nil
}

// remoteStateExprs finds the backend and config expressions of a remote_state block or of a
// remote_state object attribute.
func remoteStateExprs(body *hclsyntax.Body) (hcl.Expression, hcl.Expression, bool, error) {
	exprs := map[string]hcl.Expression{}
	if attr, isAttr := body.Attributes["remote_state"]; isAttr {
		obj, isObj := attr.Expr.(*hclsyntax.ObjectConsExpr)
		if !isObj {
			return nil, nil, false, errors.New("remote_state is not an object literal")
		}
		for _, item := range obj.Items {
			if kv, diags := item.KeyExpr.Value(nil); !diags.HasErrors() && kv.Type() == cty.String {
				exprs[kv.AsString()] = item.ValueExpr
			}
		}
	} else {
		idx := slices.IndexFunc(body.Blocks, func(b *hclsyntax.Block) bool { return b.Type == "remote_state" })
		if idx < 0 {
			return nil, nil, false, nil
		}
		for name, a := range body.Blocks[idx].Body.Attributes {
			exprs[name] = a.Expr
		}
	}
	if exprs["backend"] == nil {
		return nil, nil, false, errors.New("remote_state has no backend")
	}
	return exprs["backend"], exprs["config"], true, nil
}
