package main

import (
	"fmt"
	"os"

	"github.com/sassdavid/tfcensus/internal/validation"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// exitUsage is the exit code for a wrong command line.
const exitUsage = 2

func run(args []string) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: mise run validation:stats -- reviewed.csv")
		return exitUsage
	}
	f, err := os.Open(args[0]) //nolint:gosec // reading the CSV the reviewer names is the point
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "validationstats: %v\n", err)
		return 1
	}
	defer func() { _ = f.Close() }()
	s, err := validation.Read(f)
	if err == nil {
		err = s.Write(os.Stdout)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "validationstats: %s: %v\n", args[0], err)
		return 1
	}
	return 0
}
