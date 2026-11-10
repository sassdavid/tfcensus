package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sassdavid/tfcensus/internal/prose"
)

func main() {
	os.Exit(run())
}

func run() int {
	files, err := trackedFiles(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "proselint: %v\n", err)
		return 1
	}
	c := prose.NewChecker()
	var findings []prose.Finding
	for _, f := range files {
		if !checked(f) {
			continue
		}
		data, readErr := os.ReadFile(f)
		if readErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "proselint: %v\n", readErr)
			return 1
		}
		findings = append(findings, c.Check(f, data)...)
	}
	for _, f := range findings {
		_, _ = fmt.Fprintln(os.Stderr, f)
	}
	if len(findings) > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "%d writing problems; see .claude/rules/writing.md\n", len(findings))
		return 1
	}
	return 0
}

// trackedFiles lists files git would commit, including new untracked ones.
func trackedFiles(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for f := range bytes.SplitSeq(bytes.TrimRight(out, "\x00"), []byte{0}) {
		files = append(files, filepath.ToSlash(string(f)))
	}
	return files, nil
}

func checked(f string) bool {
	excludedPrefixes := []string{
		"docs/plan/",          // imported plan, not yet reworded
		"docs/engineering/",   // imported standards and ADRs, not yet reworded
		".claude/rules/",      // the rules quote the forbidden words
		"internal/prose/",     // so does the checker
		"testdata/cassettes/", // recorded responses quote upstream text
	}
	excludedFiles := []string{"docs/README.md", ".golangci.yml", "LICENSE"}
	for _, p := range excludedPrefixes {
		if strings.HasPrefix(f, p) {
			return false
		}
	}
	if slices.Contains(excludedFiles, f) {
		return false
	}
	switch filepath.Ext(f) {
	case ".md", ".go", ".yml", ".yaml", ".toml", ".sh", ".txtar":
		return true
	}
	return false
}
