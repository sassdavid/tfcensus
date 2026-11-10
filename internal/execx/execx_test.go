package execx_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
)

func TestMain(m *testing.M) { fakebin.Main(m) }

// newRunner returns a runner whose parent environment is exactly env plus PATH=dir(bin).
func newRunner(t *testing.T, bin string, env []string, opts ...execx.Option) (*execx.Runner, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	parent := append([]string{"PATH=" + filepath.Dir(bin)}, env...)
	opts = append([]execx.Option{execx.WithEnviron(func() []string { return parent })}, opts...)
	return execx.NewRunner(logger, opts...), &logs
}

func TestRunCapturesOutput(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: []fakebin.Rule{
		{Args: []string{"find", "--json"}, Stdout: `[{"path":"live/prod"}]`, Stderr: "warn: slow\n"},
	}})
	r, _ := newRunner(t, bin, nil)
	dir := t.TempDir()

	res, err := r.Run(
		t.Context(),
		execx.Cmd{Name: "terragrunt", Args: []string{"find", "--json", "--tf-path", "terraform"}, Dir: dir},
	)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"path":"live/prod"}]`, string(res.Stdout))
	assert.Equal(t, "warn: slow\n", string(res.Stderr))
	assert.Equal(t, 0, res.ExitCode)

	calls := fakebin.Calls(t, bin)
	require.Len(t, calls, 1)
	assert.Equal(t, []string{"find", "--json", "--tf-path", "terraform"}, calls[0].Args)
	wantDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	gotDir, err := filepath.EvalSymlinks(calls[0].Dir)
	require.NoError(t, err)
	assert.Equal(t, wantDir, gotDir)
}

func TestRunStreamsStdout(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terraform", fakebin.Spec{Rules: []fakebin.Rule{{Stdout: "streamed", Stderr: "e"}}})
	r, _ := newRunner(t, bin, nil)
	var out, errOut bytes.Buffer

	res, err := r.Run(t.Context(), execx.Cmd{Name: bin, Stdout: &out, Stderr: &errOut})
	require.NoError(t, err)
	assert.Equal(t, "streamed", out.String())
	assert.Empty(t, res.Stdout)
	assert.Equal(t, "e", errOut.String())
	assert.Equal(t, "e", string(res.Stderr), "stderr is captured even when streamed")
}

func TestRunExitError(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "tofu", fakebin.Spec{Rules: []fakebin.Rule{
		{Stderr: "Error: backend not initialized\n", ExitCode: 3},
	}})
	r, _ := newRunner(t, bin, nil)

	res, err := r.Run(t.Context(), execx.Cmd{Name: "tofu", Args: []string{"show", "-json"}})
	var ee *execx.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 3, ee.Code)
	assert.Equal(t, "tofu exited with code 3: Error: backend not initialized", ee.Error())
	require.NotNil(t, res)
	assert.Equal(t, 3, res.ExitCode)
}

func TestRunEnvAllowlist(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{
		Rules: []fakebin.Rule{{}},
		RecordEnv: []string{
			"AWS_PROFILE",
			"GITHUB_TOKEN",
			"TFCENSUS_CACHE_TTL",
			"TF_IN_AUTOMATION",
			"TG_NON_INTERACTIVE",
		},
	})
	r, _ := newRunner(
		t,
		bin,
		[]string{
			"AWS_PROFILE=acme-nonprod",
			"GITHUB_TOKEN=ghp_secret",
			"TFCENSUS_CACHE_TTL=1h",
			"TG_NON_INTERACTIVE=true",
		},
	)

	_, err := r.Run(t.Context(), execx.Cmd{Name: "terragrunt", Env: []string{"TF_IN_AUTOMATION=1"}})
	require.NoError(t, err)
	calls := fakebin.Calls(t, bin)
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]string{
		"AWS_PROFILE":        "acme-nonprod",
		"TF_IN_AUTOMATION":   "1",
		"TG_NON_INTERACTIVE": "true",
	}, calls[0].Env)
}

func TestRunNotFound(t *testing.T) {
	t.Parallel()

	r := execx.NewRunner(slog.New(slog.DiscardHandler), execx.WithEnviron(func() []string {
		return []string{"PATH=" + t.TempDir()}
	}))
	_, err := r.Run(t.Context(), execx.Cmd{Name: "terragrunt"})
	require.ErrorIs(t, err, execx.ErrNotFound)

	_, err = r.Run(t.Context(), execx.Cmd{Name: filepath.Join(t.TempDir(), "terragrunt")})
	require.ErrorIs(t, err, execx.ErrNotFound)

	_, err = r.Run(t.Context(), execx.Cmd{})
	require.ErrorIs(t, err, execx.ErrNotFound)
}

func TestRunIgnoresRelativePathEntries(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: []fakebin.Rule{{}}})
	binDir := filepath.Dir(bin)
	r := execx.NewRunner(slog.New(slog.DiscardHandler), execx.WithEnviron(func() []string {
		return []string{"PATH=" + filepath.Base(binDir)}
	}))
	_, err := r.Run(t.Context(), execx.Cmd{Name: "terragrunt", Dir: filepath.Dir(binDir)})
	require.ErrorIs(t, err, execx.ErrNotFound)
}

func TestRunTimeoutInterrupts(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terraform", fakebin.Spec{Rules: []fakebin.Rule{{Block: true}}})
	r, _ := newRunner(t, bin, nil)

	res, err := r.Run(t.Context(), execx.Cmd{Name: "terraform", Timeout: 500 * time.Millisecond})
	require.ErrorIs(t, err, execx.ErrTimeout)
	require.NotNil(t, res)
}

func TestRunTimeoutKillsAfterWaitDelay(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terraform", fakebin.Spec{Rules: []fakebin.Rule{{Block: true, IgnoreInterrupt: true}}})
	r, _ := newRunner(t, bin, nil, execx.WithWaitDelay(200*time.Millisecond), execx.WithTimeout(500*time.Millisecond))

	_, err := r.Run(t.Context(), execx.Cmd{Name: "terraform"})
	require.ErrorIs(t, err, execx.ErrTimeout)
}

func TestRunContextCanceled(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: []fakebin.Rule{{Block: true, Stdout: "ready"}}})
	r, _ := newRunner(t, bin, nil)
	ctx, cancel := context.WithCancel(t.Context())
	stdout := &signalWriter{ch: make(chan struct{}, 1)}

	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, execx.Cmd{Name: "terragrunt", Stdout: stdout})
		done <- err
	}()
	<-stdout.ch // the fake is running and has its signal handler installed
	cancel()
	err := <-done
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, execx.ErrTimeout)
}

func TestRunLogsRedactedCommand(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terraform", fakebin.Spec{Rules: []fakebin.Rule{{}}})
	r, logs := newRunner(t, bin, nil)

	_, err := r.Run(t.Context(), execx.Cmd{
		Name: "terraform",
		Args: []string{"plan", "-var", "db_password=hunter2", "--token", "abc123"},
		Env:  []string{"AWS_SECRET_ACCESS_KEY=topsecret"},
	})
	require.NoError(t, err)
	out := logs.String()
	assert.Contains(t, out, `"msg":"exec start"`)
	assert.Contains(t, out, `"extra_env":["AWS_SECRET_ACCESS_KEY"]`)
	assert.Contains(t, out, `"msg":"exec done"`)
	for _, secret := range []string{"hunter2", "abc123", "topsecret"} {
		assert.NotContains(t, out, secret)
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want []string
	}{
		{in: []string{"plan", "-out", "tfplan"}, want: []string{"plan", "-out", "tfplan"}},
		{in: []string{"--token", "abc"}, want: []string{"--token", "***"}},
		{in: []string{"--api-key=abc"}, want: []string{"--api-key=***"}},
		{in: []string{"-var", "db_password=x"}, want: []string{"-var", "db_password=***"}},
		{in: []string{"-var=db_password=x"}, want: []string{"-var=db_password=***"}},
		{in: []string{"-var=region=eu-west-1"}, want: []string{"-var=region=eu-west-1"}},
		{
			in:   []string{"-backend-config=key=prod/terraform.tfstate"},
			want: []string{"-backend-config=key=prod/terraform.tfstate"},
		},
		{in: []string{"AWS_SECRET_ACCESS_KEY=x"}, want: []string{"AWS_SECRET_ACCESS_KEY=***"}},
		{in: []string{"--auth"}, want: []string{"--auth"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.in, " "), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, execx.Redact(tt.in))
		})
	}
}

type signalWriter struct{ ch chan struct{} }

func (w *signalWriter) Write(p []byte) (int, error) {
	select {
	case w.ch <- struct{}{}:
	default:
	}
	return len(p), nil
}
