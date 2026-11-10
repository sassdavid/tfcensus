package app_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
)

func TestEnvLookup(t *testing.T) {
	t.Parallel()
	e := app.Env{"A=1", "B=", "A=2", "BROKEN"}
	v, ok := e.Lookup("A")
	assert.True(t, ok)
	assert.Equal(t, "2", v, "later entries win")
	v, ok = e.Lookup("B")
	assert.True(t, ok)
	assert.Empty(t, v)
	_, ok = e.Lookup("BROKEN")
	assert.False(t, ok)
	assert.Empty(t, e.Get("missing"))
}

func TestLogLevel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		opts app.LogOptions
		want slog.Level
	}{
		{app.LogOptions{}, slog.LevelWarn},
		{app.LogOptions{Verbose: 1}, slog.LevelInfo},
		{app.LogOptions{Verbose: 2}, slog.LevelDebug},
		{app.LogOptions{Verbose: 3}, app.LevelTrace},
		{app.LogOptions{Verbose: 5}, app.LevelTrace},
		{app.LogOptions{Quiet: true}, slog.LevelError},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.opts.Level(), "%+v", tt.opts)
	}
}

func TestNewLogger(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := app.NewLogger(&buf, app.LogOptions{Format: app.LogFormatJSON, Verbose: 3})
	log.Log(t.Context(), app.LevelTrace, "dump", "url", "https://example.com")
	assert.Contains(t, buf.String(), `"level":"TRACE"`)

	buf.Reset()
	log = app.NewLogger(&buf, app.LogOptions{Format: app.LogFormatText})
	log.InfoContext(t.Context(), "hidden")
	log.WarnContext(t.Context(), "shown", "tool", "terraform")
	out := buf.String()
	assert.NotContains(t, out, "hidden")
	assert.Contains(t, out, "WRN shown tool=terraform")
	assert.NotContains(t, out, "\x1b[", "no ANSI without Color")

	_, err := app.ParseLogFormat("xml")
	require.Error(t, err)
}
