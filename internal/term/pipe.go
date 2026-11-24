package term

import (
	"errors"
	"syscall"
)

// IsBrokenPipe reports whether err comes from writing to a pipe whose reader has gone, as when
// output goes to `head`.
func IsBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || isWindowsBrokenPipe(err)
}
