package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// A design is the structured half of the design stage's output: the domain
// model of one bounded context and the contracts other teams see. When the
// work has a canonical event model, the design is domain modelling only:
// aggregates with their invariants, and the placement of each of the model's
// commands in an aggregate, with the invariant behind each rejection.
// Without one, the design also holds its own event model (events, commands,
// read models, slices), as on path v1. Parsing it is the boundary's job; the
// domain receives typed values and says what is wrong with them
// (Design.Problems).

// Invariant is a rule an aggregate always keeps, in prose.
type Invariant struct{ text string }

func NewInvariant(s string) (Invariant, error) {
	t, ok := nonEmpty(s)
	if !ok {
		return Invariant{}, fmt.Errorf("invariant: %w", ErrInvalidDesign)
	}
	return Invariant{text: t}, nil
}

func (i Invariant) String() string { return i.text }

// AggregateDesign is an aggregate: what it protects and the states it goes
// through.
type AggregateDesign struct {
	name       string
	invariants []Invariant
	states     []string
}

func NewAggregateDesign(name string, invariants []Invariant, states []string) AggregateDesign {
	return AggregateDesign{name: strings.TrimSpace(name), invariants: slices.Clone(invariants), states: trimAll(states)}
}

func (a AggregateDesign) Name() string            { return a.name }
func (a AggregateDesign) Invariants() []Invariant { return slices.Clone(a.invariants) }
func (a AggregateDesign) States() []string        { return slices.Clone(a.states) }

// CommandDesign is a command an aggregate handles: the events it produces
// when accepted and the rejections it may answer with.
type CommandDesign struct {
	name       string
	aggregate  string
	events     []string
	rejections []string
}

func NewCommandDesign(name, aggregate string, events, rejections []string) CommandDesign {
	return CommandDesign{name: strings.TrimSpace(name), aggregate: strings.TrimSpace(aggregate), events: trimAll(events), rejections: trimAll(rejections)}
}

func (c CommandDesign) Name() string         { return c.name }
func (c CommandDesign) Aggregate() string    { return c.aggregate }
func (c CommandDesign) Events() []string     { return slices.Clone(c.events) }
func (c CommandDesign) Rejections() []string { return slices.Clone(c.rejections) }

// Visibility says who sees an event: only its bounded context, or other
// teams too, through a contract.
type Visibility struct{ name string }

var (
	VisibilityInternal = Visibility{name: "internal"}
	VisibilityExternal = Visibility{name: "external"}
)

func ParseVisibility(s string) (Visibility, error) {
	for _, v := range []Visibility{VisibilityInternal, VisibilityExternal} {
		if v.name == s {
			return v, nil
		}
	}
	return Visibility{}, fmt.Errorf("visibility %q (available: internal, external): %w", s, ErrInvalidDesign)
}

func (v Visibility) String() string { return v.name }
func (v Visibility) isZero() bool   { return v.name == "" }

// EventDesign is an event of an aggregate.
type EventDesign struct {
	name       string
	aggregate  string
	visibility Visibility
}

func NewEventDesign(name, aggregate string, visibility Visibility) (EventDesign, error) {
	if err := requirePresent(required{"visibility", visibility}); err != nil {
		return EventDesign{}, fmt.Errorf("event %s: %w", name, err)
	}
	return EventDesign{name: strings.TrimSpace(name), aggregate: strings.TrimSpace(aggregate), visibility: visibility}, nil
}

func (e EventDesign) Name() string           { return e.name }
func (e EventDesign) Aggregate() string      { return e.aggregate }
func (e EventDesign) Visibility() Visibility { return e.visibility }

// ReadModelDesign is a read model and the events it is built from.
type ReadModelDesign struct {
	name string
	from []string
}

func NewReadModelDesign(name string, from []string) ReadModelDesign {
	return ReadModelDesign{name: strings.TrimSpace(name), from: trimAll(from)}
}

func (r ReadModelDesign) Name() string   { return r.name }
func (r ReadModelDesign) From() []string { return slices.Clone(r.from) }

// SliceKind is what a vertical slice does: handle a command, answer a query
// from a read model, or react to an event.
type SliceKind struct{ name string }

var (
	SliceCommand    = SliceKind{name: "command"}
	SliceQuery      = SliceKind{name: "query"}
	SliceAutomation = SliceKind{name: "automation"}
)

func ParseSliceKind(s string) (SliceKind, error) {
	for _, k := range []SliceKind{SliceCommand, SliceQuery, SliceAutomation} {
		if k.name == s {
			return k, nil
		}
	}
	return SliceKind{}, fmt.Errorf("slice kind %q (available: automation, command, query): %w", s, ErrInvalidDesign)
}

func (k SliceKind) String() string { return k.name }
func (k SliceKind) isZero() bool   { return k.name == "" }

// Change is what the design does to a slice.
type Change struct{ name string }

var (
	ChangeAdded     = Change{name: "added"}
	ChangeChanged   = Change{name: "changed"}
	ChangeRemoved   = Change{name: "removed"}
	ChangeUnchanged = Change{name: "unchanged"}
)

// Changes lists every change, in the order status counts them.
func Changes() []Change { return []Change{ChangeAdded, ChangeChanged, ChangeRemoved, ChangeUnchanged} }

func ParseChange(s string) (Change, error) {
	for _, c := range Changes() {
		if c.name == s {
			return c, nil
		}
	}
	return Change{}, fmt.Errorf("change %q (available: added, changed, removed, unchanged): %w", s, ErrInvalidDesign)
}

func (c Change) String() string { return c.name }
func (c Change) isZero() bool   { return c.name == "" }

// SliceValues are a slice's fields. Which references a slice needs depends
// on its kind; the others stay empty.
type SliceValues struct {
	Name string
	Kind SliceKind
	// Command is the command a command slice handles, or the command an
	// automation sends.
	Command string
	// ReadModel is the read model a query slice answers from.
	ReadModel string
	// On is the event an automation reacts to.
	On string
	// Publishes is the external event an automation publishes.
	Publishes string
	Change    Change
}

// SliceDesign is one vertical slice of the change.
type SliceDesign struct{ v SliceValues }

func NewSliceDesign(v SliceValues) (SliceDesign, error) {
	if err := requirePresent(required{"slice kind", v.Kind}, required{"slice change", v.Change}); err != nil {
		return SliceDesign{}, fmt.Errorf("slice %s: %w", v.Name, err)
	}
	v.Name, v.Command, v.ReadModel = strings.TrimSpace(v.Name), strings.TrimSpace(v.Command), strings.TrimSpace(v.ReadModel)
	v.On, v.Publishes = strings.TrimSpace(v.On), strings.TrimSpace(v.Publishes)
	return SliceDesign{v: v}, nil
}

func (s SliceDesign) Name() string        { return s.v.Name }
func (s SliceDesign) Kind() SliceKind     { return s.v.Kind }
func (s SliceDesign) Change() Change      { return s.v.Change }
func (s SliceDesign) Values() SliceValues { return s.v }

// NeedsSpecs reports whether specify must write specs for the slice: it
// handles a command or reacts to an event, and the design adds or changes
// it. Queries only read, and the owner reviews them with the design.
func (s SliceDesign) NeedsSpecs() bool {
	return (s.v.Kind == SliceCommand || s.v.Kind == SliceAutomation) && (s.v.Change == ChangeAdded || s.v.Change == ChangeChanged)
}

// CoveredBy reports whether a test is a spec of the slice: its name starts
// with "Test" and the slice's name, e.g. TestPostTransaction_RejectsWhenFrozen.
func (s SliceDesign) CoveredBy(test string) bool {
	return s.v.Name != "" && strings.HasPrefix(test, "Test"+s.v.Name)
}

// ContractKind is how other teams reach the bounded context.
type ContractKind struct{ name string }

var (
	ContractHTTP  = ContractKind{name: "http"}
	ContractEvent = ContractKind{name: "event"}
)

func ParseContractKind(s string) (ContractKind, error) {
	for _, k := range []ContractKind{ContractHTTP, ContractEvent} {
		if k.name == s {
			return k, nil
		}
	}
	return ContractKind{}, fmt.Errorf("contract kind %q (available: event, http): %w", s, ErrInvalidDesign)
}

func (k ContractKind) String() string { return k.name }
func (k ContractKind) isZero() bool   { return k.name == "" }

// ContractDesign is what other teams see: an HTTP endpoint served by a
// slice, or an external event and its schema.
type ContractDesign struct {
	kind   ContractKind
	name   string
	slice  string
	schema string
}

func NewContractDesign(kind ContractKind, name, slice, schema string) (ContractDesign, error) {
	if err := requirePresent(required{"contract kind", kind}); err != nil {
		return ContractDesign{}, fmt.Errorf("contract %s: %w", name, err)
	}
	return ContractDesign{kind: kind, name: strings.TrimSpace(name), slice: strings.TrimSpace(slice), schema: strings.TrimSpace(schema)}, nil
}

func (c ContractDesign) Kind() ContractKind { return c.kind }
func (c ContractDesign) Name() string       { return c.name }
func (c ContractDesign) Slice() string      { return c.slice }
func (c ContractDesign) Schema() string     { return c.schema }

// RejectionCause is the invariant of its aggregate that makes a command
// answer with a rejection.
type RejectionCause struct {
	Rejection string
	Invariant string
}

// PlacementDesign places a command of the event model in the aggregate that
// handles it, and says which of the aggregate's invariants each of the
// command's rejections protects.
type PlacementDesign struct {
	command    string
	aggregate  string
	rejections []RejectionCause
}

// NewPlacementDesign keeps the rejections in the order given; the boundary
// sorts them when they come from an unordered map.
func NewPlacementDesign(command, aggregate string, rejections []RejectionCause) PlacementDesign {
	p := PlacementDesign{command: strings.TrimSpace(command), aggregate: strings.TrimSpace(aggregate)}
	for _, r := range rejections {
		p.rejections = append(p.rejections, RejectionCause{Rejection: strings.TrimSpace(r.Rejection), Invariant: strings.TrimSpace(r.Invariant)})
	}
	return p
}

func (p PlacementDesign) Command() string              { return p.command }
func (p PlacementDesign) Aggregate() string            { return p.aggregate }
func (p PlacementDesign) Rejections() []RejectionCause { return slices.Clone(p.rejections) }

// DesignValues are a design's parts. Placements are only for a design of
// work with an event model; Commands, Events, ReadModels and Slices only
// for one without.
type DesignValues struct {
	BoundedContext string
	Aggregates     []AggregateDesign
	Commands       []CommandDesign
	Events         []EventDesign
	ReadModels     []ReadModelDesign
	Slices         []SliceDesign
	Placements     []PlacementDesign
	Contracts      []ContractDesign
}

// Design is the structured design of a change. Any value can be built;
// Problems says whether it is a valid design.
type Design struct{ v DesignValues }

func NewDesign(v DesignValues) Design {
	return Design{v: DesignValues{
		BoundedContext: strings.TrimSpace(v.BoundedContext),
		Aggregates:     slices.Clone(v.Aggregates), Commands: slices.Clone(v.Commands), Events: slices.Clone(v.Events),
		ReadModels: slices.Clone(v.ReadModels), Slices: slices.Clone(v.Slices), Placements: slices.Clone(v.Placements),
		Contracts: slices.Clone(v.Contracts),
	}}
}

func (d Design) BoundedContext() string        { return d.v.BoundedContext }
func (d Design) Aggregates() []AggregateDesign { return slices.Clone(d.v.Aggregates) }
func (d Design) Commands() []CommandDesign     { return slices.Clone(d.v.Commands) }
func (d Design) Events() []EventDesign         { return slices.Clone(d.v.Events) }
func (d Design) ReadModels() []ReadModelDesign { return slices.Clone(d.v.ReadModels) }
func (d Design) Slices() []SliceDesign         { return slices.Clone(d.v.Slices) }
func (d Design) Contracts() []ContractDesign   { return slices.Clone(d.v.Contracts) }
func (d Design) Placements() []PlacementDesign { return slices.Clone(d.v.Placements) }

// SlicesNeedingSpecs are the slices specify must write specs for, in order.
func (d Design) SlicesNeedingSpecs() []SliceDesign {
	var need []SliceDesign
	for _, s := range d.v.Slices {
		if s.NeedsSpecs() {
			need = append(need, s)
		}
	}
	return need
}

// UncoveredSlices are the slices needing specs that none of the tests
// covers, in order.
func (d Design) UncoveredSlices(tests []string) []SliceDesign {
	var uncovered []SliceDesign
	for _, s := range d.SlicesNeedingSpecs() {
		if !slices.ContainsFunc(tests, s.CoveredBy) {
			uncovered = append(uncovered, s)
		}
	}
	return uncovered
}

var pascalCase = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// Problems lists what makes the design invalid, one sentence each, in a
// stable order; none means it is valid. Given the work's canonical event
// model, the design is domain modelling only, cross-checked against the
// model (see modellingProblems). Without one, a design holds its own event
// model (events, commands, read models, slices), which follows the event
// model's structure rules, and places it in aggregates.
func (d Design) Problems(eventModel *EventModel) []string {
	var p problems
	v := d.v
	if v.BoundedContext == "" {
		p.add("the bounded context has no name")
	}
	aggregates := names(v.Aggregates, AggregateDesign.Name)
	p.uniqueNames("aggregate", aggregates, false)
	for _, a := range v.Aggregates {
		if len(a.invariants) == 0 {
			p.add("aggregate %s has no invariant", a.name)
		}
	}
	if eventModel != nil {
		d.modellingProblems(&p, *eventModel)
		return p.list
	}
	if len(v.Placements) > 0 {
		p.add("without an event model, the design names each command's aggregate in commands, not in placements")
	}

	model := d.eventModel()
	model.structureProblems(&p, "the design")
	for _, c := range v.Commands {
		if !slices.Contains(aggregates, c.aggregate) {
			p.add("command %s names aggregate %q, which the design doesn't have", c.name, c.aggregate)
		}
	}
	for _, e := range v.Events {
		if !slices.Contains(aggregates, e.aggregate) {
			p.add("event %s names aggregate %q, which the design doesn't have", e.name, e.aggregate)
		}
	}

	p.uniqueNames("contract", names(v.Contracts, ContractDesign.Name), false)
	var external []string
	for _, e := range v.Events {
		if e.visibility == VisibilityExternal {
			external = append(external, e.name)
		}
	}
	p.contractProblems(v.Contracts, external, names(v.Slices, SliceDesign.Name), "the design")
	model.structureEnd(&p)
	return p.list
}

// modellingProblems checks a design against the work's event model: it
// holds no events, commands, read models or slices of its own; every
// command of a slice the model adds or changes is placed, once, in one of
// its aggregates; every rejection the command declares is caused by one of
// that aggregate's invariants; every external event of the model has an
// event contract, and every http contract names a slice of the model.
func (d Design) modellingProblems(p *problems, model EventModel) {
	v := d.v
	for _, part := range []struct {
		name string
		n    int
	}{{"events", len(v.Events)}, {"commands", len(v.Commands)}, {"read models", len(v.ReadModels)}, {"slices", len(v.Slices)}} {
		if part.n > 0 {
			p.add("the design repeats %s of the event model: with an event model, it holds only aggregates, placements and contracts", part.name)
		}
	}
	placed := map[string]bool{}
	for _, pl := range v.Placements {
		command, ok := model.Command(pl.command)
		switch {
		case !ok:
			p.add("placement of command %q, which the event model doesn't have", pl.command)
			continue
		case placed[pl.command]:
			p.add("command %s is placed more than once", pl.command)
			continue
		}
		placed[pl.command] = true
		i := slices.IndexFunc(v.Aggregates, func(a AggregateDesign) bool { return a.name == pl.aggregate })
		if i < 0 {
			p.add("command %s is placed in aggregate %q, which the design doesn't have", pl.command, pl.aggregate)
			continue
		}
		aggregate := v.Aggregates[i]
		for _, r := range command.rejections {
			j := slices.IndexFunc(pl.rejections, func(c RejectionCause) bool { return c.Rejection == r })
			switch {
			case j < 0:
				p.add("rejection %s of command %s is mapped to no invariant of aggregate %s", r, pl.command, aggregate.name)
			case !slices.ContainsFunc(aggregate.invariants, func(inv Invariant) bool { return inv.text == pl.rejections[j].Invariant }):
				p.add("rejection %s of command %s is mapped to %q, which is not an invariant of aggregate %s", r, pl.command, pl.rejections[j].Invariant, aggregate.name)
			}
		}
		for _, c := range pl.rejections {
			if !slices.Contains(command.rejections, c.Rejection) {
				p.add("command %s doesn't declare rejection %q", pl.command, c.Rejection)
			}
		}
	}
	for _, s := range model.v.Slices {
		sv := s.v
		if (sv.Kind == SliceCommand || sv.Kind == SliceAutomation) && sv.Command != "" &&
			(sv.Change == ChangeAdded || sv.Change == ChangeChanged) && !placed[sv.Command] {
			placed[sv.Command] = true // report each command once
			p.add("command %s of slice %s is placed in no aggregate", sv.Command, sv.Name)
		}
	}
	p.uniqueNames("contract", names(v.Contracts, ContractDesign.Name), false)
	var external []string
	for _, e := range model.v.Events {
		if e.visibility == VisibilityExternal {
			external = append(external, e.name)
		}
	}
	p.contractProblems(v.Contracts, external, names(model.v.Slices, SliceDesign.Name), "the event model")
}

// eventModel is the event model a design holds.
func (d Design) eventModel() EventModel {
	var v EventModelValues
	for _, e := range d.v.Events {
		v.Events = append(v.Events, ModelEvent{name: e.name, visibility: e.visibility})
	}
	for _, c := range d.v.Commands {
		v.Commands = append(v.Commands, ModelCommand{name: c.name, events: c.events, rejections: c.rejections})
	}
	v.ReadModels, v.Slices = d.v.ReadModels, d.v.Slices
	return EventModel{v: v}
}

// contractProblems checks that every external event has an event contract,
// and that every http contract names an existing slice.
func (p *problems) contractProblems(contracts []ContractDesign, externalEvents, sliceNames []string, owner string) {
	for _, e := range externalEvents {
		if !slices.ContainsFunc(contracts, func(c ContractDesign) bool { return c.kind == ContractEvent && c.name == e }) {
			p.add("external event %s has no event contract", e)
		}
	}
	for _, c := range contracts {
		if c.kind == ContractHTTP && !slices.Contains(sliceNames, c.slice) {
			p.add("http contract %q names slice %q, which %s doesn't have", c.name, c.slice, owner)
		}
	}
}

// problems collects a design's problems in order.
type problems struct{ list []string }

func (p *problems) add(format string, args ...any) {
	p.list = append(p.list, fmt.Sprintf(format, args...))
}

// uniqueNames reports empty, repeated and, when pascal is set, non
// PascalCase names of one kind.
func (p *problems) uniqueNames(kind string, names []string, pascal bool) {
	seen := map[string]bool{}
	for _, n := range names {
		switch {
		case n == "":
			article := "a"
			if strings.ContainsRune("aeiou", rune(kind[0])) {
				article = "an"
			}
			p.add("%s %s has no name", article, kind)
			continue
		case seen[n]:
			p.add("%s name %s is used more than once", kind, n)
		case pascal && !pascalCase.MatchString(n):
			p.add("%s name %q is not PascalCase", kind, n)
		}
		seen[n] = true
	}
}

func names[T any](items []T, name func(T) string) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = name(item)
	}
	return out
}

func trimAll(values []string) []string {
	out := make([]string, len(values))
	for i, s := range values {
		out[i] = strings.TrimSpace(s)
	}
	return out
}
