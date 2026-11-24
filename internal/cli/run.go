package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/term"
)

// Exit codes. [ExitCodes] describes each one for the docs site.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitFindings    = 3 // --fail-on matched
	ExitAuth        = 4
	ExitInterrupted = 130
)

// ExitCode is one exit code and what it means.
type ExitCode struct {
	Code int
	Name string
	Desc string
}

// ExitCodes returns every exit code in ascending order.
func ExitCodes() []ExitCode {
	return []ExitCode{
		{ExitOK, "ok", "The command finished. With --fail-on, nothing matched."},
		{ExitError, "error", "The command failed: a read, a write, a child process or a check failed."},
		{
			ExitUsage,
			"usage",
			"Wrong usage: an unknown command or flag, a bad value or argument, an invalid config file, " +
				"or a prompt with no terminal.",
		},
		{ExitFindings, "findings", "A --fail-on gate matched. The report is still written."},
		{ExitAuth, "auth", "Credentials were missing or rejected for every source the command needed."},
		{ExitInterrupted, "interrupted", "Interrupted by Ctrl-C (SIGINT) or a cancelled context."},
	}
}

// universalExitCodes are the codes every runnable command can return.
func universalExitCodes() []int { return []int{ExitOK, ExitError, ExitUsage, ExitInterrupted} }

// Run is the process entry point. args includes the program name, as [os.Args] does.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	// Taking SIGPIPE turns a write to a closed pipe into an EPIPE error instead of a silent exit,
	// so a report piped into head still writes its --out files and keeps its exit code.
	pipe := make(chan os.Signal, 1)
	signal.Notify(pipe, syscall.SIGPIPE)
	defer signal.Stop(pipe)
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
	return Main(ctx, d, args)
}

// Main is [Execute] for a whole process. It also answers carapace's completion requests, which
// changes package-level state, so in-process tests call [Execute] instead.
func Main(ctx context.Context, d *app.Deps, args []string) int {
	return execute(ctx, d, args, true)
}

// Execute runs the command line in args (without the program name) and returns the exit code.
func Execute(ctx context.Context, d *app.Deps, args []string) int {
	return execute(ctx, d, args, false)
}

func execute(ctx context.Context, d *app.Deps, args []string, withCarapace bool) int {
	root, g := newRoot(d)
	if withCarapace {
		enableCarapace(root)
	}
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	g.close()
	if term.IsBrokenPipe(err) {
		err = nil // the reader has what it wanted
	}
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
