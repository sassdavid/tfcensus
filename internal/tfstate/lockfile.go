package tfstate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// LockFileName is the dependency lock file terraform init and tofu init write.
const LockFileName = ".terraform.lock.hcl"

// LockedProvider is a provider selected in a lock file.
type LockedProvider struct {
	Version string
	Host    string // the registry host, such as registry.opentofu.org
}

// OpenTofu reports whether the provider comes from the OpenTofu registry, which only tofu init
// selects from.
func (p LockedProvider) OpenTofu() bool { return p.Host == hostOpenTofu }

// ReadLockFile returns the provider versions selected in dir's lock file, keyed by normalized
// source (see [Provider]). A missing file returns an empty map.
func ReadLockFile(dir string) (map[string]string, error) {
	locked, err := ReadLockProviders(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(locked))
	for source, p := range locked {
		out[source] = p.Version
	}
	return out, nil
}

// ReadLockProviders returns the providers selected in dir's lock file, keyed by normalized source.
// A missing file returns an empty map.
func ReadLockProviders(dir string) (map[string]LockedProvider, error) {
	path := filepath.Join(dir, LockFileName)
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]LockedProvider{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lock file: %w", err)
	}
	f, diags := hclparse.NewParser().ParseHCL(src, path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse lock file: %w", diags)
	}
	content, _, diags := f.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "provider", LabelNames: []string{"source"}}},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse lock file: %w", diags)
	}
	out := map[string]LockedProvider{}
	for _, b := range content.Blocks {
		attrs, _, attrDiags := b.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "version"}},
		})
		if attrDiags.HasErrors() {
			continue
		}
		a, ok := attrs.Attributes["version"]
		if !ok {
			continue
		}
		v, valDiags := a.Expr.Value(nil)
		if valDiags.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			continue
		}
		source, host := normalizeSource(b.Labels[0])
		out[source] = LockedProvider{Version: v.AsString(), Host: host}
	}
	return out, nil
}

// KeepLockFile saves dir's lock file and returns the function that puts it back as it was, or
// removes it when init created it.
func KeepLockFile(dir string) (func() error, error) {
	path := filepath.Join(dir, LockFileName)
	orig, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return func() error {
		now, readErr := os.ReadFile(path)
		switch {
		case existed && (readErr != nil || !bytes.Equal(now, orig)):
			if werr := os.WriteFile(path, orig, 0o644); werr != nil { //nolint:gosec // G306: a lock file is committed.
				return fmt.Errorf("restore %s: %w", path, werr)
			}
		case !existed && readErr == nil:
			if rerr := os.Remove(path); rerr != nil {
				return fmt.Errorf("remove %s: %w", path, rerr)
			}
		}
		return nil
	}, nil
}
