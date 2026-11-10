package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// DefaultTimeout is the per-command timeout when neither [WithTimeout] nor [Cmd.Timeout] sets one.
const DefaultTimeout = 30 * time.Minute

// DefaultWaitDelay is how long a child gets between the interrupt and the kill.
const DefaultWaitDelay = 10 * time.Second

const (
	stderrTailBytes = 4 << 10 // trailing stderr kept for error messages
	exitInterrupted = 130
	goosWindows     = "windows"
)

// ErrNotFound is returned when the executable isn't on the child's PATH or isn't executable.
var ErrNotFound = errors.New("executable not found")

// ErrTimeout is returned when a command runs longer than its timeout.
var ErrTimeout = errors.New("command timed out")

// ExitError reports a non-zero exit. Stderr holds the trailing captured stderr.
type ExitError struct {
	Name   string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s exited with code %d", e.Name, e.Code)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

// DefaultEnvAllow lists the environment variables passed to child processes. Entries ending in "*"
// are prefixes. Names match case-insensitively on Windows, where PATH is often spelled Path.
func DefaultEnvAllow() []string {
	return []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TEMP", "TMP", "TZ", "TERM",
		"LANG", "LC_*", "XDG_*",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
		"SSL_CERT_FILE", "SSL_CERT_DIR",
		"AWS_*", "TF_*", "TG_*", "TERRAGRUNT_*", "TOFU_*", "OPENTOFU_*",
		"GIT_*", "SSH_AUTH_SOCK",
		"SYSTEMROOT", "SystemRoot", "COMSPEC", "PATHEXT", "WINDIR", "APPDATA", "LOCALAPPDATA",
		"USERPROFILE", "PROGRAMDATA", "ProgramFiles", "ProgramFiles(x86)",
	}
}

// Runner starts commands. The zero value is not usable; use [NewRunner].
type Runner struct {
	logger    *slog.Logger
	environ   func() []string
	allow     []string
	timeout   time.Duration
	waitDelay time.Duration
}

// Option configures a Runner.
type Option func(*Runner)

// WithEnviron sets the parent environment the allowlist is applied to (default [os.Environ]).
func WithEnviron(f func() []string) Option { return func(r *Runner) { r.environ = f } }

// WithEnvAllow replaces the environment allowlist.
func WithEnvAllow(allow []string) Option { return func(r *Runner) { r.allow = allow } }

// WithTimeout sets the default per-command timeout.
func WithTimeout(d time.Duration) Option { return func(r *Runner) { r.timeout = d } }

// WithWaitDelay sets how long a child gets between the interrupt and the kill.
func WithWaitDelay(d time.Duration) Option { return func(r *Runner) { r.waitDelay = d } }

// NewRunner returns a Runner that logs through logger.
func NewRunner(logger *slog.Logger, opts ...Option) *Runner {
	r := &Runner{
		logger:    logger,
		environ:   os.Environ,
		allow:     DefaultEnvAllow(),
		timeout:   DefaultTimeout,
		waitDelay: DefaultWaitDelay,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Cmd describes one invocation.
type Cmd struct {
	Name    string // looked up on the child's PATH unless it contains a path separator
	Args    []string
	Dir     string
	Env     []string // extra KEY=VALUE pairs, passed regardless of the allowlist; only keys are logged
	Stdin   io.Reader
	Stdout  io.Writer     // nil captures into Result.Stdout
	Stderr  io.Writer     // also receives stderr, which is captured either way
	Timeout time.Duration // overrides the Runner default; negative disables it
}

// Result holds a finished command's output.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Duration time.Duration
}

// Run starts the command and waits for it. On cancellation or timeout the child gets one interrupt,
// so terraform can release its state lock, and is killed after the wait delay. A non-zero exit is
// returned as *[ExitError] together with the Result.
func (r *Runner) Run(ctx context.Context, c Cmd) (*Result, error) {
	env := r.childEnv(c.Env)
	path, err := lookPath(c.Name, envValue(env, "PATH"), envValue(env, "PATHEXT"), c.Dir)
	if err != nil {
		return nil, err
	}

	timeout := r.timeout
	if c.Timeout != 0 {
		timeout = c.Timeout
	}
	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeoutCause(ctx, timeout, ErrTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = env
	cmd.Stdin = c.Stdin
	// A terminal Ctrl-C goes to the whole foreground process group. Children get their own group, so
	// the only interrupt they see is the one Cancel sends; terraform exits without cleanup on a second.
	cmd.SysProcAttr = ownProcessGroup()
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = r.waitDelay

	var stdout bytes.Buffer
	stderr := &tailBuffer{max: stderrTailBytes}
	if c.Stdout != nil {
		cmd.Stdout = c.Stdout
	} else {
		cmd.Stdout = &stdout
	}
	cmd.Stderr = stderr
	if c.Stderr != nil {
		cmd.Stderr = io.MultiWriter(stderr, c.Stderr)
	}

	r.logger.DebugContext(ctx, "exec start",
		slog.String("cmd", filepath.Base(c.Name)),
		slog.Any("args", Redact(c.Args)),
		slog.String("dir", c.Dir),
		slog.Any("extra_env", envKeys(c.Env)))

	start := time.Now()
	runErr := cmd.Run()
	res := &Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Duration: time.Since(start)}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}

	r.logger.DebugContext(ctx, "exec done",
		slog.String("cmd", filepath.Base(c.Name)),
		slog.Int("exit_code", res.ExitCode),
		slog.Duration("duration", res.Duration))

	return res, r.classify(ctx, runCtx, c.Name, res, runErr)
}

func (r *Runner) classify(ctx, runCtx context.Context, name string, res *Result, runErr error) error {
	if runErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("run %s: %w", name, context.Cause(ctx))
	}
	if errors.Is(context.Cause(runCtx), ErrTimeout) {
		return fmt.Errorf("run %s: %w", name, ErrTimeout)
	}
	if _, ok := errors.AsType[*exec.ExitError](runErr); ok {
		if res.ExitCode < 0 {
			res.ExitCode = exitInterrupted
		}
		return &ExitError{Name: filepath.Base(name), Code: res.ExitCode, Stderr: string(res.Stderr)}
	}
	return fmt.Errorf("run %s: %w", name, runErr)
}

func (r *Runner) childEnv(extra []string) []string {
	var env []string
	for _, kv := range r.environ() {
		key, _, ok := strings.Cut(kv, "=")
		if ok && allowed(key, r.allow) {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

func allowed(key string, allow []string) bool {
	return slices.ContainsFunc(allow, func(pattern string) bool {
		k, p := key, pattern
		if runtime.GOOS == goosWindows {
			k, p = strings.ToUpper(k), strings.ToUpper(p)
		}
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			return strings.HasPrefix(k, prefix)
		}
		return k == p
	})
}

// envValue returns the last value of key in env; later entries win, as in [exec.Cmd].
func envValue(env []string, key string) string {
	for _, e := range slices.Backward(env) {
		k, v, ok := strings.Cut(e, "=")
		if ok && (k == key || (runtime.GOOS == goosWindows && strings.EqualFold(k, key))) {
			return v
		}
	}
	return ""
}

func envKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	return keys
}

// lookPath searches the child's PATH, not the parent's, so the allowlisted environment decides which
// binary runs.
func lookPath(name, pathEnv, pathExt, dir string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty command name", ErrNotFound)
	}
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		p := name
		if !filepath.IsAbs(p) && dir != "" {
			p = filepath.Join(dir, p)
		}
		if isExecutable(p) {
			return p, nil
		}
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" || !filepath.IsAbs(d) {
			continue // same reasoning as exec.ErrDot: never run a binary from a relative PATH entry
		}
		for _, candidate := range candidates(filepath.Join(d, name), pathExt) {
			if isExecutable(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %q not found on PATH", ErrNotFound, name)
}

func candidates(p, pathExt string) []string {
	if runtime.GOOS != goosWindows || filepath.Ext(p) != "" {
		return []string{p}
	}
	if pathExt == "" {
		pathExt = ".COM;.EXE;.BAT;.CMD"
	}
	var out []string
	for ext := range strings.SplitSeq(pathExt, ";") {
		if ext != "" {
			out = append(out, p+strings.ToLower(ext))
		}
	}
	return out
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return runtime.GOOS == goosWindows || fi.Mode().Perm()&0o111 != 0
}

// interrupt sends SIGINT. Windows can't signal a child process, so there it kills.
func interrupt(p *os.Process) error {
	if runtime.GOOS == goosWindows {
		return p.Kill()
	}
	return p.Signal(os.Interrupt)
}

// tailBuffer keeps only the last max bytes written.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte { return t.buf }
