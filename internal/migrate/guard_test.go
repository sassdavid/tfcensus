package migrate_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteAPIsOnlyInMigrate enforces ADR-0007: no Go file outside internal/migrate calls an S3 or
// DynamoDB write API or pushes state, and in the CLI only the apply, rollback and mark-applied
// paths reach the engine's write methods.
func TestWriteAPIsOnlyInMigrate(t *testing.T) {
	t.Parallel()
	writes := regexp.MustCompile(`\.(PutObject|CopyObject|DeleteObject|DeleteObjects|PutItem|DeleteItem|` +
		`UpdateItem|BatchWriteItem|PutBucket[A-Za-z]*|DeleteBucket[A-Za-z]*)\(|"push"`)
	engineWrites := regexp.MustCompile(`\.(Apply|Rollback|MarkApplied)\(ctx, mf|Pusher\s*=`)
	root := filepath.Join("..", "..")
	var cliHits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case strings.HasPrefix(d.Name(), ".") && rel != ".", rel == "testdata", rel == "internal/migrate":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasPrefix(rel, "internal/testutil/") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range writes.FindAllString(string(b), -1) {
			t.Errorf("%s calls %s outside internal/migrate", rel, m)
		}
		for _, m := range engineWrites.FindAllString(string(b), -1) {
			cliHits = append(cliHits, rel+": "+m)
		}
		return nil
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"internal/cli/move.go: .Apply(ctx, mf",
		"internal/cli/move.go: .Rollback(ctx, mf",
		"internal/cli/move.go: .MarkApplied(ctx, mf",
		"internal/cli/move.go: Pusher =",
	}, cliHits)
}
