package exitchecks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// sliceFile is a slice's JSON form, in an event model or a design.
type sliceFile struct {
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Command   *string `json:"command"`
	ReadModel *string `json:"readModel"`
	On        *string `json:"on"`
	Publishes *string `json:"publishes"`
	Change    string  `json:"change"`
}

// eventModelFile is the canonical event model's JSON form. Its field names
// are the contract with the event-modelling agent.
type eventModelFile struct {
	Events []struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	} `json:"events"`
	Commands []struct {
		Name       string   `json:"name"`
		Events     []string `json:"events"`
		Rejections []string `json:"rejections"`
	} `json:"commands"`
	ReadModels []struct {
		Name string   `json:"name"`
		From []string `json:"from"`
	} `json:"readModels"`
	Slices    []sliceFile `json:"slices"`
	Scenarios []struct {
		ID          string   `json:"id"`
		Slice       string   `json:"slice"`
		Given       []string `json:"given"`
		When        string   `json:"when"`
		Description string   `json:"description"`
		Then        struct {
			Events    []string `json:"events"`
			Rejection string   `json:"rejection"`
			ReadModel string   `json:"readModel"`
			State     string   `json:"state"`
		} `json:"then"`
	} `json:"scenarios"`
	ExistingEvents []string `json:"existingEvents"`
}

// EventModelError says why a canonical event model is not valid: it doesn't
// parse, or the domain finds problems in it. Problems lists each one.
type EventModelError struct {
	Path     string // relative to the repository root
	Missing  bool
	Problems []string
}

func (e *EventModelError) Error() string {
	if e.Missing {
		return e.Path + " is missing"
	}
	return e.Path + ": " + strings.Join(e.Problems, "; ")
}

// LoadEventModel reads a canonical event model, by its path relative to
// root, and returns it only if it is valid; otherwise an *EventModelError
// says why.
func LoadEventModel(root string, path domain.RepoPath) (domain.EventModel, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path.String())))
	if errors.Is(err, os.ErrNotExist) {
		return domain.EventModel{}, &EventModelError{Path: path.String(), Missing: true}
	}
	if err != nil {
		return domain.EventModel{}, fmt.Errorf("read event model: %w", err)
	}
	model, problems := parseEventModel(data)
	if len(problems) > 0 {
		return domain.EventModel{}, &EventModelError{Path: path.String(), Problems: problems}
	}
	return model, nil
}

// parseEventModel turns an event model's JSON into a domain event model,
// and lists what is wrong with it: the JSON's own problems first, then the
// domain's.
func parseEventModel(data []byte) (domain.EventModel, []string) {
	var file eventModelFile
	if problem := decodeStrict(data, &file); problem != "" {
		return domain.EventModel{}, []string{problem}
	}
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	v := domain.EventModelValues{ExistingEvents: file.ExistingEvents}
	for i, e := range file.Events {
		visibility, err := domain.ParseVisibility(e.Visibility)
		if err != nil {
			add(`events[%d] (%q): "visibility" must be "internal" or "external", not %q`, i, e.Name, e.Visibility)
			continue
		}
		event, err := domain.NewModelEvent(e.Name, visibility)
		if err != nil {
			return domain.EventModel{}, []string{err.Error()}
		}
		v.Events = append(v.Events, event)
	}
	for _, c := range file.Commands {
		v.Commands = append(v.Commands, domain.NewModelCommand(c.Name, c.Events, c.Rejections))
	}
	for _, r := range file.ReadModels {
		v.ReadModels = append(v.ReadModels, domain.NewReadModelDesign(r.Name, r.From))
	}
	var sliceProblems []string
	v.Slices, sliceProblems = parseSlices(file.Slices)
	problems = append(problems, sliceProblems...)
	for _, s := range file.Scenarios {
		v.Scenarios = append(v.Scenarios, domain.NewScenario(domain.ScenarioValues{
			ID: s.ID, Slice: s.Slice, Given: s.Given, When: s.When, Description: s.Description,
			Then: domain.ScenarioThen{Events: s.Then.Events, Rejection: s.Then.Rejection, ReadModel: s.Then.ReadModel, State: s.Then.State},
		}))
	}
	if len(problems) > 0 {
		return domain.EventModel{}, problems
	}
	model := domain.NewEventModel(v)
	return model, model.Problems()
}

// parseSlices turns slices' JSON into domain slices, and lists the kinds
// and changes gf doesn't know.
func parseSlices(files []sliceFile) ([]domain.SliceDesign, []string) {
	var slices []domain.SliceDesign
	var problems []string
	for i, s := range files {
		kind, err := domain.ParseSliceKind(s.Kind)
		if err != nil {
			problems = append(problems, fmt.Sprintf(`slices[%d] (%q): "kind" must be "command", "query" or "automation", not %q`, i, s.Name, s.Kind))
		}
		change, err2 := domain.ParseChange(s.Change)
		if err2 != nil {
			problems = append(problems, fmt.Sprintf(`slices[%d] (%q): "change" must be "added", "changed", "removed" or "unchanged", not %q`, i, s.Name, s.Change))
		}
		if err != nil || err2 != nil {
			continue
		}
		slice, err := domain.NewSliceDesign(domain.SliceValues{
			Name: s.Name, Kind: kind, Command: orEmpty(s.Command), ReadModel: orEmpty(s.ReadModel),
			On: orEmpty(s.On), Publishes: orEmpty(s.Publishes), Change: change,
		})
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		slices = append(slices, slice)
	}
	return slices, problems
}

// decodeStrict decodes one JSON value, refusing unknown fields; it returns
// what is wrong, or "".
func decodeStrict(data []byte, v any) string {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return "not valid JSON for gf: " + err.Error()
	}
	if dec.More() {
		return "not valid JSON for gf: more than one JSON value"
	}
	return ""
}

// AssumptionsHeading is the section of event modelling's markdown that
// lists what it had to assume, one list item each: "- M1: <assumption>".
const AssumptionsHeading = "Assumptions"

var assumptionItem = regexp.MustCompile(`^[-*]\s+(?:\*\*)?(M[0-9]+)(?:\*\*)?\s*[:.]\s*(.+)$`)

// ParseAssumptions reads the assumptions of event modelling's markdown, and
// lists what is wrong with them: list items without an "M<n>:" id, and
// repeated ids. No section, or one without list items, is no assumption.
func ParseAssumptions(markdown string) ([]domain.Assumption, []string) {
	section, _ := markdownBlock(markdown, AssumptionsHeading)
	var assumptions []domain.Assumption
	var problems []string
	scanner := bufio.NewScanner(strings.NewReader(section))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		m := assumptionItem.FindStringSubmatch(line)
		if m == nil {
			problems = append(problems, fmt.Sprintf("the assumption %q has no id: start it with M1:, M2:, …", strings.TrimSpace(line[2:])))
			continue
		}
		a, err := domain.NewAssumption(m[1], m[2])
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		assumptions = append(assumptions, a)
	}
	return assumptions, append(problems, domain.AssumptionProblems(assumptions)...)
}

// markdownBlock returns the lines of the level-2 section with this heading
// (case-insensitive), up to the next heading of level 1 or 2, as written.
func markdownBlock(text, heading string) (body string, found bool) {
	var lines []string
	in := false
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if level := headingLevel(trimmed); level == 1 || level == 2 {
			if in {
				break
			}
			in = level == 2 && strings.EqualFold(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), heading)
			found = found || in
			continue
		}
		if in {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), found
}

func (c *checkRun) eventModelValid() (finding, error) {
	path := domain.EventModelFor(c.visit.Work, c.visit.Visit)
	_, err := LoadEventModel(c.Root, path)
	var invalid *EventModelError
	var problems []string
	switch {
	case errors.As(err, &invalid) && invalid.Missing:
		return fail("%s is missing", invalid.Path), nil
	case errors.As(err, &invalid):
		for _, p := range invalid.Problems {
			problems = append(problems, path.String()+": "+p)
		}
	case err != nil:
		return finding{}, err
	}
	artifact := domain.ArtifactFor(c.visit.Work, c.visit.Stage, c.visit.Visit)
	markdown, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(artifact.String())))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return finding{}, err
	}
	_, assumptionProblems := ParseAssumptions(string(markdown))
	for _, p := range assumptionProblems {
		problems = append(problems, artifact.String()+" ## "+AssumptionsHeading+": "+p)
	}
	if len(problems) == 0 {
		return pass, nil
	}
	// The summary may be cut short; the log has every problem.
	for _, p := range problems {
		fmt.Fprintf(c.log, "- %s\n", p)
	}
	return c.ran(fail("%s: %s", plural(len(problems), "problem"), strings.Join(problems, "; "))), nil
}

// acceptedEventModel is the work's latest accepted canonical event model,
// as it was when event modelling was accepted; ok is false when the work
// has none.
func (c *checkRun) acceptedEventModel() (model domain.EventModel, ok bool, problem *finding, err error) {
	if c.visit.EventModel == (domain.Visit{}) {
		return domain.EventModel{}, false, nil, nil
	}
	data, exists, problem, err := c.acceptedFile(domain.StageEventModelling, domain.EventModelFor(c.visit.Work, c.visit.EventModel), c.visit.EventModelEnd)
	if err != nil || problem != nil {
		return domain.EventModel{}, false, problem, err
	}
	path := domain.EventModelFor(c.visit.Work, c.visit.EventModel)
	if !exists {
		f := fail("the accepted event model %s is missing", path)
		return domain.EventModel{}, false, &f, nil
	}
	model, problems := parseEventModel(data)
	if len(problems) > 0 {
		f := fail("the accepted event model %s is invalid: %s", path, strings.Join(problems, "; "))
		return domain.EventModel{}, false, &f, nil
	}
	return model, true, nil, nil
}

// acceptedFile reads a file as it was when a stage's visit was accepted:
// at the baseline of the visit entered then (end), or at this visit's own
// baseline while that is this visit. Later stages can write in the artifact
// directory too, so the working tree is not read.
func (c *checkRun) acceptedFile(stage domain.Stage, path domain.RepoPath, end domain.AssignmentID) (data []byte, exists bool, problem *finding, err error) {
	tree, ok, err := c.ownBaseline()
	if err != nil {
		return nil, false, nil, err
	}
	if end != (domain.AssignmentID{}) && end != c.visit.Assignment {
		if tree, ok, err = c.baselines.Tree(end); err != nil {
			return nil, false, nil, err
		}
	}
	if !ok {
		f := c.missingBaseline(stage)
		return nil, false, &f, nil
	}
	data, exists, err = worktree.FileAt(c.Root, tree, path)
	return data, exists, nil, err
}
