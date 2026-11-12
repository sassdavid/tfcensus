package ownership

// SelectName is the rule name a --select expression's errors carry.
const SelectName = "--select"

// Selector is a CEL expression over `resource` that picks resources, compiled in the rules'
// environment. in_state is always false in it.
type Selector struct{ c *compiled }

// NewSelector compiles expr. A compile error is a [*RuleError] named [SelectName].
func NewSelector(expr string) (*Selector, error) {
	env, err := newEnv(nil)
	if err != nil {
		return nil, err
	}
	c, err := compile(env, Rule{Name: SelectName, Match: expr, Reason: SelectName, Category: CategoryIgnored},
		"select", "")
	if err != nil {
		return nil, err
	}
	return &Selector{c: c}, nil
}

// Match reports whether the expression is true for r.
func (s *Selector) Match(r *Resource) (bool, error) {
	return eval(s.c, map[string]any{"resource": r, "unit": &Unit{}})
}
