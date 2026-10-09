//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd || linux)

package caller

// StdinIsTerminal fails closed where gf cannot tell: owner-only commands are
// refused.
func StdinIsTerminal() bool { return false }
