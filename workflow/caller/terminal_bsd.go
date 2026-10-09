//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package caller

import "syscall"

const getTermios = syscall.TIOCGETA
