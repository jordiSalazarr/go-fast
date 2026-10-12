package exitchecks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// grillFile is grill's structured outcome in JSON. Its field names are the
// contract with the grill skill.
type grillFile struct {
	Decisions []struct {
		ID        string   `json:"id"`
		Question  string   `json:"question"`
		Answer    string   `json:"answer"`
		Rationale string   `json:"rationale"`
		Addresses []string `json:"addresses"`
	} `json:"decisions"`
	AcceptanceScenarios []struct {
		ID    string `json:"id"`
		Given string `json:"given"`
		When  string `json:"when"`
		Then  string `json:"then"`
	} `json:"acceptanceScenarios"`
	OpenQuestions []string `json:"openQuestions"`
	DocPath       string   `json:"docPath"`
}

// Sections of grill's decision document.
const (
	IntentHeading              = "Intent"
	ScopeHeading               = "Scope"
	DecisionsHeading           = "Decisions"
	GlossaryHeading            = "Glossary"
	AcceptanceScenariosHeading = "Acceptance scenarios"
	OpenQuestionsHeading       = "Open questions"
)

// GrillError says why grill's structured outcome can't be read.
type GrillError struct {
	Path    string // relative to the repository root
	Missing bool
	Problem string
}

func (e *GrillError) Error() string {
	if e.Missing {
		return e.Path + " is missing"
	}
	return e.Path + ": " + e.Problem
}

// LoadGrill reads a grill visit's structured outcome, by its path relative
// to root; a *GrillError says why it can't. Whether it is complete is
// Grill.Problems' to say.
func LoadGrill(root string, path domain.RepoPath) (domain.Grill, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path.String())))
	if errors.Is(err, os.ErrNotExist) {
		return domain.Grill{}, &GrillError{Path: path.String(), Missing: true}
	}
	if err != nil {
		return domain.Grill{}, fmt.Errorf("read grill: %w", err)
	}
	grill, problem := parseGrill(data)
	if problem != "" {
		return domain.Grill{}, &GrillError{Path: path.String(), Problem: problem}
	}
	return grill, nil
}

func parseGrill(data []byte) (domain.Grill, string) {
	var file grillFile
	if problem := decodeStrict(data, &file); problem != "" {
		return domain.Grill{}, problem
	}
	v := domain.GrillValues{OpenQuestions: file.OpenQuestions}
	if file.DocPath != "" {
		doc, err := domain.ParseRepoPath(file.DocPath)
		if err != nil {
			return domain.Grill{}, fmt.Sprintf("docPath %q is not a clean path relative to the repository root", file.DocPath)
		}
		v.DocPath = doc
	}
	for _, d := range file.Decisions {
		v.Decisions = append(v.Decisions, domain.GrillDecision{ID: d.ID, Question: d.Question, Answer: d.Answer, Rationale: d.Rationale, Addresses: d.Addresses})
	}
	for _, a := range file.AcceptanceScenarios {
		v.AcceptanceScenarios = append(v.AcceptanceScenarios, domain.AcceptanceScenario{ID: a.ID, Given: a.Given, When: a.When, Then: a.Then})
	}
	return domain.NewGrill(v), ""
}

func (c *checkRun) grillValid() (finding, error) {
	path := domain.GrillFor(c.visit.Work, c.visit.Visit)
	grill, err := LoadGrill(c.Root, path)
	var invalid *GrillError
	switch {
	case errors.As(err, &invalid) && invalid.Missing:
		return fail("%s is missing", invalid.Path), nil
	case errors.As(err, &invalid):
		return fail("%s", invalid.Error()), nil
	case err != nil:
		return finding{}, err
	}
	facts := domain.GrillFacts{Work: c.visit.Work, Decisions: c.Config.Decisions, HasEventModel: c.visit.HasEventModel}
	if facts.HasEventModel {
		assumptions, problem, err := c.acceptedAssumptions()
		if err != nil || problem != nil {
			return deref(problem), err
		}
		facts.Assumptions = assumptions
	}
	var problems []string
	for _, p := range grill.Problems(facts) {
		problems = append(problems, path.String()+": "+p)
	}
	if doc := grill.DocPath(); facts.Decisions.IsDecisionOf(doc, c.visit.Work) {
		docProblems, err := c.decisionDocProblems(doc, facts.HasEventModel)
		if err != nil {
			return finding{}, err
		}
		problems = append(problems, docProblems...)
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

// decisionDocProblems checks that the decision document exists and has its
// sections: intent, scope, decisions, glossary, acceptance scenarios
// (without an event model), and open questions saying "None".
func (c *checkRun) decisionDocProblems(doc domain.RepoPath, hasEventModel bool) ([]string, error) {
	content, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(doc.String())))
	if errors.Is(err, os.ErrNotExist) {
		return []string{doc.String() + " is missing"}, nil
	}
	if err != nil {
		return nil, err
	}
	var problems []string
	required := []string{IntentHeading, ScopeHeading, DecisionsHeading}
	if !hasEventModel {
		required = append(required, AcceptanceScenariosHeading)
	}
	for _, heading := range required {
		body, found := markdownSection(string(content), heading)
		switch {
		case !found:
			problems = append(problems, doc.String()+": no '## "+heading+"' section")
		case body == "":
			problems = append(problems, doc.String()+": the '## "+heading+"' section is empty")
		}
	}
	if _, found := markdownSection(string(content), GlossaryHeading); !found {
		problems = append(problems, doc.String()+": no '## "+GlossaryHeading+"' section")
	}
	body, found := markdownSection(string(content), OpenQuestionsHeading)
	switch {
	case !found:
		problems = append(problems, doc.String()+": no '## "+OpenQuestionsHeading+"' section")
	case !strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(body), "."), "none"):
		problems = append(problems, doc.String()+": the '## "+OpenQuestionsHeading+"' section must say None")
	}
	return problems, nil
}

// acceptedAssumptions are the assumptions of the work's latest accepted
// event model, as its markdown said when it was accepted.
func (c *checkRun) acceptedAssumptions() ([]domain.Assumption, *finding, error) {
	if c.visit.EventModel == (domain.Visit{}) {
		f := fail("the work has an event model but no accepted event-modelling visit")
		return nil, &f, nil
	}
	artifact := domain.ArtifactFor(c.visit.Work, domain.StageEventModelling, c.visit.EventModel)
	data, exists, problem, err := c.acceptedFile(domain.StageEventModelling, artifact, c.visit.EventModelEnd)
	if err != nil || problem != nil {
		return nil, problem, err
	}
	if !exists {
		f := fail("the accepted event model's markdown %s is missing", artifact)
		return nil, &f, nil
	}
	assumptions, _ := ParseAssumptions(string(data))
	return assumptions, nil, nil
}
