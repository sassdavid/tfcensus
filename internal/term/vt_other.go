//go:build !windows

package term

import "io"

// enableVirtualTerminal reports whether w can show ANSI styles. Outside Windows it always can.
func enableVirtualTerminal(io.Writer) bool { return true }
