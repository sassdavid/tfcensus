package main

import (
	"cmp"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sassdavid/tfcensus/internal/typemap/gen"
)

func main() {
	os.Exit(run())
}

func run() int {
	out := flag.String("out", filepath.Join("internal", "typemap", "data"), "output directory")
	flag.Parse()
	home, err := os.UserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "typemapgen: %v\n", err)
		return 1
	}
	awsDir := cmp.Or(os.Getenv(gen.EnvAWSDir), gen.DefaultDir(home, gen.RepoAWS))
	awsccDir := cmp.Or(os.Getenv(gen.EnvAWSCCDir), gen.DefaultDir(home, gen.RepoAWSCC))
	res, err := gen.Generate(awsDir, awsccDir)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "typemapgen: %v (set %s and %s)\n", err, gen.EnvAWSDir, gen.EnvAWSCCDir)
		return 1
	}
	for name, b := range map[string][]byte{"generated.yaml": res.YAML, "generated.yaml.LICENSE": res.License} {
		if err = os.WriteFile(filepath.Join(*out, name), b, 0o600); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "typemapgen: %v\n", err)
			return 1
		}
	}
	return 0
}
