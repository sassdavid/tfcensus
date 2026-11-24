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
