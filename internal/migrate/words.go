package migrate

import (
	"errors"
	"strings"
)

var errUnterminated = errors.New("unterminated quote")

// splitWords splits an action string the way a POSIX shell splits words: blanks separate words,
// single quotes keep everything literal, double quotes keep blanks and honor \" and \\, and a
// backslash outside quotes escapes the next character. tfmigrate splits actions the same way.
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errUnterminated
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			j, err := doubleQuoted(s, i+1, &cur)
			if err != nil {
				return nil, err
			}
			i = j
			inWord = true
		case c == '\\' && i+1 < len(s):
			cur.WriteByte(s[i+1])
			i++
			inWord = true
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// doubleQuoted copies a double-quoted run starting at s[i] into cur and returns the index of the
// closing quote.
func doubleQuoted(s string, i int, cur *strings.Builder) (int, error) {
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return i, nil
		case c == '\\' && i+1 < len(s) && strings.IndexByte(`"\$`+"`", s[i+1]) >= 0:
			cur.WriteByte(s[i+1])
			i++
		default:
			cur.WriteByte(c)
		}
	}
	return 0, errUnterminated
}
