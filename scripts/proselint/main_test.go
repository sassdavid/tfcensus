package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// siteExcluded mirrors srcExclude in docs/.vitepress/config.mts: what the site doesn't publish.
func siteExcluded(rel string) bool {
	for _, p := range []string{"plan/", "engineering/", "validation/", "node_modules/", ".vitepress/dist/", ".vitepress/cache/"} {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return rel == "README.md" || rel == "dogfood.md"
}

func TestCheckedCoversPublishedFolders(t *testing.T) {
	t.Parallel()
	for _, f := range []string{
		"docs/index.md",
		"docs/install.md",
		"docs/getting-started.md",
		"docs/guides/scan.md",
		"docs/concepts/safety.md",
		"docs/reference/config.md",
		"docs/cli/index.md",
		"docs/ci/drift.yml",
		"docs/.vitepress/config.mts",
		"docs/.vitepress/sidebar.mts",
		"docs/.vitepress/tokens.mjs",
		"docs/.vitepress/theme/index.ts",
		"docs/.vitepress/theme/components/HomeHero.vue",
	} {
		if !checked(f) {
			t.Errorf("checked(%q) = false, want true: the site publishes it", f)
		}
	}
	for _, f := range []string{"docs/plan/milestones.md", "docs/engineering/stack.md", "docs/README.md"} {
		if checked(f) {
			t.Errorf("checked(%q) = true, want false", f)
		}
	}
}

func TestCheckedCoversEveryPublishedPage(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "docs")
	pages := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && siteExcluded(rel+"/") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(rel) != ".md" || siteExcluded(rel) {
			return nil
		}
		pages++
		if !checked("docs/" + rel) {
			t.Errorf("lint:prose skips docs/%s, which the site publishes", rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if pages == 0 {
		t.Fatal("found no published pages under docs/")
	}
}
