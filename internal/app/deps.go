package app

import (
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/sassdavid/tfcensus/internal/config"
)

// Env is an injected environment, in [os.Environ] "KEY=value" form.
type Env []string

// Lookup returns the value of key and whether it is set.
func (e Env) Lookup(key string) (string, bool) {
	// Later entries win, as in os/exec.
	for _, kv := range slices.Backward(e) {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == key {
			return v, true
		}
	}
	return "", false
}

// Get returns the value of key, or "" when unset.
func (e Env) Get(key string) string {
	v, _ := e.Lookup(key)
	return v
}

// Deps carries everything a command needs from the outside world.
type Deps struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Env   Env
	Clock func() time.Time
	Getwd func() (string, error)

	// Logger is set by the root command before any RunE runs.
	Logger *slog.Logger
	// Config is nil until a command loads it.
	Config *config.Result
}

// IsTerminal reports whether w is a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
