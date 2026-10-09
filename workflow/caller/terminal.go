//go:build darwin || dragonfly || freebsd || netbsd || openbsd || linux

package caller

import (
	"os"
	"syscall"
	"unsafe"
)

// StdinIsTerminal reports whether gf's stdin is a terminal, the way isatty
// does: by asking the terminal driver for its settings. Claude Code's Bash
// tool runs without a terminal, and /dev/null, pipes and files fail this
// check, although /dev/null is a character device.
func StdinIsTerminal() bool { return isTerminal(os.Stdin) }

func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), getTermios, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
