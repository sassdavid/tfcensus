package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/config"
)

// Exit codes, as listed in docs/engineering/engineering-standards.md.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitFindings    = 3 // --fail-on matched
	ExitAuth        = 4
	ExitInterrupted = 130
)

// Run is the process entry point. args includes the program name, as [os.Args] does.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	d := &app.Deps{
		Stdin:  os.Stdin,
		Stdout: stdout,
		Stderr: stderr,
		Env:    app.Env(os.Environ()),
		Clock:  time.Now,
		Getwd:  os.Getwd,
	}
	if len(args) > 0 {
		args = args[1:]
	}
	return Execute(ctx, d, args)
}

// Execute runs the command line in args (without the program name) and returns the exit code.
func Execute(ctx context.Context, d *app.Deps, args []string) int {
	root, g := newRoot(d)
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	g.close()
	code := exitCode(ctx, err, g.started)
	if err != nil || code == ExitInterrupted {
		printError(d.Stderr, err, code, root.Name(), cmd.CommandPath())
	}
	return code
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageError(err error) error { return &exitError{code: ExitUsage, err: err} }

func exitCode(ctx context.Context, err error, started bool) int {
	if err == nil {
		if ctx.Err() != nil {
			return ExitInterrupted
		}
		return ExitOK
	}
	var ee *exitError
	switch {
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		return ExitInterrupted
	case errors.As(err, &ee):
		return ee.code
	case errors.Is(err, config.ErrInvalid):
		return ExitUsage
	case !started:
		// cobra failed before PersistentPreRunE: unknown command, bad flag or arguments.
		return ExitUsage
	default:
		return ExitError
	}
}

// problemLister is an error made of several problems, printed one per line under a header.
type problemLister interface {
	error
	Header() string
	ProblemLines() []string
}

func printError(w io.Writer, err error, code int, prog, cmdPath string) {
	if code == ExitInterrupted {
		_, _ = fmt.Fprintln(w, prog+": interrupted")
		return
	}
	if pl, ok := errors.AsType[problemLister](err); ok {
		_, _ = fmt.Fprintln(w, prog+": "+pl.Header()+":")
		for _, line := range pl.ProblemLines() {
			_, _ = fmt.Fprintln(w, "  "+line)
		}
		return
	}
	_, _ = fmt.Fprintf(w, "%s: %v\n", prog, err)
	if code == ExitUsage {
		_, _ = fmt.Fprintf(w, "Run '%s --help' for usage.\n", cmdPath)
	}
}
