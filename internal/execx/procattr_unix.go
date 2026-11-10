//go:build !windows

package execx

import "syscall"

func ownProcessGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
