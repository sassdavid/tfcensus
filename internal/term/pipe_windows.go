//go:build windows

package term

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isWindowsBrokenPipe matches the errors Windows returns for a write to a closed pipe.
func isWindowsBrokenPipe(err error) bool {
	return errors.Is(err, windows.ERROR_NO_DATA) || errors.Is(err, windows.ERROR_BROKEN_PIPE)
}
