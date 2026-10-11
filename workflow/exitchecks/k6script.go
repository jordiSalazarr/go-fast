package exitchecks

import (
	"fmt"
	"regexp"
	"strings"
)

// A stress script only generates its NFR's traffic: gf sets the load and
// measures the run itself. k6 (checked with v2.1.0) already gives the CLI
// the last word on most of that: --vus and --duration replace the script's
// vus, duration, iterations, stages and scenarios ("cli level configuration
// overrode scenarios configuration entirely"), --no-thresholds makes its
// thresholds inert, and --summary-mode disabled keeps handleSummary from
// running. The script check refuses all of them anyway, so a script says
// what it does, and refuses what the CLI doesn't override: rps and
// minIterationDuration would throttle the load.

// forbiddenOptions are the k6 options a stress script may not set.
var forbiddenOptions = []string{"vus", "duration", "iterations", "stages", "scenarios", "thresholds", "rps", "minIterationDuration"}

var (
	handleSummaryRe   = regexp.MustCompile(`\bhandleSummary\b`)
	optionsExportRe   = regexp.MustCompile(`\bexport\s+(?:const|let|var)\s+options\s*=\s*`)
	optionsReexportRe = regexp.MustCompile(`\bexport\s*\{[^}]*\boptions\b[^}]*\}`)
	optionsAssignRe   = regexp.MustCompile(`\boptions\s*(?:\.\s*([A-Za-z_$][\w$]*)|\[\s*['"]([^'"]*)['"]\s*\])\s*=[^=]`)
	optionKeyRe       = regexp.MustCompile(`(?:^|[{,])\s*(?:([A-Za-z_$][\w$]*)|"([^"]*)"|'([^']*)')\s*(?::|,|}|$)`)
	computedKeyRe     = regexp.MustCompile(`[{,]\s*\[`)
)

// scriptProblems says why a k6 script may not run under gf: it sets the
// load, throttles it, or judges the run. The check reads the source, with
// comments and string contents blanked out; it is strict rather than clever:
// options must be an object literal exported as `export const options =
// {...}`, without spreads or computed keys.
func scriptProblems(src string) []string {
	code := blankCommentsAndStrings(src)
	var problems []string
	if handleSummaryRe.MatchString(code) {
		problems = append(problems, "defines handleSummary (gf measures the run itself)")
	}
	if optionsReexportRe.MatchString(code) {
		problems = append(problems, "exports options by name; export them as `export const options = {...}`")
	}
	for _, m := range optionsAssignRe.FindAllStringSubmatch(code, -1) {
		key := m[1] + m[2]
		if forbidden(key) {
			problems = append(problems, fmt.Sprintf("sets options.%s", key))
		}
	}
	if loc := optionsExportRe.FindStringIndex(code); loc != nil {
		literal, ok := objectLiteral(code[loc[1]:])
		switch {
		case !ok:
			problems = append(problems, "exports options that aren't an object literal; use `export const options = {...}`")
		case strings.Contains(literal, "..."):
			problems = append(problems, "spreads other values into options; list each option in the literal")
		default:
			problems = append(problems, forbiddenKeys(literal)...)
		}
	}
	return problems
}

// forbiddenKeys names the forbidden options a literal sets, at any depth, so
// a scenario's executor settings are caught too.
func forbiddenKeys(literal string) []string {
	var problems []string
	seen := map[string]bool{}
	if computedKeyRe.MatchString(literal) {
		problems = append(problems, "uses computed keys in options; write each option's name")
	}
	for _, m := range optionKeyRe.FindAllStringSubmatch(literal, -1) {
		key := m[1] + m[2] + m[3]
		if forbidden(key) && !seen[key] {
			seen[key] = true
			problems = append(problems, fmt.Sprintf("sets options.%s (gf sets the load from the NFR)", key))
		}
	}
	return problems
}

func forbidden(key string) bool {
	for _, f := range forbiddenOptions {
		if key == f {
			return true
		}
	}
	return false
}

// objectLiteral returns the balanced {...} at the start of code, which has
// its comments and strings blanked.
func objectLiteral(code string) (string, bool) {
	if !strings.HasPrefix(code, "{") {
		return "", false
	}
	depth := 0
	for i, r := range code {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return code[:i+1], true
			}
		}
	}
	return "", false
}

// blankCommentsAndStrings replaces comments with spaces, and the contents of
// string and template literals with 'x', keeping their quotes and line
// breaks. A string used as an object key or an index keeps its text.
func blankCommentsAndStrings(src string) string {
	out := []byte(src)
	n := len(src)
	for i := 0; i < n; {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < n && src[i] != '\n' {
				out[i] = ' '
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			stop := n
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			for ; i < stop; i++ {
				if src[i] != '\n' {
					out[i] = ' '
				}
			}
		case src[i] == '"' || src[i] == '\'' || src[i] == '`':
			quote := src[i]
			start := i
			i++
			for i < n && src[i] != quote {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			if i < n {
				i++ // the closing quote
			}
			end := min(i, n)
			// A quoted object key ("thresholds": ...) or index (options["vus"])
			// keeps its text.
			if quote != '`' && (keyFollows(src[end:]) || indexPrecedes(src[:start])) {
				continue
			}
			for j := start + 1; j < end-1; j++ {
				if out[j] != '\n' {
					out[j] = 'x'
				}
			}
		default:
			i++
		}
	}
	return string(out)
}

// indexPrecedes reports whether before ends, after spaces, with the bracket
// of an index.
func indexPrecedes(before string) bool {
	return strings.HasSuffix(strings.TrimRight(before, " \t\r\n"), "[")
}

// keyFollows reports whether rest starts, after spaces, with the colon of an
// object key.
func keyFollows(rest string) bool {
	return strings.HasPrefix(strings.TrimLeft(rest, " \t\r\n"), ":")
}
