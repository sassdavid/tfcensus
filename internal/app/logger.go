package app

import (
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/lmittmann/tint"
)

// LevelTrace is the level enabled by -vvv.
const LevelTrace = slog.LevelDebug - 4

const (
	verbosityInfo  = 1
	verbosityDebug = 2
)

// LogFormat selects the slog handler.
type LogFormat string

// Supported log formats.
const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

// LogOptions configures [NewLogger].
type LogOptions struct {
	Format  LogFormat
	Verbose int // number of -v flags
	Quiet   bool
	Color   bool
}

// ParseLogFormat validates a --log-format value.
func ParseLogFormat(s string) (LogFormat, error) {
	switch f := LogFormat(s); f {
	case LogFormatText, LogFormatJSON:
		return f, nil
	default:
		return "", fmt.Errorf("invalid log format %q: want text or json", s)
	}
}

// Level maps -q and the -v count to a slog level. The default is warn.
func (o LogOptions) Level() slog.Level {
	switch {
	case o.Quiet:
		return slog.LevelError
	case o.Verbose >= verbosityDebug+1:
		return LevelTrace
	case o.Verbose == verbosityDebug:
		return slog.LevelDebug
	case o.Verbose == verbosityInfo:
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}

// NewLogger returns a tint text logger or a JSON logger writing to w.
func NewLogger(w io.Writer, o LogOptions) *slog.Logger {
	level := o.Level()
	if o.Format == LogFormatJSON {
		return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: renameTrace,
		}))
	}
	return slog.New(tint.NewTextHandler(w, &tint.Options{
		Level:       level,
		NoColor:     !o.Color,
		TimeFormat:  time.TimeOnly,
		ReplaceAttr: renameTrace,
	}))
}

// renameTrace prints LevelTrace as "TRACE" instead of "DEBUG-4".
func renameTrace(_ []string, a slog.Attr) slog.Attr {
	if a.Key != slog.LevelKey {
		return a
	}
	if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == LevelTrace {
		a.Value = slog.StringValue("TRACE")
	}
	return a
}
