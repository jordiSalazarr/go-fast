//go:build linux

package caller

import "syscall"

const getTermios = syscall.TCGETS
