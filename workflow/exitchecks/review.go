package exitchecks

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// Sections of review's artifact, as the review skill writes it.
const (
	VerdictHeading         = "Verdict"
	BlockingHeading        = "Blocking"
	RiskMapHeading         = "Risk map"
	AgreedBehaviourHeading = "Agreed behaviour"
)

// blockingFinding is a blocking finding's heading: `### B1 <title>`.
var blockingFinding = regexp.MustCompile(`^###\s+(B\d+)\b`)

// reviewVerdictConsistent: a passing review's artifact says Passed, lists
// no blocking finding, and has its risk map and agreed behaviour.
func (c *checkRun) reviewVerdictConsistent() (finding, error) {
	artifact := domain.ArtifactFor(c.visit.Work, c.visit.Stage, c.visit.Visit)
	content, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(artifact.String())))
	if errors.Is(err, os.ErrNotExist) {
		return fail("%s is missing", artifact), nil
	}
	if err != nil {
		return finding{}, err
	}
	text := string(content)
	var problems []string
	verdict, found := markdownSection(text, VerdictHeading)
	first, _, _ := strings.Cut(verdict, "\n")
	switch {
	case !found:
		problems = append(problems, "no '## Verdict' section")
	case !strings.HasPrefix(first, "Passed"):
		problems = append(problems, fmt.Sprintf("the verdict says %q, not Passed; a Failed verdict is `gf submit --failed`", first))
	}
	if ids := blockingFindings(text); len(ids) > 0 {
		problems = append(problems, "the verdict says Passed but ## Blocking lists "+strings.Join(ids, ", "))
	}
	for _, heading := range []string{RiskMapHeading, AgreedBehaviourHeading} {
		body, found := markdownSection(text, heading)
		switch {
		case !found:
			problems = append(problems, "no '## "+heading+"' section")
		case body == "":
			problems = append(problems, "the '## "+heading+"' section is empty")
		}
	}
	if len(problems) > 0 {
		return fail("%s: %s", artifact, strings.Join(problems, "; ")), nil
	}
	return pass, nil
}

// blockingFindings are the ids of the `### B<n>` headings under
// `## Blocking`, in order.
func blockingFindings(text string) []string {
	var ids []string
	in := false
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if level := headingLevel(line); level == 1 || level == 2 {
			in = level == 2 && strings.EqualFold(strings.TrimSpace(strings.TrimLeft(line, "#")), BlockingHeading)
			continue
		}
		if m := blockingFinding.FindStringSubmatch(line); in && m != nil {
			ids = append(ids, m[1])
		}
	}
	return ids
}
