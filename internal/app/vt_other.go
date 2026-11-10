//go:build !windows

package app

import "io"

// EnableVirtualTerminal reports whether w can show ANSI colors. Outside Windows it always can.
func EnableVirtualTerminal(io.Writer) bool { return true }
