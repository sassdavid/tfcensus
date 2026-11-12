package prompt_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/prompt"
)

func lookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func TestAllowed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		stdin, stdout bool
		env           map[string]string
		noInput, want bool
	}{
		{"terminal", true, true, nil, false, true},
		{"piped stdin", false, true, nil, false, false},
		{"piped stdout", true, false, nil, false, false},
		{"CI set", true, true, map[string]string{"CI": "true"}, false, false},
		{"--no-input", true, true, nil, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, prompt.Allowed(tt.stdin, tt.stdout, lookup(tt.env), tt.noInput))
		})
	}
}

func TestLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input string
		want        error
	}{
		{"match", "live/prod/network-sg\n", nil},
		{"match without newline", "  live/prod/network-sg  ", nil},
		{"y is not enough", "y\n", prompt.ErrDeclined},
		{"empty", "", prompt.ErrDeclined},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			err := prompt.NewLine(strings.NewReader(tt.input), &out).
				ConfirmTyped(t.Context(), "Push 3 states.", "live/prod/network-sg")
			if tt.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.want)
			}
			assert.Equal(t, "Push 3 states.\nType \"live/prod/network-sg\" to confirm: ", out.String())
		})
	}
}

func TestLineCancel(t *testing.T) {
	t.Parallel()
	r, w := io.Pipe()
	t.Cleanup(func() { _ = w.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := prompt.NewLine(r, io.Discard).ConfirmTyped(ctx, "m", "x")
	require.ErrorIs(t, err, context.Canceled)
}

func TestGate(t *testing.T) {
	t.Parallel()
	require.NoError(t, prompt.Gate{Yes: true}.Confirm(t.Context(), "m", "x"))
	require.ErrorIs(t, prompt.Gate{}.Confirm(t.Context(), "m", "x"), prompt.ErrNoInput)
	p := prompt.NewLine(strings.NewReader("x\n"), io.Discard)
	require.NoError(t, prompt.Gate{Prompter: p}.Confirm(t.Context(), "m", "x"))
}
