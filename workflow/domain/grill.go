package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// DecisionsDir is the repository directory that holds grill's decision
// documents, one per work, and the index gf generates of them. The owner
// configures it; it is docs/decisions by default.
type DecisionsDir struct{ path RepoPath }

// DefaultDecisionsDir is docs/decisions.
func DefaultDecisionsDir() DecisionsDir { return DecisionsDir{path: RepoPath{value: "docs/decisions"}} }

// NewDecisionsDir refuses a directory inside gofast's own files.
func NewDecisionsDir(path RepoPath) (DecisionsDir, error) {
	if path.isZero() || path == gofastDir || path.within(gofastDir) {
		return DecisionsDir{}, fmt.Errorf("decisions directory %q: %w", path, ErrInvalidRepoPath)
	}
	return DecisionsDir{path: path}, nil
}

func (d DecisionsDir) Path() RepoPath { return d.path }
func (d DecisionsDir) isZero() bool   { return d.path.isZero() }

// Index is the README.md gf generates in the directory. Nobody else writes
// it.
func (d DecisionsDir) Index() RepoPath { return RepoPath{value: d.path.value + "/README.md"} }

var decisionDocName = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})-(.+)\.md$`)

// IsDecisionOf reports whether path is a decision document of work: directly
// in the directory, named <YYYY-MM-DD>-<work id>-<slug>.md.
func (d DecisionsDir) IsDecisionOf(path RepoPath, work WorkID) bool {
	i := strings.LastIndex(path.value, "/")
	if i < 0 || path.value[:i] != d.path.value || work.isZero() {
		return false
	}
	m := decisionDocName.FindStringSubmatch(path.value[i+1:])
	if m == nil {
		return false
	}
	slug, ok := strings.CutPrefix(m[2], work.value+"-")
	return ok && slugPattern.MatchString(slug)
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// GrillDecision is one question grill settled with the owner: what was
// asked, what the owner answered, and why. Addresses are the ids of the
// event model's assumptions it settles.
type GrillDecision struct {
	ID        string
	Question  string
	Answer    string
	Rationale string
	Addresses []string
}

// AcceptanceScenario is grill's example of the agreed behaviour, in prose,
// when the work has no event model: specify covers each with a spec.
type AcceptanceScenario struct {
	ID    string
	Given string
	When  string
	Then  string
}

// GrillValues are grill's structured output.
type GrillValues struct {
	Decisions           []GrillDecision
	AcceptanceScenarios []AcceptanceScenario
	OpenQuestions       []string
	// DocPath is the work's decision document.
	DocPath RepoPath
}

// Grill is the structured outcome of a grill visit. Any value can be
// built; Problems says whether it is complete.
type Grill struct{ v GrillValues }

func NewGrill(v GrillValues) Grill {
	g := GrillValues{DocPath: v.DocPath, OpenQuestions: trimAll(v.OpenQuestions)}
	for _, d := range v.Decisions {
		g.Decisions = append(g.Decisions, GrillDecision{ID: strings.TrimSpace(d.ID), Question: strings.TrimSpace(d.Question),
			Answer: strings.TrimSpace(d.Answer), Rationale: strings.TrimSpace(d.Rationale), Addresses: trimAll(d.Addresses)})
	}
	for _, a := range v.AcceptanceScenarios {
		g.AcceptanceScenarios = append(g.AcceptanceScenarios, AcceptanceScenario{ID: strings.TrimSpace(a.ID),
			Given: strings.TrimSpace(a.Given), When: strings.TrimSpace(a.When), Then: strings.TrimSpace(a.Then)})
	}
	return Grill{v: g}
}

func (g Grill) Decisions() []GrillDecision { return slices.Clone(g.v.Decisions) }
func (g Grill) AcceptanceScenarios() []AcceptanceScenario {
	return slices.Clone(g.v.AcceptanceScenarios)
}
func (g Grill) DocPath() RepoPath { return g.v.DocPath }

var (
	decisionID   = regexp.MustCompile(`^D[0-9]+$`)
	acceptanceID = regexp.MustCompile(`^A[0-9]+$`)
)

// GrillFacts are what grill's outcome is checked against: the work, the
// repository's decisions directory, and, when the work has an event model,
// the assumptions event modelling made.
type GrillFacts struct {
	Work          WorkID
	Decisions     DecisionsDir
	HasEventModel bool
	Assumptions   []Assumption
}

// Problems lists what makes grill's outcome incomplete, one sentence each,
// in a stable order; none means grill is done: no question is open, every
// decision has a unique id, a question and an answer, the decision document
// is the work's, every assumption of the event model is settled by a
// decision that addresses it by id, and, without an event model, the agreed
// behaviour has acceptance scenarios.
func (g Grill) Problems(f GrillFacts) []string {
	var p problems
	v := g.v
	if len(v.OpenQuestions) > 0 {
		p.add("%s still open: %s", plural(len(v.OpenQuestions), "question is", "questions are"), strings.Join(v.OpenQuestions, "; "))
	}
	if len(v.Decisions) == 0 {
		p.add("grill recorded no decision")
	}
	seen := map[string]bool{}
	for _, d := range v.Decisions {
		switch {
		case !decisionID.MatchString(d.ID):
			p.add("decision id %q is not of the form D1, D2, …", d.ID)
		case seen[d.ID]:
			p.add("decision id %s is used more than once", d.ID)
		}
		seen[d.ID] = true
		if d.Question == "" || d.Answer == "" {
			p.add("decision %s needs both a question and an answer", d.ID)
		}
	}
	switch {
	case v.DocPath.isZero():
		p.add("docPath is missing")
	case !f.Decisions.IsDecisionOf(v.DocPath, f.Work):
		p.add("docPath %s is not this work's decision document: %s/<YYYY-MM-DD>-%s-<slug>.md", v.DocPath, f.Decisions.path, f.Work)
	}
	if f.HasEventModel {
		for _, a := range f.Assumptions {
			if !slices.ContainsFunc(v.Decisions, func(d GrillDecision) bool { return slices.Contains(d.Addresses, a.id) }) {
				p.add("assumption %s of the event model is addressed by no decision (%s)", a.id, a.text)
			}
		}
	} else if len(v.AcceptanceScenarios) == 0 {
		p.add("without an event model, grill needs acceptance scenarios (A1, A2, …)")
	}
	seen = map[string]bool{}
	for _, a := range v.AcceptanceScenarios {
		switch {
		case !acceptanceID.MatchString(a.ID):
			p.add("acceptance scenario id %q is not of the form A1, A2, …", a.ID)
		case seen[a.ID]:
			p.add("acceptance scenario id %s is used more than once", a.ID)
		}
		seen[a.ID] = true
		if a.Given == "" || a.When == "" || a.Then == "" {
			p.add("acceptance scenario %s needs a given, a when and a then", a.ID)
		}
	}
	return p.list
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
