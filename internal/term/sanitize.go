package term

import "strings"

const (
	firstPrintable = 0x20 // characters below this are C0 controls
	del            = 0x7f
	firstC1        = 0x80
	afterC1        = 0xa0
	caretOffset    = 0x40 // caret notation shows control c as ^ followed by c+0x40
)

// Sanitize makes control characters in text from outside the program visible, so a release
// note or a tag value can't move the cursor, set the title or write the clipboard. Tabs and
// newlines stay; a carriage return before a newline is dropped. Other C0 characters and DEL
// become caret notation (^[ for ESC), and C1 characters become ^[ plus their 7-bit form.
func Sanitize(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/8) // room for some caret pairs
	for i, r := range s {
		switch {
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r == '\r' && strings.HasPrefix(s[i+1:], "\n"):
		case r < firstPrintable:
			b.WriteByte('^')
			b.WriteRune(r + caretOffset)
		case r == del:
			b.WriteString("^?")
		case r >= firstC1 && r < afterC1:
			b.WriteString("^[")
			b.WriteRune(r - caretOffset)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isControl(r rune) bool {
	return (r < firstPrintable && r != '\t' && r != '\n') || (r >= del && r < afterC1)
}
