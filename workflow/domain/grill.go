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
	// MustNotChange is what a refactor preserves: public APIs, behaviour,
	// event names, wire formats.
	MustNotChange []string
	OpenQuestions []string
	// DocPath is the work's decision document.
	DocPath RepoPath
}

// Grill is the structured outcome of a grill visit. Any value can be
// built; Problems says whether it is complete.
type Grill struct{ v GrillValues }

func NewGrill(v GrillValues) Grill {
	g := GrillValues{DocPath: v.DocPath, OpenQuestions: trimAll(v.OpenQuestions), MustNotChange: trimAll(v.MustNotChange)}
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
func (g Grill) DocPath() RepoPath       { return g.v.DocPath }
func (g Grill) MustNotChange() []string { return slices.Clone(g.v.MustNotChange) }

// GrillRequires is what grill's outcome must hold on work of a type:
// acceptance scenarios on feature work without an event model, which
// specify covers; on a refactor, which changes no behaviour, none, but
// what must not change.
func GrillRequires(workType WorkType, hasEventModel bool) (acceptanceScenarios, mustNotChange bool) {
	if workType == WorkTypeRefactor {
		return false, true
	}
	return !hasEventModel, false
}

var (
	decisionID   = regexp.MustCompile(`^D[0-9]+$`)
	acceptanceID = regexp.MustCompile(`^A[0-9]+$`)
)

// GrillFacts are what grill's outcome is checked against: the work and its
// type, the repository's decisions directory, and, when the work has an
// event model, the assumptions event modelling made.
type GrillFacts struct {
	Work          WorkID
	WorkType      WorkType
	Decisions     DecisionsDir
	HasEventModel bool
	Assumptions   []Assumption
}

// Problems lists what makes grill's outcome incomplete, one sentence each,
// in a stable order; none means grill is done: no question is open, every
// decision has a unique id, a question and an answer, the decision document
// is the work's, every assumption of the event model is settled by a
// decision that addresses it by id, and what the work type requires
// (GrillRequires): acceptance scenarios for the agreed behaviour, or what
// must not change, never empty and never just "None".
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
	}
	acceptance, mustNotChange := GrillRequires(f.WorkType, f.HasEventModel)
	if acceptance && len(v.AcceptanceScenarios) == 0 {
		p.add("without an event model, grill needs acceptance scenarios (A1, A2, …)")
	}
	if mustNotChange {
		switch {
		case len(v.MustNotChange) == 0:
			p.add("a refactor's grill needs mustNotChange: what the refactor preserves (public APIs, behaviour, event names, wire formats)")
		case len(v.MustNotChange) == 1 && isNone(v.MustNotChange[0]):
			p.add("mustNotChange says None: every refactor preserves something, so name it")
		}
		for i, item := range v.MustNotChange {
			if item == "" {
				p.add("mustNotChange item %d is empty", i+1)
			}
		}
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

// DocumentEntry is an entry of the decision document the owner reads: its
// id and its text, up to the next entry.
type DocumentEntry struct {
	ID   string
	Text string
}

// DecisionDocument is what the owner approves: the entries of its
// `## Decisions` and `## Acceptance scenarios` sections, and the items of
// its `## Must not change` list.
type DecisionDocument struct {
	Decisions           []DocumentEntry
	AcceptanceScenarios []DocumentEntry
	MustNotChange       []string
}

// ListItems are the top-level items of a markdown list: lines starting with
// "- ", "* " or "1. ", each joined with its indented continuation lines.
func ListItems(section string) []string {
	var items []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if item, ok := listItem(line); ok {
			items = append(items, item)
			continue
		}
		if len(items) > 0 && line != strings.TrimLeft(line, " \t") {
			items[len(items)-1] += " " + trimmed
		}
	}
	return items
}

var numberedItem = regexp.MustCompile(`^[0-9]+[.)]\s+`)

func listItem(line string) (string, bool) {
	for _, bullet := range []string{"- ", "* ", "+ "} {
		if rest, ok := strings.CutPrefix(line, bullet); ok {
			return strings.TrimSpace(rest), true
		}
	}
	if m := numberedItem.FindString(line); m != "" {
		return strings.TrimSpace(line[len(m):]), true
	}
	return "", false
}

// isNone reports whether an item only says "None".
func isNone(item string) bool {
	return strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(item), "."), "none")
}

// sameItem compares a document's list item with the structured file's,
// ignoring markdown emphasis and code marks and spacing.
func sameItem(doc, file string) bool {
	norm := func(s string) string {
		return strings.Join(strings.Fields(strings.NewReplacer("`", "", "*", "", "_", "").Replace(s)), " ")
	}
	return norm(doc) == norm(file)
}

var (
	bulletEntry  = regexp.MustCompile(`^-\s+([A-Z][0-9]+)\s*:`)
	headingEntry = regexp.MustCompile(`^###\s+([A-Z][0-9]+)(\s*:|\s+—|\s*$)`)
)

// ParseDecisionDocument reads the entries of the document's sections, given
// their text. An entry opens with a line "- D1: …" or "### D1" (optionally
// followed by ":" or " —"), with D for decisions and A for acceptance
// scenarios, and runs to the next entry.
func ParseDecisionDocument(decisions, acceptance string) DecisionDocument {
	return DecisionDocument{Decisions: entries(decisions, 'D'), AcceptanceScenarios: entries(acceptance, 'A')}
}

func entries(section string, prefix byte) []DocumentEntry {
	var out []DocumentEntry
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		m := bulletEntry.FindStringSubmatch(trimmed)
		if m == nil {
			m = headingEntry.FindStringSubmatch(trimmed)
		}
		if m != nil && m[1][0] == prefix {
			out = append(out, DocumentEntry{ID: m[1], Text: trimmed})
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].Text += "\n" + trimmed
		}
	}
	return out
}

// DocumentProblems lists where the decision document the owner approves
// and grill's structured outcome, file, disagree: a decision or acceptance
// scenario in one but not the other, must-not-change lists that differ in
// length or in an item, in order, and, with an event model, an
// assumption a decision addresses that its entry in the document doesn't
// name.
func (g Grill) DocumentProblems(doc DecisionDocument, file string, hasEventModel bool) []string {
	var p problems
	ids := func(entries []DocumentEntry) []string {
		return names(entries, func(e DocumentEntry) string { return e.ID })
	}
	decisionIDs := names(g.v.Decisions, func(d GrillDecision) string { return d.ID })
	p.sameIDs("decision", decisionIDs, ids(doc.Decisions), file)
	p.sameIDs("acceptance scenario", g.AcceptanceIDs(), ids(doc.AcceptanceScenarios), file)
	if want, got := g.v.MustNotChange, doc.MustNotChange; len(want) != len(got) {
		p.add("the '## Must not change' list has %d items, but mustNotChange in %s has %d", len(got), file, len(want))
	} else {
		for i := range want {
			if !sameItem(got[i], want[i]) {
				p.add("must-not-change item %d differs: the decision document says %q, %s says %q", i+1, got[i], file, want[i])
			}
		}
	}
	if !hasEventModel {
		return p.list
	}
	for _, d := range g.v.Decisions {
		i := slices.IndexFunc(doc.Decisions, func(e DocumentEntry) bool { return e.ID == d.ID })
		if i < 0 {
			continue
		}
		for _, a := range d.Addresses {
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(a) + `\b`).MatchString(doc.Decisions[i].Text) {
				p.add("decision %s addresses %s, but its entry in the decision document doesn't name %s", d.ID, a, a)
			}
		}
	}
	return p.list
}

// sameIDs reports ids in the structured file but not in the document, then
// ids in the document but not in the file.
func (p *problems) sameIDs(kind string, inFile, inDoc []string, file string) {
	for _, id := range inFile {
		if !slices.Contains(inDoc, id) {
			p.add("%s %s is in %s but not in the decision document", kind, id, file)
		}
	}
	for _, id := range inDoc {
		if !slices.Contains(inFile, id) {
			p.add("%s %s is in the decision document but not in %s", kind, id, file)
		}
	}
}
