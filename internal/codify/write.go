package codify

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrExists is returned when a file to write exists and force is off.
var ErrExists = errors.New("file exists")

// Write writes files into dir, which must exist. Without force it refuses before writing anything
// when one of them exists. Each file is written next to its final name and renamed.
func Write(dir string, files []File, force bool) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("check --into: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("check --into: %s is not a directory", dir)
	}
	for _, f := range files {
		if f.Name != filepath.Base(f.Name) || strings.HasPrefix(f.Name, ".") {
			return fmt.Errorf("write %q: not a plain file name", f.Name)
		}
		if !force {
			_, statErr := os.Lstat(filepath.Join(dir, f.Name))
			if statErr == nil {
				return fmt.Errorf("%w: %s (pass --force to overwrite)", ErrExists, filepath.Join(dir, f.Name))
			}
			if !errors.Is(statErr, fs.ErrNotExist) {
				return fmt.Errorf("check %s: %w", f.Name, statErr)
			}
		}
	}
	for _, f := range files {
		if err = writeFile(dir, f); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(dir string, f File) (err error) {
	tmp, err := os.CreateTemp(dir, "."+f.Name+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", f.Name, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(f.Content); err != nil {
		return fmt.Errorf("write %s: %w", f.Name, err)
	}
	if err = tmp.Chmod(0o644); err != nil { //nolint:mnd // a config file others read
		return fmt.Errorf("write %s: %w", f.Name, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", f.Name, err)
	}
	if err = os.Rename(tmp.Name(), filepath.Join(dir, f.Name)); err != nil {
		return fmt.Errorf("write %s: %w", f.Name, err)
	}
	return nil
}

// NextSteps returns the commands to run after codify in dir: plan with generated config, and
// terraform query when a query file was written. terragrunt says whether dir is a terragrunt unit.
func NextSteps(dir string, terragrunt bool, t Target, r *Result) []string {
	if len(r.Imports) == 0 {
		return []string{}
	}
	bin := "terraform"
	if t.Engine == EngineOpenTofu {
		bin = "tofu"
	}
	steps := []string{"cd " + dir}
	// terragrunt runs terraform inside .terragrunt-cache, so the output path must be absolute.
	if terragrunt {
		steps = append(steps, "terragrunt run -- plan -generate-config-out=$PWD/generated.tf")
	} else {
		steps = append(steps, bin+" plan -generate-config-out=generated.tf")
	}
	if len(r.Lists) > 0 {
		query := "terraform query -generate-config-out=generated_query.tf"
		if terragrunt {
			query = "terragrunt run -- query -generate-config-out=$PWD/generated_query.tf"
		}
		steps = append(steps, "or, for config of every resource of the listed types: "+query)
	}
	return append(
		steps,
		"review generated.tf, move the resources into your config, then plan until it shows no changes",
	)
}
