package render_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/render"
)

func TestParseFormat(t *testing.T) {
	t.Parallel()
	for _, f := range render.AllFormats() {
		got, err := render.ParseFormat(string(f))
		require.NoError(t, err)
		assert.Equal(t, f, got)
	}
	got, err := render.ParseFormat("")
	require.NoError(t, err)
	assert.Equal(t, render.FormatAuto, got)
	_, err = render.ParseFormat("yaml")
	require.ErrorContains(t, err, `invalid format "yaml"`)
}

func TestFormatResolve(t *testing.T) {
	t.Parallel()
	assert.Equal(t, render.FormatTTY, render.FormatAuto.Resolve(true))
	assert.Equal(t, render.FormatPlain, render.FormatAuto.Resolve(false))
	assert.Equal(t, render.FormatJSON, render.FormatJSON.Resolve(true))
	require.NoError(t, render.FormatJSON.Check(render.FormatPlain, render.FormatJSON))
	require.ErrorContains(t, render.FormatSARIF.Check(render.FormatJSON), `format "sarif" is not supported`)
}

func TestUseColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode render.ColorMode
		env  map[string]string
		tty  bool
		want bool
	}{
		{"auto tty", render.ColorAuto, nil, true, true},
		{"auto pipe", render.ColorAuto, nil, false, false},
		{"NO_COLOR", render.ColorAuto, map[string]string{"NO_COLOR": "1"}, true, false},
		{"empty NO_COLOR ignored", render.ColorAuto, map[string]string{"NO_COLOR": ""}, true, true},
		{"CLICOLOR_FORCE", render.ColorAuto, map[string]string{"CLICOLOR_FORCE": "1"}, false, true},
		{"CLICOLOR_FORCE=0", render.ColorAuto, map[string]string{"CLICOLOR_FORCE": "0"}, false, false},
		{
			"NO_COLOR beats CLICOLOR_FORCE",
			render.ColorAuto,
			map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"},
			true,
			false,
		},
		{"always beats NO_COLOR", render.ColorAlways, map[string]string{"NO_COLOR": "1"}, false, true},
		{"never beats tty", render.ColorNever, nil, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lookup := func(k string) (string, bool) { v, ok := tt.env[k]; return v, ok }
			assert.Equal(t, tt.want, render.UseColor(tt.mode, lookup, tt.tty))
		})
	}
	_, err := render.ParseColorMode("sometimes")
	require.Error(t, err)
}

func TestEnvelopeJSON(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 9, 30, 15, 999, time.FixedZone("CET", 3600))
	var buf bytes.Buffer
	require.NoError(t, render.JSON(&buf, render.NewEnvelope("1.2.3", now, map[string]string{"a": "<b>"})))
	assert.JSONEq(t, `{
		"schema_version": "1",
		"tool": "tfcensus",
		"version": "1.2.3",
		"generated_at": "2026-11-10T08:30:15Z",
		"data": {"a": "<b>"},
		"errors": []
	}`, buf.String())
	assert.Contains(t, buf.String(), `"<b>"`, "HTML is not escaped")
}
