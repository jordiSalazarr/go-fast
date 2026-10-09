package guardagentactions

import (
	"path"
	"strings"
)

// Best-effort reading of a Bash command: enough to find a gf owner-only
// command in any segment (after &&, ;, |, inside $() or backticks, behind env,
// sudo and similar wrappers, inside sh -c / eval, or via go run ./cmd/gf).
// The permission deny rules in the README are the hard layer.

var ownerOnlySubcommands = map[string]bool{"approve": true, "reject": true, "extend": true, "abandon": true}

// ownerOnlyCommand returns the first owner-only gf subcommand the command runs.
func ownerOnlyCommand(command string) (string, bool) {
	for _, words := range segments(command) {
		if sub, ok := ownerOnlyIn(words); ok {
			return sub, true
		}
	}
	return "", false
}

// segments splits a command line into simple commands, each a list of words
// with quotes removed. Command substitutions become segments of their own.
func segments(s string) [][]string {
	var (
		segs   [][]string
		words  []string
		word   strings.Builder
		inWord bool
	)
	flushWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	flushSegment := func() {
		flushWord()
		if len(words) > 0 {
			segs = append(segs, words)
			words = nil
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			word.WriteByte(s[i+1])
			inWord = true
			i++
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				end = len(s) - i - 1
			}
			word.WriteString(s[i+1 : i+1+end])
			inWord = true
			i += end + 1
		case c == '"':
			inWord = true
			for i++; i < len(s) && s[i] != '"'; i++ {
				switch {
				case s[i] == '\\' && i+1 < len(s):
					i++
					word.WriteByte(s[i])
				case s[i] == '$' && i+1 < len(s) && s[i+1] == '(':
					end := closingParen(s, i+1)
					segs = append(segs, segments(s[i+2:end])...)
					i = end
				case s[i] == '`':
					end := strings.IndexByte(s[i+1:], '`')
					if end < 0 {
						end = len(s) - i - 1
					}
					segs = append(segs, segments(s[i+1:i+1+end])...)
					i += end + 1
				default:
					word.WriteByte(s[i])
				}
			}
		case c == ' ' || c == '\t':
			flushWord()
		case strings.IndexByte(";&|\n()`{}", c) >= 0:
			flushSegment()
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			flushSegment()
			i++
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	flushSegment()
	return segs
}

// closingParen returns the index of the parenthesis closing the one at open,
// or len(s) when it is never closed.
func closingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s)
}

var wrappers = map[string]bool{
	"env": true, "sudo": true, "command": true, "exec": true, "nohup": true,
	"time": true, "nice": true, "xargs": true, "builtin": true, "doas": true,
}

// keywords may precede a command in a segment, e.g. `then gf approve`.
var keywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "do": true,
	"while": true, "until": true, "!": true,
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true}

func ownerOnlyIn(words []string) (string, bool) {
	words = unwrap(words)
	if len(words) == 0 {
		return "", false
	}
	prog := path.Base(words[0])
	args := words[1:]
	switch {
	case prog == "gf":
		return gfSubcommand(args)
	case prog == "go" && len(args) > 0 && args[0] == "run":
		for i, a := range args[1:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if pkg, _, _ := strings.Cut(a, "@"); path.Base(pkg) == "gf" {
				return gfSubcommand(args[i+2:])
			}
			return "", false
		}
	case shells[prog]:
		for i, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && i+1 < len(args) {
				return ownerOnlyCommand(args[i+1])
			}
		}
	case prog == "eval":
		return ownerOnlyCommand(strings.Join(args, " "))
	}
	return "", false
}

// unwrap drops leading variable assignments and wrapper commands with their
// options, e.g. `FOO=1 env -i sudo gf approve` → `gf approve`.
func unwrap(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case isAssignment(w), keywords[w]:
			words = words[1:]
		case wrappers[path.Base(w)]:
			words = words[1:]
			for len(words) > 0 && (strings.HasPrefix(words[0], "-") || isAssignment(words[0])) {
				words = words[1:]
			}
		default:
			return words
		}
	}
	return words
}

func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// gfSubcommand returns the subcommand of gf's arguments if it is owner-only.
func gfSubcommand(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dir":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, ownerOnlySubcommands[a]
		}
	}
	return "", false
}
