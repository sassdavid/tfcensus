package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// UpdateEnv makes golden helpers rewrite their files. mise run test:golden -- -update sets it.
const UpdateEnv = "GOLDEN_UPDATE"

// Updating reports whether golden files should be rewritten.
func Updating() bool {
	return os.Getenv(UpdateEnv) != ""
}

// Golden compares got with the file at path, or rewrites the file when updating.
func Golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if Updating() {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `mise run test:golden -- -update` to create it)", path, err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s\nrun `mise run test:golden -- -update` and review the diff", path, diff)
	}
}
