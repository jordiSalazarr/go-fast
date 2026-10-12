package exitchecks

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// designFile is the structured design's JSON form. Its field names are the
// contract with the design agent.
type designFile struct {
	BoundedContext string `json:"boundedContext"`
	Aggregates     []struct {
		Name       string   `json:"name"`
		Invariants []string `json:"invariants"`
		States     []string `json:"states"`
	} `json:"aggregates"`
	Commands []struct {
		Name       string   `json:"name"`
		Aggregate  string   `json:"aggregate"`
		Events     []string `json:"events"`
		Rejections []string `json:"rejections"`
	} `json:"commands"`
	Events []struct {
		Name       string `json:"name"`
		Aggregate  string `json:"aggregate"`
		Visibility string `json:"visibility"`
	} `json:"events"`
	ReadModels []struct {
		Name string   `json:"name"`
		From []string `json:"from"`
	} `json:"readModels"`
	Slices    []sliceFile `json:"slices"`
	Contracts []struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Slice  string `json:"slice"`
		Schema string `json:"schema"`
	} `json:"contracts"`
}

// DesignError says why a structured design is not valid: it doesn't parse,
// or the domain finds problems in it. Problems lists each one.
type DesignError struct {
	Path     string // relative to the repository root
	Missing  bool
	Problems []string
}

func (e *DesignError) Error() string {
	if e.Missing {
		return e.Path + " is missing"
	}
	return e.Path + ": " + strings.Join(e.Problems, "; ")
}

// LoadDesign reads a structured design, by its path relative to root, and
// returns it only if it is valid; otherwise a *DesignError says why.
func LoadDesign(root string, path domain.RepoPath) (domain.Design, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path.String())))
	if errors.Is(err, os.ErrNotExist) {
		return domain.Design{}, &DesignError{Path: path.String(), Missing: true}
	}
	if err != nil {
		return domain.Design{}, fmt.Errorf("read design: %w", err)
	}
	design, problems := parseDesign(data)
	if len(problems) > 0 {
		return domain.Design{}, &DesignError{Path: path.String(), Problems: problems}
	}
	return design, nil
}

// parseDesign turns a design's JSON into a domain design, and lists what
// is wrong with it: the JSON's own problems first, then the domain's.
func parseDesign(data []byte) (domain.Design, []string) {
	var file designFile
	if problem := decodeStrict(data, &file); problem != "" {
		return domain.Design{}, []string{problem}
	}
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	v := domain.DesignValues{BoundedContext: file.BoundedContext}
	for i, a := range file.Aggregates {
		var invariants []domain.Invariant
		for j, text := range a.Invariants {
			inv, err := domain.NewInvariant(text)
			if err != nil {
				add("aggregates[%d].invariants[%d] is empty", i, j)
				continue
			}
			invariants = append(invariants, inv)
		}
		v.Aggregates = append(v.Aggregates, domain.NewAggregateDesign(a.Name, invariants, a.States))
	}
	for _, c := range file.Commands {
		v.Commands = append(v.Commands, domain.NewCommandDesign(c.Name, c.Aggregate, c.Events, c.Rejections))
	}
	for i, e := range file.Events {
		visibility, err := domain.ParseVisibility(e.Visibility)
		if err != nil {
			add(`events[%d] (%q): "visibility" must be "internal" or "external", not %q`, i, e.Name, e.Visibility)
			continue
		}
		event, err := domain.NewEventDesign(e.Name, e.Aggregate, visibility)
		if err != nil {
			return domain.Design{}, []string{err.Error()}
		}
		v.Events = append(v.Events, event)
	}
	for _, r := range file.ReadModels {
		v.ReadModels = append(v.ReadModels, domain.NewReadModelDesign(r.Name, r.From))
	}
	var sliceProblems []string
	v.Slices, sliceProblems = parseSlices(file.Slices)
	problems = append(problems, sliceProblems...)
	for i, c := range file.Contracts {
		kind, err := domain.ParseContractKind(c.Kind)
		if err != nil {
			add(`contracts[%d] (%q): "kind" must be "http" or "event", not %q`, i, c.Name, c.Kind)
			continue
		}
		contract, err := domain.NewContractDesign(kind, c.Name, c.Slice, c.Schema)
		if err != nil {
			return domain.Design{}, []string{err.Error()}
		}
		v.Contracts = append(v.Contracts, contract)
	}
	if len(problems) > 0 {
		return domain.Design{}, problems
	}
	design := domain.NewDesign(v)
	return design, design.Problems()
}

func orEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (c *checkRun) designValid() (finding, error) {
	_, err := LoadDesign(c.Root, domain.DesignFor(c.visit.Work, c.visit.Visit))
	var invalid *DesignError
	switch {
	case errors.As(err, &invalid) && invalid.Missing:
		return fail("%s is missing", invalid.Path), nil
	case errors.As(err, &invalid):
		// The summary may be cut short; the log has every problem.
		for _, p := range invalid.Problems {
			fmt.Fprintf(c.log, "- %s\n", p)
		}
		return c.ran(fail("%s has %s: %s", invalid.Path, plural(len(invalid.Problems), "problem"), strings.Join(invalid.Problems, "; "))), nil
	case err != nil:
		return finding{}, err
	}
	return pass, nil
}

func (c *checkRun) nfrProposalsValid() (finding, error) {
	path := domain.NFRProposalsFor(c.visit.Work, c.visit.Visit).String()
	_, _, err := LoadNFRsFrom(c.Root, path)
	var invalid *NFRsError
	if errors.As(err, &invalid) {
		return fail("%s: %s", path, invalid.Problem), nil
	}
	if err != nil {
		return finding{}, err
	}
	return pass, nil
}

// Sections of modify-behaviour's discovery artifact.
const (
	CurrentBehaviourHeading  = "Current behaviour"
	ExpectedBehaviourHeading = "Expected behaviour"
)

func (c *checkRun) behaviourDelta() (finding, error) {
	artifact := domain.ArtifactFor(c.visit.Work, c.visit.Stage, c.visit.Visit)
	content, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(artifact.String())))
	if errors.Is(err, os.ErrNotExist) {
		return fail("%s is missing", artifact), nil
	}
	if err != nil {
		return finding{}, err
	}
	var problems []string
	for _, heading := range []string{CurrentBehaviourHeading, ExpectedBehaviourHeading} {
		body, found := markdownSection(string(content), heading)
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

// markdownSection returns the text of the level-2 section with this
// heading (case-insensitive), up to the next heading of level 1 or 2,
// without its blank lines and subheadings; found is false when there is no
// such section.
func markdownSection(text, heading string) (body string, found bool) {
	var lines []string
	in := false
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if level := headingLevel(line); level == 1 || level == 2 {
			if in {
				break
			}
			in = level == 2 && strings.EqualFold(strings.TrimSpace(strings.TrimLeft(line, "#")), heading)
			found = found || in
			continue
		}
		if in && line != "" && headingLevel(line) == 0 {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), found
}

// headingLevel is the level of an ATX heading line, 0 for any other line.
func headingLevel(line string) int {
	level := len(line) - len(strings.TrimLeft(line, "#"))
	if level == 0 || level > 6 || (len(line) > level && line[level] != ' ') {
		return 0
	}
	return level
}

// acceptedDesign is the latest accepted design, as it was when design was
// accepted: later stages can write in the artifact directory too.
func (c *checkRun) acceptedDesign() (domain.Design, *finding, error) {
	noDesign := func(format string, args ...any) (domain.Design, *finding, error) {
		f := fail(format, args...)
		return domain.Design{}, &f, nil
	}
	if c.visit.Design == (domain.Visit{}) {
		return noDesign("the work has no accepted design")
	}
	path := domain.DesignFor(c.visit.Work, c.visit.Design)
	data, exists, problem, err := c.acceptedFile(domain.StageDesign, path, c.visit.DesignEnd)
	if err != nil || problem != nil {
		return domain.Design{}, problem, err
	}
	if !exists {
		return noDesign("the accepted design %s is missing", path)
	}
	design, problems := parseDesign(data)
	if len(problems) > 0 {
		return noDesign("the accepted design %s is invalid: %s", path, strings.Join(problems, "; "))
	}
	return design, nil, nil
}

func (c *checkRun) specsCoverSlices() (finding, error) {
	design, problem, err := c.acceptedDesign()
	if err != nil || problem != nil {
		return deref(problem), err
	}
	_, tests, problem, err := c.specsWritten()
	if err != nil || problem != nil {
		return deref(problem), err
	}
	uncovered := design.UncoveredSlices(tests)
	if len(uncovered) == 0 {
		return pass, nil
	}
	names := make([]string, len(uncovered))
	for i, s := range uncovered {
		names[i] = s.Name() + " (Test" + s.Name() + "…)"
	}
	return fail("no spec written in this stage covers %s: %s", plural(len(uncovered), "slice"), strings.Join(names, ", ")), nil
}
