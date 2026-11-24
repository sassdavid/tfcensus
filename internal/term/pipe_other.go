//go:build !windows

package term

func isWindowsBrokenPipe(error) bool { return false }
