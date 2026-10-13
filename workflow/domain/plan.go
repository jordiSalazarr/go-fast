package domain

import (
	"path"
	"slices"
	"strings"
)

// RefactorMove is one move a refactor plan describes: code from one place
// to another. It is descriptive only.
type RefactorMove struct {
	From string
	To   string
}

// RefactorPlanValues are plan's structured output.
type RefactorPlanValues struct {
	// Areas are globs of the code the refactor may change.
	Areas []string
	Moves []RefactorMove
	// BehaviourChange must be false: a change of behaviour is
	// modify-behaviour's work.
	BehaviourChange bool
}

// RefactorPlan is the structured outcome of a plan visit. Any value can be
// built; Problems says whether it is complete.
type RefactorPlan struct {
	v     RefactorPlanValues
	areas []AreaGlob // the valid areas
}

func NewRefactorPlan(v RefactorPlanValues) RefactorPlan {
	p := RefactorPlan{v: RefactorPlanValues{Areas: trimAll(v.Areas), BehaviourChange: v.BehaviourChange}}
	for _, m := range v.Moves {
		p.v.Moves = append(p.v.Moves, RefactorMove{From: strings.TrimSpace(m.From), To: strings.TrimSpace(m.To)})
	}
	for _, a := range p.v.Areas {
		if g, err := NewAreaGlob(a); err == nil {
			p.areas = append(p.areas, g)
		}
	}
	return p
}

// Areas are the plan's valid areas: the code the refactor may change.
func (p RefactorPlan) Areas() []AreaGlob     { return slices.Clone(p.areas) }
func (p RefactorPlan) Moves() []RefactorMove { return slices.Clone(p.v.Moves) }

// Problems lists what makes the plan incomplete, one sentence each, in a
// stable order; none means it is valid: it names at least one area, every
// area is a valid glob of code (not the whole repository, gofast's own
// files or the decisions directory, decisions), it changes no behaviour,
// and the markdown plan links the accepted grill's decision document,
// grillDoc.
func (p RefactorPlan) Problems(markdown string, grillDoc RepoPath, decisions DecisionsDir) []string {
	var list problems
	if len(p.v.Areas) == 0 {
		list.add("the plan names no area: list the code the refactor may change as globs under \"areas\"")
	}
	for _, a := range p.v.Areas {
		clean := path.Clean(a)
		switch {
		case wholeRepository(a):
			list.add("area %q covers the whole repository: name the code the refactor moves (e.g. internal/wallet/**)", a)
		case within(clean, gofastDir):
			list.add("area %q is gofast's own files, not code: name the code the refactor moves (e.g. internal/wallet/**)", a)
		case within(clean, decisions.path):
			list.add("area %q is the decisions directory, not code: name the code the refactor moves (e.g. internal/wallet/**)", a)
		default:
			if _, err := NewAreaGlob(a); err != nil {
				list.add("area %q is not a glob of repository paths, such as internal/wallet/**", a)
			}
		}
	}
	if p.v.BehaviourChange {
		list.add("behaviourChange is true: a refactor changes no behaviour; a change of behaviour is modify-behaviour work")
	}
	switch {
	case grillDoc.isZero():
		list.add("the work has no accepted grill whose decision document the plan could link")
	case !strings.Contains(markdown, grillDoc.value):
		list.add("the markdown plan doesn't link the accepted grill's decision document, %s", grillDoc)
	}
	return list.list
}

// wholeRepository reports whether an area matches every path: after
// cleaning, it is one of **, **/*, *, ., ./... or /.
func wholeRepository(area string) bool {
	for _, form := range []string{area, path.Clean(area)} {
		switch form {
		case "**", "**/*", "*", ".", "./...", "/":
			return true
		}
	}
	return false
}

// within reports whether a cleaned area starts with dir.
func within(area string, dir RepoPath) bool {
	return !dir.isZero() && (area == dir.value || strings.HasPrefix(area, dir.value+"/"))
}
