package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

//go:embed template.yaml
var template []byte

// ErrExists reports that [WriteTemplate] found a file at its path.
var ErrExists = errors.New("file exists")

// Template returns the commented config file that config init writes. Every key except
// schema_version is commented out at its default.
func Template() []byte { return slices.Clone(template) }

// WriteTemplate writes [Template] to path and creates its directory. It replaces an existing
// file only when force is set.
func WriteTemplate(path string, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s", ErrExists, path)
	}
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	if _, err = f.Write(template); err != nil {
		_ = f.Close()
		return fmt.Errorf("write config file: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}
