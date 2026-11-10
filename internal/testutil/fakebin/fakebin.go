package fakebin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
)

// ExitInterrupted is the exit code of a blocking fake that was interrupted.
const ExitInterrupted = 130

const (
	specSuffix        = ".fakebin.json"
	callsSuffix       = ".fakebin.calls.jsonl"
	exitNoRule        = 127
	initialLineBuffer = 64 << 10
	maxCallLine       = 16 << 20 // one JSON line per call; argv can be long
)

// Spec describes a fake tool. The first rule whose Args is a prefix of the call's args answers it.
type Spec struct {
	Rules     []Rule   `json:"rules"`
	RecordEnv []string `json:"record_env,omitempty"` // env vars recorded with each call
}

// Rule is one canned response.
type Rule struct {
	Args            []string `json:"args,omitempty"`
	Stdout          string   `json:"stdout,omitempty"`
	StdoutFile      string   `json:"stdout_file,omitempty"` // appended after Stdout; relative to the fake's directory
	Stderr          string   `json:"stderr,omitempty"`
	ExitCode        int      `json:"exit_code,omitempty"`
	Block           bool     `json:"block,omitempty"`            // wait for SIGINT/SIGTERM, then exit 130
	IgnoreInterrupt bool     `json:"ignore_interrupt,omitempty"` // with Block: only a kill ends the process
}

// Call is one recorded invocation.
type Call struct {
	Args []string          `json:"args"`
	Dir  string            `json:"dir"`
	Env  map[string]string `json:"env,omitempty"`
}

// Main runs the fake when this process was started through an [Install]ed link, and the tests
// otherwise. Call it from TestMain.
func Main(m *testing.M) {
	Serve()
	os.Exit(m.Run())
}

// Serve runs the fake and exits when this process was started through an [Install]ed link, and
// returns otherwise. A TestMain that hands over to testscript.Main calls it first, because
// testscript.Main replaces [os.Args][0], where the fake finds its spec.
func Serve() {
	if spec, ok := loadSpec(os.Args[0]); ok {
		os.Exit(run(os.Args[0], os.Args[1:], spec, os.Stdout, os.Stderr))
	}
}

// Install links the test binary into a new temp directory as name and returns the link's path.
func Install(t testing.TB, name string, spec Spec) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("fakebin: %v", err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		target += ".exe"
		err = copyFile(exe, target)
	} else {
		err = os.Symlink(exe, target)
	}
	if err != nil {
		t.Fatalf("fakebin: install %s: %v", name, err)
	}
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("fakebin: %v", err)
	}
	if err = os.WriteFile(target+specSuffix, b, 0o600); err != nil {
		t.Fatalf("fakebin: %v", err)
	}
	return target
}

// Calls returns the invocations recorded for the fake at path, oldest first.
func Calls(t testing.TB, path string) []Call {
	t.Helper()
	f, err := os.Open(path + callsSuffix)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("fakebin: %v", err)
	}
	defer func() { _ = f.Close() }()
	var calls []Call
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, initialLineBuffer), maxCallLine)
	for sc.Scan() {
		var c Call
		if err = json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("fakebin: %v", err)
		}
		calls = append(calls, c)
	}
	if err = sc.Err(); err != nil {
		t.Fatalf("fakebin: %v", err)
	}
	return calls
}

func loadSpec(argv0 string) (Spec, bool) {
	b, err := os.ReadFile(argv0 + specSuffix) //nolint:gosec // G703: test helper reading its own sidecar spec.
	if err != nil {
		return Spec{}, false
	}
	var s Spec
	if err = json.Unmarshal(b, &s); err != nil {
		return Spec{}, false
	}
	return s, true
}

func run(argv0 string, args []string, spec Spec, stdout, stderr io.Writer) int {
	if err := record(argv0, args, spec); err != nil {
		_, _ = fmt.Fprintf(stderr, "fakebin: %v\n", err)
		return 1
	}
	idx := slices.IndexFunc(spec.Rules, func(r Rule) bool {
		return len(r.Args) <= len(args) && slices.Equal(r.Args, args[:len(r.Args)])
	})
	if idx < 0 {
		_, _ = fmt.Fprintf(stderr, "fakebin: no rule matches %q\n", args)
		return exitNoRule
	}
	rule := spec.Rules[idx]
	// Register before writing output: a caller that reacts to the output must not race the handler.
	sig := make(chan os.Signal, 1)
	if rule.Block {
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	}
	_, _ = io.WriteString(stdout, rule.Stdout)
	if rule.StdoutFile != "" {
		if err := copyTo(stdout, resolve(argv0, rule.StdoutFile)); err != nil {
			_, _ = fmt.Fprintf(stderr, "fakebin: %v\n", err)
			return 1
		}
	}
	_, _ = io.WriteString(stderr, rule.Stderr)
	if !rule.Block {
		return rule.ExitCode
	}
	for range sig {
		if !rule.IgnoreInterrupt {
			break
		}
	}
	return ExitInterrupted
}

func record(argv0 string, args []string, spec Spec) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	c := Call{Args: args, Dir: dir}
	if args == nil {
		c.Args = []string{}
	}
	for _, k := range spec.RecordEnv {
		if v, ok := os.LookupEnv(k); ok {
			if c.Env == nil {
				c.Env = map[string]string{}
			}
			c.Env[k] = v
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode call: %w", err)
	}
	//nolint:gosec // G703: test helper appending to its own sidecar call log.
	f, err := os.OpenFile(argv0+callsSuffix, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("record call: %w", err)
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("record call: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("record call: %w", err)
	}
	return nil
}

func resolve(argv0, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(argv0), p)
}

func copyTo(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open stdout file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err = io.Copy(w, f); err != nil {
		return fmt.Errorf("copy stdout file: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err = os.WriteFile(dst, b, 0o700); err != nil { //nolint:gosec // the copy must be executable.
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}
