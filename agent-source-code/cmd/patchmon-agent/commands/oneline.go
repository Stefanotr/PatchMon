package commands

import (
	"strings"
	"unicode"
)

// oneLine makes s safe to print as a single line of output or to send as a
// one-line message: control characters become spaces. An empty result reads
// "(unnamed)".
func oneLine(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	if s == "" {
		return "(unnamed)"
	}
	return s
}
