package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// An event model is the canonical form of the owner's event model: the
// events, commands and read models of the change, its slices, and the
// scenarios that say, slice by slice, what happens given past events. The
// event-modelling stage writes it; grill asks about it, design places it in
// aggregates, and specify covers its scenarios. Parsing it is the boundary's
// job; the domain receives typed values and says what is wrong with them
// (EventModel.Problems).

// ModelEvent is an event of the model.
type ModelEvent struct {
	name       string
	visibility Visibility
}

func NewModelEvent(name string, visibility Visibility) (ModelEvent, error) {
	if err := requirePresent(required{"visibility", visibility}); err != nil {
		return ModelEvent{}, fmt.Errorf("event %s: %w", name, err)
	}
	return ModelEvent{name: strings.TrimSpace(name), visibility: visibility}, nil
}

func (e ModelEvent) Name() string           { return e.name }
func (e ModelEvent) Visibility() Visibility { return e.visibility }

// ModelCommand is a command of the model: the events it produces when
// accepted and the rejections it may answer with.
type ModelCommand struct {
	name       string
	events     []string
	rejections []string
}

func NewModelCommand(name string, events, rejections []string) ModelCommand {
	return ModelCommand{name: strings.TrimSpace(name), events: trimAll(events), rejections: trimAll(rejections)}
}

func (c ModelCommand) Name() string         { return c.name }
func (c ModelCommand) Events() []string     { return slices.Clone(c.events) }
func (c ModelCommand) Rejections() []string { return slices.Clone(c.rejections) }

// ScenarioThen is what a scenario expects: the events the command produces,
// the rejection it answers with, or the state of the slice's read model.
// Exactly one of them is set in a valid scenario.
type ScenarioThen struct {
	Events    []string
	Rejection string
	ReadModel string
	// State describes the read model's state, in prose.
	State string
}

// ScenarioValues are a scenario's fields.
type ScenarioValues struct {
	ID    string
	Slice string
	// Given are the events that happened before, in order.
	Given []string
	// When is the command of a command or automation slice; empty for a
	// query slice, and for an automation that only publishes an event.
	When        string
	Then        ScenarioThen
	Description string
}

// Scenario is one example of a slice's behaviour: given past events, when a
// command arrives, then what happens.
type Scenario struct{ v ScenarioValues }

func NewScenario(v ScenarioValues) Scenario {
	v.ID, v.Slice, v.When, v.Description = strings.TrimSpace(v.ID), strings.TrimSpace(v.Slice), strings.TrimSpace(v.When), strings.TrimSpace(v.Description)
	v.Given = trimAll(v.Given)
	v.Then = ScenarioThen{Events: trimAll(v.Then.Events), Rejection: strings.TrimSpace(v.Then.Rejection),
		ReadModel: strings.TrimSpace(v.Then.ReadModel), State: strings.TrimSpace(v.Then.State)}
	return Scenario{v: v}
}

func (s Scenario) ID() string             { return s.v.ID }
func (s Scenario) Slice() string          { return s.v.Slice }
func (s Scenario) Values() ScenarioValues { return s.v }

// outcomes counts how many kinds of outcome the scenario expects.
func (t ScenarioThen) outcomes() int {
	n := 0
	for _, set := range []bool{len(t.Events) > 0, t.Rejection != "", t.ReadModel != ""} {
		if set {
			n++
		}
	}
	return n
}

// EventModelValues are an event model's parts. ExistingEvents are events of
// the system outside this model that scenarios, read models and automations
// may refer to.
type EventModelValues struct {
	Events         []ModelEvent
	Commands       []ModelCommand
	ReadModels     []ReadModelDesign
	Slices         []SliceDesign
	Scenarios      []Scenario
	ExistingEvents []string
}

// EventModel is the canonical event model of a change. Any value can be
// built; Problems says whether it is a valid one.
type EventModel struct{ v EventModelValues }

func NewEventModel(v EventModelValues) EventModel {
	return EventModel{v: EventModelValues{
		Events: slices.Clone(v.Events), Commands: slices.Clone(v.Commands), ReadModels: slices.Clone(v.ReadModels),
		Slices: slices.Clone(v.Slices), Scenarios: slices.Clone(v.Scenarios), ExistingEvents: trimAll(v.ExistingEvents),
	}}
}

func (m EventModel) Events() []ModelEvent          { return slices.Clone(m.v.Events) }
func (m EventModel) Commands() []ModelCommand      { return slices.Clone(m.v.Commands) }
func (m EventModel) ReadModels() []ReadModelDesign { return slices.Clone(m.v.ReadModels) }
func (m EventModel) Slices() []SliceDesign         { return slices.Clone(m.v.Slices) }
func (m EventModel) Scenarios() []Scenario         { return slices.Clone(m.v.Scenarios) }
func (m EventModel) ExistingEvents() []string      { return slices.Clone(m.v.ExistingEvents) }

// Command is the model's command of a name.
func (m EventModel) Command(name string) (ModelCommand, bool) {
	i := slices.IndexFunc(m.v.Commands, func(c ModelCommand) bool { return c.name == name })
	if i < 0 {
		return ModelCommand{}, false
	}
	return m.v.Commands[i], true
}

// ScenariosToCover are the scenarios of the slices the model adds or
// changes, in order: specify covers each with a spec.
func (m EventModel) ScenariosToCover() []Scenario {
	var cover []Scenario
	for _, s := range m.v.Scenarios {
		i := slices.IndexFunc(m.v.Slices, func(sl SliceDesign) bool { return sl.v.Name == s.v.Slice })
		if i >= 0 && (m.v.Slices[i].v.Change == ChangeAdded || m.v.Slices[i].v.Change == ChangeChanged) {
			cover = append(cover, s)
		}
	}
	return cover
}

var scenarioID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Problems lists what makes the event model invalid, one sentence each, in
// a stable order; none means it is valid: its structure (Design checks the
// same of a design without an event model), then its scenarios.
func (m EventModel) Problems() []string {
	var p problems
	m.structureProblems(&p, "the event model")
	m.structureEnd(&p)
	m.scenarioProblems(&p)
	return p.list
}

// structureProblems checks the model's events, commands, read models and
// slices; owner names what is missing a reference ("the event model", "the
// design").
func (m EventModel) structureProblems(p *problems, owner string) {
	v := m.v
	commands := names(v.Commands, ModelCommand.Name)
	events := names(v.Events, ModelEvent.Name)
	known := append(slices.Clone(events), v.ExistingEvents...)
	readModels := names(v.ReadModels, ReadModelDesign.Name)

	p.uniqueNames("command", commands, true)
	p.uniqueNames("event", events, true)
	p.uniqueNames("read model", readModels, false)
	p.uniqueNames("slice", names(v.Slices, SliceDesign.Name), true)

	for _, c := range v.Commands {
		if len(c.events) == 0 {
			p.add("command %s produces no event", c.name)
		}
		for _, e := range c.events {
			if !slices.Contains(events, e) {
				p.add("command %s produces event %q, which %s doesn't have", c.name, e, owner)
			}
		}
		if slices.Contains(c.rejections, "") {
			p.add("command %s has a rejection without a name", c.name)
		}
	}

	for _, e := range v.Events {
		if !m.produces(e.name) && !m.consumedFromOutside(e.name) {
			p.add("event %s is produced by no command and published or consumed by no automation", e.name)
		}
	}

	for _, r := range v.ReadModels {
		if len(r.from) == 0 {
			p.add("read model %s is built from no event", r.name)
		}
		for _, e := range r.from {
			if !slices.Contains(known, e) {
				p.add("read model %s is built from event %q, which %s doesn't have", r.name, e, owner)
			}
		}
	}

	for _, s := range v.Slices {
		m.checkSlice(p, s, owner, commands, known, readModels)
	}
}

// structureEnd are the structure rules that come after a design's own
// rules about aggregates and contracts: at least one slice changes.
func (m EventModel) structureEnd(p *problems) {
	if !slices.ContainsFunc(m.v.Slices, func(s SliceDesign) bool { return s.v.Change != ChangeUnchanged }) {
		p.add("no slice is added, changed or removed")
	}
}

// checkSlice checks that a slice references what its kind needs.
func (m EventModel) checkSlice(p *problems, s SliceDesign, owner string, commands, events, readModels []string) {
	v := s.v
	switch v.Kind {
	case SliceCommand:
		if !slices.Contains(commands, v.Command) {
			p.add("command slice %s handles command %q, which %s doesn't have", v.Name, v.Command, owner)
		}
	case SliceQuery:
		if !slices.Contains(readModels, v.ReadModel) {
			p.add("query slice %s reads read model %q, which %s doesn't have", v.Name, v.ReadModel, owner)
		}
	case SliceAutomation:
		if !slices.Contains(events, v.On) {
			p.add("automation slice %s reacts to event %q, which %s doesn't have", v.Name, v.On, owner)
		}
		switch {
		case v.Command == "" && v.Publishes == "":
			p.add("automation slice %s neither sends a command nor publishes an external event", v.Name)
		case v.Command != "" && !slices.Contains(commands, v.Command):
			p.add("automation slice %s sends command %q, which %s doesn't have", v.Name, v.Command, owner)
		}
		if v.Publishes != "" && !slices.ContainsFunc(m.v.Events, func(e ModelEvent) bool {
			return e.name == v.Publishes && e.visibility == VisibilityExternal
		}) {
			p.add("automation slice %s publishes %q, which is not an external event of %s", v.Name, v.Publishes, owner)
		}
	}
}

// produces reports whether a command produces the event, or an automation
// publishes it.
func (m EventModel) produces(event string) bool {
	return slices.ContainsFunc(m.v.Commands, func(c ModelCommand) bool { return slices.Contains(c.events, event) }) ||
		slices.ContainsFunc(m.v.Slices, func(s SliceDesign) bool { return s.v.Kind == SliceAutomation && s.v.Publishes == event })
}

// consumedFromOutside reports whether an automation reacts to the event:
// then it may come from outside the bounded context.
func (m EventModel) consumedFromOutside(event string) bool {
	return slices.ContainsFunc(m.v.Slices, func(s SliceDesign) bool { return s.v.Kind == SliceAutomation && s.v.On == event })
}

// scenarioProblems checks each scenario against its slice, and that every
// slice the model adds or changes that handles a command has scenarios:
// one at least, and one per rejection its command declares.
func (m EventModel) scenarioProblems(p *problems) {
	events := names(m.v.Events, ModelEvent.Name)
	known := append(slices.Clone(events), m.v.ExistingEvents...)
	seen := map[string]bool{}
	for _, s := range m.v.Scenarios {
		v := s.v
		switch {
		case v.ID == "":
			p.add("a scenario of slice %s has no id", v.Slice)
		case !scenarioID.MatchString(v.ID):
			p.add("scenario id %q has characters other than letters, digits, '.', '_' and '-'", v.ID)
		case seen[v.ID]:
			p.add("scenario id %s is used more than once", v.ID)
		}
		seen[v.ID] = true
		for _, e := range v.Given {
			if !slices.Contains(known, e) {
				p.add("scenario %s is given event %q, which is neither in the event model nor in existingEvents", v.ID, e)
			}
		}
		i := slices.IndexFunc(m.v.Slices, func(sl SliceDesign) bool { return sl.v.Name == v.Slice })
		if i < 0 {
			p.add("scenario %s is about slice %q, which the event model doesn't have", v.ID, v.Slice)
			continue
		}
		m.checkScenario(p, s, m.v.Slices[i])
	}

	for _, sl := range m.v.Slices {
		v := sl.v
		if (v.Kind != SliceCommand && v.Kind != SliceAutomation) || (v.Change != ChangeAdded && v.Change != ChangeChanged) {
			continue
		}
		own := slices.DeleteFunc(slices.Clone(m.v.Scenarios), func(s Scenario) bool { return s.v.Slice != v.Name })
		if len(own) == 0 {
			p.add("%s slice %s has no scenario", v.Kind, v.Name)
			continue
		}
		command, ok := m.Command(v.Command)
		if !ok {
			continue
		}
		for _, r := range command.rejections {
			if r != "" && !slices.ContainsFunc(own, func(s Scenario) bool { return s.v.Then.Rejection == r }) {
				p.add("%s slice %s has no scenario for rejection %s of command %s", v.Kind, v.Name, r, command.name)
			}
		}
	}
}

// checkScenario checks a scenario against its slice: its when is the
// slice's command, absent for queries and for automations that only
// publish; its then is one outcome the slice can have.
func (m EventModel) checkScenario(p *problems, s Scenario, slice SliceDesign) {
	v, sv := s.v, slice.v
	command := sv.Command
	if sv.Kind == SliceQuery {
		command = ""
	}
	switch {
	case command == "" && v.When != "":
		p.add("scenario %s: slice %s takes no command, so the scenario has no when, not %q", v.ID, sv.Name, v.When)
	case command != "" && v.When != command:
		p.add("scenario %s: when must be %s, the command of slice %s, not %q", v.ID, command, sv.Name, v.When)
	}
	t := v.Then
	if t.outcomes() != 1 {
		p.add("scenario %s: then must have exactly one of events, rejection or readModel", v.ID)
		return
	}
	c, hasCommand := m.Command(command)
	switch {
	case len(t.Events) > 0 && command != "":
		for _, e := range t.Events {
			if !slices.Contains(c.events, e) {
				p.add("scenario %s: command %s doesn't produce %q", v.ID, command, e)
			}
		}
	case len(t.Events) > 0 && sv.Kind == SliceAutomation && sv.Publishes != "":
		for _, e := range t.Events {
			if e != sv.Publishes {
				p.add("scenario %s: slice %s publishes %s, not %q", v.ID, sv.Name, sv.Publishes, e)
			}
		}
	case len(t.Events) > 0:
		p.add("scenario %s: slice %s produces no events", v.ID, sv.Name)
	case t.Rejection != "" && (!hasCommand || !slices.Contains(c.rejections, t.Rejection)):
		p.add("scenario %s: rejection %s is not declared on command %s", v.ID, t.Rejection, command)
	case t.ReadModel != "" && (sv.Kind != SliceQuery || t.ReadModel != sv.ReadModel):
		p.add("scenario %s: readModel %s is not the read model of slice %s", v.ID, t.ReadModel, sv.Name)
	case t.ReadModel != "" && t.State == "":
		p.add("scenario %s: then describes no state of read model %s", v.ID, t.ReadModel)
	}
}

// Assumption is something the event-modelling agent had to assume, with a
// stable id that grill's decisions refer to.
type Assumption struct {
	id   string
	text string
}

var assumptionID = regexp.MustCompile(`^M[0-9]+$`)

// NewAssumption needs an id of the form M1, M2, … and a text.
func NewAssumption(id, text string) (Assumption, error) {
	id, t := strings.TrimSpace(id), strings.TrimSpace(text)
	if !assumptionID.MatchString(id) || t == "" {
		return Assumption{}, fmt.Errorf("assumption %q: %w", id, ErrInvalidEventModel)
	}
	return Assumption{id: id, text: t}, nil
}

func (a Assumption) ID() string   { return a.id }
func (a Assumption) Text() string { return a.text }

// AssumptionProblems lists repeated assumption ids.
func AssumptionProblems(assumptions []Assumption) []string {
	var p problems
	seen := map[string]bool{}
	for _, a := range assumptions {
		if seen[a.id] {
			p.add("assumption id %s is used more than once", a.id)
		}
		seen[a.id] = true
	}
	return p.list
}
