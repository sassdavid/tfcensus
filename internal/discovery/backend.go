package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// diagError turns HCL diagnostics into one error with the first problem's location.
func diagError(diags hcl.Diagnostics) error {
	for _, d := range diags {
		if d.Severity != hcl.DiagError {
			continue
		}
		msg := d.Summary
		if d.Detail != "" {
			msg += ": " + strings.TrimSuffix(d.Detail, ".")
		}
		if d.Subject != nil {
			msg = fmt.Sprintf("%s:%d: %s", d.Subject.Filename, d.Subject.Start.Line, msg)
		}
		return errors.New(msg)
	}
	return errors.New("invalid HCL")
}

// toGo converts a cty value to the shapes encoding/json produces.
func toGo(v cty.Value) (any, error) {
	if !v.IsWhollyKnown() {
		return nil, errors.New("value is not known before apply")
	}
	b, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return nil, fmt.Errorf("convert value: %w", err)
	}
	var out any
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("convert value: %w", err)
	}
	return out, nil
}

// backendFrom builds a Backend from a backend type and its config map. For s3 it fails when the
// state location (bucket and key) isn't known; problems with other keys become warnings.
func backendFrom(typ string, config map[string]any, configErr map[string]error) (*Backend, []string, error) {
	if typ != BackendS3 {
		return &Backend{Type: typ}, nil, nil
	}
	for _, key := range []string{"bucket", "key"} {
		if err := configErr[key]; err != nil {
			return nil, nil, fmt.Errorf("remote_state config %s: %w", key, err)
		}
	}
	var warnings []string
	for _, key := range sortedKeys(configErr) {
		if key != "bucket" && key != "key" {
			warnings = append(warnings, fmt.Sprintf("backend config %s: %v", key, configErr[key]))
		}
	}
	b, err := s3Backend(config)
	if err != nil {
		return nil, nil, err
	}
	var missing []string
	if b.Bucket == "" {
		missing = append(missing, "bucket")
	}
	if b.Key == "" {
		missing = append(missing, "key")
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("backend config has no %s (partial configuration, set at init time)",
			strings.Join(missing, " or "))
	}
	if b.Region == "" {
		warnings = append(warnings, "backend has no region; terraform takes it from the environment")
	}
	return b, warnings, nil
}

func s3Backend(config map[string]any) (*Backend, error) {
	var problems []string
	str := func(key string) string {
		v, ok := config[key]
		if !ok || v == nil {
			return ""
		}
		s, isStr := v.(string)
		if !isStr {
			problems = append(problems, key+" is not a string")
		}
		return s
	}
	b := &Backend{
		Type:               BackendS3,
		Bucket:             str("bucket"),
		Key:                str("key"),
		Region:             str("region"),
		WorkspaceKeyPrefix: str("workspace_key_prefix"),
		DynamoDBTable:      str("dynamodb_table"),
		RoleARN:            str("role_arn"),
		Profile:            str("profile"),
	}
	if ar, ok := config["assume_role"].(map[string]any); ok {
		if arn, isStr := ar["role_arn"].(string); isStr {
			b.RoleARN = arn
		}
	}
	switch v := config["use_lockfile"].(type) {
	case bool:
		b.UseLockfile = v
	case string:
		b.UseLockfile = v == "true"
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("backend config: %s", strings.Join(problems, ", "))
	}
	return b, nil
}

// targetSource is one place a target account or region was found.
type targetSource struct {
	value      string
	from       string
	confidence Confidence
}

// providerTarget is what an aws provider block says about the target.
type providerTarget struct {
	account, region targetSource
}

// resolveTarget picks the best account and region from the provider block, then the inputs, then
// the backend. The confidence is that of the weaker of the two.
func resolveTarget(p providerTarget, inputs map[string]any, b *Backend) Target {
	account := firstSource(p.account, inputSource(inputs, accountID, "aws_account_id", "account_id"), backendAccount(b))
	region := firstSource(p.region, inputSource(inputs, nonEmpty, "aws_region", "region"), backendRegion(b))
	t := Target{
		Account: account.value, AccountFrom: account.from,
		Region: region.value, RegionFrom: region.from,
		Confidence: ConfidenceHigh,
	}
	for _, src := range []targetSource{account, region} {
		c := src.confidence
		if src.value == "" {
			c = ConfidenceLow
		}
		if rank(c) < rank(t.Confidence) {
			t.Confidence = c
		}
	}
	return t
}

func rank(c Confidence) int {
	switch c {
	case ConfidenceHigh:
		return 2 //nolint:mnd // ordering of the three levels
	case ConfidenceMedium:
		return 1
	case ConfidenceLow:
		return 0
	}
	return 0
}

func firstSource(srcs ...targetSource) targetSource {
	for _, s := range srcs {
		if s.value != "" {
			return s
		}
	}
	return targetSource{}
}

func inputSource(inputs map[string]any, valid func(string) bool, keys ...string) targetSource {
	for _, k := range keys {
		if s, ok := inputs[k].(string); ok && valid(s) {
			return targetSource{value: s, from: "inputs." + k, confidence: ConfidenceMedium}
		}
	}
	return targetSource{}
}

func nonEmpty(s string) bool { return s != "" }

func accountID(s string) bool {
	return len(s) == accountIDLen && strings.Trim(s, "0123456789") == ""
}

const accountIDLen = 12

func backendAccount(b *Backend) targetSource {
	if b == nil {
		return targetSource{}
	}
	re := regexp.MustCompile(`(?:^|[^0-9])([0-9]{12})(?:[^0-9]|$)`)
	if m := re.FindStringSubmatch(b.Bucket); m != nil {
		return targetSource{value: m[1], from: "backend.bucket", confidence: ConfidenceLow}
	}
	if acct := arnAccount(b.RoleARN); acct != "" {
		return targetSource{value: acct, from: "backend.role_arn", confidence: ConfidenceLow}
	}
	return targetSource{}
}

func backendRegion(b *Backend) targetSource {
	if b == nil || b.Region == "" {
		return targetSource{}
	}
	return targetSource{value: b.Region, from: "backend.region", confidence: ConfidenceLow}
}

// arnAccount returns the account ID field of an ARN, or "".
func arnAccount(arn string) string {
	parts := strings.SplitN(arn, ":", 6) //nolint:mnd // arn:partition:service:region:account:resource
	if len(parts) < 6 || parts[0] != "arn" || !accountID(parts[4]) {
		return ""
	}
	return parts[4]
}

// providerFromBody reads the default (unaliased) aws provider block in body. Attribute values must
// be known with ctx; ctx may be nil for generated files, which hold literals.
func providerFromBody(body hcl.Body, ctx *hcl.EvalContext) providerTarget {
	content, _, _ := body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "provider", LabelNames: []string{"name"}}},
	})
	for _, blk := range content.Blocks {
		if blk.Labels[0] != "aws" {
			continue
		}
		if p, ok := awsProvider(blk.Body, ctx); ok {
			return p
		}
	}
	return providerTarget{}
}

// awsProvider reads one aws provider block. It reports false for an aliased provider.
func awsProvider(body hcl.Body, ctx *hcl.EvalContext) (providerTarget, bool) {
	pc, _, _ := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "alias"}, {Name: "region"}, {Name: "allowed_account_ids"}},
		Blocks:     []hcl.BlockHeaderSchema{{Type: "assume_role"}},
	})
	if _, aliased := pc.Attributes["alias"]; aliased {
		return providerTarget{}, false
	}
	var p providerTarget
	if v, conf, ok := attrString(pc.Attributes["region"], ctx); ok {
		p.region = targetSource{value: v, from: "provider.region", confidence: conf}
	}
	if v, conf, ok := attrSingleString(pc.Attributes["allowed_account_ids"], ctx); ok && accountID(v) {
		p.account = targetSource{value: v, from: "provider.allowed_account_ids", confidence: conf}
	}
	if p.account.value != "" || len(pc.Blocks) == 0 {
		return p, true
	}
	ar, _, _ := pc.Blocks[0].Body.PartialContent(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "role_arn"}}})
	if v, conf, ok := attrString(ar.Attributes["role_arn"], ctx); ok && arnAccount(v) != "" {
		p.account = targetSource{value: arnAccount(v), from: "provider.assume_role", confidence: conf}
	}
	return p, true
}

// attrString evaluates a string attribute. A value that comes through a variable default is only
// medium confidence, since tfvars can override it.
func attrString(attr *hcl.Attribute, ctx *hcl.EvalContext) (string, Confidence, bool) {
	if attr == nil {
		return "", "", false
	}
	v, diags := attr.Expr.Value(ctx)
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() || v.Type() != cty.String {
		return "", "", false
	}
	return v.AsString(), exprConfidence(attr.Expr), true
}

func attrSingleString(attr *hcl.Attribute, ctx *hcl.EvalContext) (string, Confidence, bool) {
	if attr == nil {
		return "", "", false
	}
	v, diags := attr.Expr.Value(ctx)
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() || !v.CanIterateElements() || v.LengthInt() != 1 {
		return "", "", false
	}
	var out string
	for _, e := range v.AsValueSlice() {
		if e.Type() != cty.String || e.IsNull() {
			return "", "", false
		}
		out = e.AsString()
	}
	return out, exprConfidence(attr.Expr), true
}

func exprConfidence(expr hcl.Expression) Confidence {
	if len(expr.Variables()) > 0 {
		return ConfidenceMedium
	}
	return ConfidenceHigh
}
