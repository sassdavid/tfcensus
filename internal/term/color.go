package term

import "fmt"

// ColorMode is the --color flag value.
type ColorMode string

// Color modes.
const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// ParseColorMode validates a --color value.
func ParseColorMode(s string) (ColorMode, error) {
	switch m := ColorMode(s); m {
	case ColorAuto, ColorAlways, ColorNever:
		return m, nil
	default:
		return "", fmt.Errorf("invalid color mode %q: want auto, always or never", s)
	}
}

// UseColor reports whether a stream gets ANSI styles. The first rule that applies wins:
// --color always or never, a non-empty NO_COLOR (off), CLICOLOR_FORCE or FORCE_COLOR set to
// anything but empty, 0 or false (on), CLICOLOR=0 or TERM=dumb (off), then whether the stream is
// a terminal.
func UseColor(mode ColorMode, lookup func(string) (string, bool), isTTY bool) bool {
	switch mode {
	case ColorAlways:
		return true
	case ColorNever:
		return false
	case ColorAuto:
	}
	get := func(k string) string { v, _ := lookup(k); return v }
	switch {
	case get("NO_COLOR") != "":
		return false
	case forced(get("CLICOLOR_FORCE")) || forced(get("FORCE_COLOR")):
		return true
	case get("CLICOLOR") == "0" || get("TERM") == "dumb":
		return false
	}
	return isTTY
}

func forced(v string) bool { return v != "" && v != "0" && v != "false" }
