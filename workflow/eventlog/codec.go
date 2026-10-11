package eventlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

const schemaV1 = 1

var errUnknownEvent = errors.New("unknown event type")

// Stable event names. Never rename: they are stored in committed logs.
const (
	typeWorkStarted             = "WorkStarted"
	typeStageEntered            = "StageEntered"
	typeWorkCompleted           = "WorkCompleted"
	typeWorkAbandoned           = "WorkAbandoned"
	typeAssignmentOpened        = "AssignmentOpened"
	typeAttemptFailed           = "AttemptFailed"
	typeSubmittedForApproval    = "SubmittedForApproval"
	typeAssignmentRejected      = "AssignmentRejected"
	typeAssignmentEscalated     = "AssignmentEscalated"
	typeBudgetExtended          = "BudgetExtended"
	typeAssignmentAccepted      = "AssignmentAccepted"
	typeAssignmentCancelled     = "AssignmentCancelled"
	typeAssignmentHandedToFixer = "AssignmentHandedToFixer"
	typeFixerEntered            = "FixerEntered"
	typeReworkLimitReached      = "ReworkLimitReached"
	typeReworkStarted           = "ReworkStarted"
	typeReworkBudgetExtended    = "ReworkBudgetExtended"
)

type envelope struct {
	ID         string          `json:"id"`
	Position   int             `json:"position"`
	Stream     string          `json:"stream"`
	Version    int             `json:"version"`
	Type       string          `json:"type"`
	Schema     int             `json:"schema"`
	OccurredAt time.Time       `json:"occurredAt"`
	Actor      actorV1         `json:"actor"`
	Payload    json.RawMessage `json:"payload"`
}

type actorV1 struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Payloads, schema 1.

type ownerV1 struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type workStartedV1 struct {
	WorkID      string `json:"workId"`
	WorkType    string `json:"workType"`
	Description string `json:"description"`
	Branch      string `json:"branch"` // required; logs from before work belonged to a branch are not supported
}

type stageEnteredV1 struct {
	WorkID string `json:"workId"`
	Stage  string `json:"stage"`
	Visit  int    `json:"visit"`
	Gate   string `json:"gate"`
	Budget int    `json:"budget"`
}

type workCompletedV1 struct {
	WorkID string `json:"workId"`
}

type workAbandonedV1 struct {
	WorkID string  `json:"workId"`
	Owner  ownerV1 `json:"owner"`
	Reason string  `json:"reason"`
}

type fixerEnteredV1 struct {
	WorkID    string `json:"workId"`
	From      string `json:"from"`
	FromVisit int    `json:"fromVisit"`
	Visit     int    `json:"visit"`
	Mode      string `json:"mode"`
	Reason    string `json:"reason"`
}

type reworkLimitReachedV1 struct {
	WorkID  string `json:"workId"`
	Reworks int    `json:"reworks"`
}

type reworkStartedV1 struct {
	WorkID string `json:"workId"`
	Target string `json:"target"`
	Visit  int    `json:"visit"`
	Reason string `json:"reason"`
}

type reworkBudgetExtendedV1 struct {
	WorkID     string  `json:"workId"`
	Owner      ownerV1 `json:"owner"`
	Additional int     `json:"additional"`
	NewBudget  int     `json:"newBudget"`
}

type assignmentOpenedV1 struct {
	AssignmentID string `json:"assignmentId"`
	WorkID       string `json:"workId"`
	Stage        string `json:"stage"`
	Visit        int    `json:"visit"`
	Gate         string `json:"gate"`
	Budget       int    `json:"budget"`
}

type attemptFailedV1 struct {
	AssignmentID string `json:"assignmentId"`
	Attempt      int    `json:"attempt"`
	Reason       string `json:"reason"`
}

type submittedForApprovalV1 struct {
	AssignmentID string `json:"assignmentId"`
	Attempt      int    `json:"attempt"`
}

type assignmentRejectedV1 struct {
	AssignmentID string  `json:"assignmentId"`
	Owner        ownerV1 `json:"owner"`
	Feedback     string  `json:"feedback"`
	Attempt      int     `json:"attempt"`
}

type assignmentEscalatedV1 struct {
	AssignmentID string `json:"assignmentId"`
	AttemptsUsed int    `json:"attemptsUsed"`
	Budget       int    `json:"budget"`
}

type budgetExtendedV1 struct {
	AssignmentID string  `json:"assignmentId"`
	Owner        ownerV1 `json:"owner"`
	Additional   int     `json:"additional"`
	NewBudget    int     `json:"newBudget"`
}

type assignmentAcceptedV1 struct {
	AssignmentID string `json:"assignmentId"`
	WorkID       string `json:"workId"`
	Stage        string `json:"stage"`
	Visit        int    `json:"visit"`
	// The fixer's route; absent for every other stage.
	Route       string `json:"route,omitempty"`
	RouteReason string `json:"routeReason,omitempty"`
}

type assignmentCancelledV1 struct {
	AssignmentID string `json:"assignmentId"`
	Reason       string `json:"reason"`
}

type assignmentHandedToFixerV1 struct {
	AssignmentID string `json:"assignmentId"`
	WorkID       string `json:"workId"`
	Stage        string `json:"stage"`
	Visit        int    `json:"visit"`
	Attempt      int    `json:"attempt"`
	Reason       string `json:"reason"`
}

func encodeLine(r Recorded) ([]byte, error) {
	eventType, stream, payload, err := encodeEvent(r.Event)
	if err != nil {
		return nil, err
	}
	if stream != r.Stream {
		return nil, fmt.Errorf("%s belongs to stream %s, not %s", eventType, stream, r.Stream)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", eventType, err)
	}
	return json.Marshal(envelope{
		ID: r.ID, Position: r.Position, Stream: string(r.Stream), Version: r.Version,
		Type: eventType, Schema: r.Schema, OccurredAt: r.OccurredAt,
		Actor:   actorV1{Kind: string(r.Actor.Kind), Name: r.Actor.Name},
		Payload: raw,
	})
}

func decodeLine(line []byte) (Recorded, error) {
	var env envelope
	if err := strictUnmarshal(line, &env); err != nil {
		return Recorded{}, fmt.Errorf("decode envelope: %w", err)
	}
	if env.Schema != schemaV1 {
		return Recorded{}, fmt.Errorf("%s: unsupported schema %d", env.Type, env.Schema)
	}
	event, stream, err := decodeEvent(env.Type, env.Payload)
	if err != nil {
		return Recorded{}, err
	}
	if string(stream) != env.Stream {
		return Recorded{}, fmt.Errorf("%s belongs to stream %s, not %s", env.Type, stream, env.Stream)
	}
	actor := Actor{Kind: ActorKind(env.Actor.Kind), Name: env.Actor.Name}
	if err := actor.validate(); err != nil {
		return Recorded{}, err
	}
	return Recorded{
		ID: env.ID, Position: env.Position, Stream: Stream(env.Stream), Version: env.Version,
		Schema: env.Schema, OccurredAt: env.OccurredAt, Actor: actor, Event: event,
	}, nil
}

func encodeEvent(e domain.Event) (eventType string, stream Stream, payload any, err error) {
	switch e := e.(type) {
	case domain.WorkStarted:
		return typeWorkStarted, WorkStream(e.WorkID), workStartedV1{
			WorkID: e.WorkID.String(), WorkType: e.WorkType.String(), Description: e.Description.String(), Branch: e.Branch.String(),
		}, nil
	case domain.StageEntered:
		return typeStageEntered, WorkStream(e.WorkID), stageEnteredV1{
			WorkID: e.WorkID.String(), Stage: e.Stage.String(), Visit: e.Visit.Int(),
			Gate: e.Gate.String(), Budget: e.Budget.Int(),
		}, nil
	case domain.WorkCompleted:
		return typeWorkCompleted, WorkStream(e.WorkID), workCompletedV1{WorkID: e.WorkID.String()}, nil
	case domain.WorkAbandoned:
		return typeWorkAbandoned, WorkStream(e.WorkID), workAbandonedV1{
			WorkID: e.WorkID.String(), Owner: encodeOwner(e.Owner), Reason: e.Reason.String(),
		}, nil
	case domain.FixerEntered:
		return typeFixerEntered, WorkStream(e.WorkID), fixerEnteredV1{
			WorkID: e.WorkID.String(), From: e.From.String(), FromVisit: e.FromVisit.Int(), Visit: e.Visit.Int(),
			Mode: e.Mode.String(), Reason: e.Reason.String(),
		}, nil
	case domain.ReworkLimitReached:
		return typeReworkLimitReached, WorkStream(e.WorkID), reworkLimitReachedV1{WorkID: e.WorkID.String(), Reworks: e.Reworks.Int()}, nil
	case domain.ReworkStarted:
		return typeReworkStarted, WorkStream(e.WorkID), reworkStartedV1{
			WorkID: e.WorkID.String(), Target: e.Target.String(), Visit: e.Visit.Int(), Reason: e.Reason.String(),
		}, nil
	case domain.ReworkBudgetExtended:
		return typeReworkBudgetExtended, WorkStream(e.WorkID), reworkBudgetExtendedV1{
			WorkID: e.WorkID.String(), Owner: encodeOwner(e.Owner), Additional: e.Additional.Int(), NewBudget: e.NewBudget.Int(),
		}, nil
	case domain.AssignmentOpened:
		return typeAssignmentOpened, AssignmentStream(e.AssignmentID), assignmentOpenedV1{
			AssignmentID: e.AssignmentID.String(), WorkID: e.WorkID.String(), Stage: e.Stage.String(),
			Visit: e.Visit.Int(), Gate: e.Gate.String(), Budget: e.Budget.Int(),
		}, nil
	case domain.AttemptFailed:
		return typeAttemptFailed, AssignmentStream(e.AssignmentID), attemptFailedV1{
			AssignmentID: e.AssignmentID.String(), Attempt: e.Attempt.Int(), Reason: e.Reason.String(),
		}, nil
	case domain.SubmittedForApproval:
		return typeSubmittedForApproval, AssignmentStream(e.AssignmentID), submittedForApprovalV1{
			AssignmentID: e.AssignmentID.String(), Attempt: e.Attempt.Int(),
		}, nil
	case domain.AssignmentRejected:
		return typeAssignmentRejected, AssignmentStream(e.AssignmentID), assignmentRejectedV1{
			AssignmentID: e.AssignmentID.String(), Owner: encodeOwner(e.Owner),
			Feedback: e.Feedback.String(), Attempt: e.Attempt.Int(),
		}, nil
	case domain.AssignmentEscalated:
		return typeAssignmentEscalated, AssignmentStream(e.AssignmentID), assignmentEscalatedV1{
			AssignmentID: e.AssignmentID.String(), AttemptsUsed: e.AttemptsUsed.Int(), Budget: e.Budget.Int(),
		}, nil
	case domain.BudgetExtended:
		return typeBudgetExtended, AssignmentStream(e.AssignmentID), budgetExtendedV1{
			AssignmentID: e.AssignmentID.String(), Owner: encodeOwner(e.Owner),
			Additional: e.Additional.Int(), NewBudget: e.NewBudget.Int(),
		}, nil
	case domain.AssignmentAccepted:
		return typeAssignmentAccepted, AssignmentStream(e.AssignmentID), assignmentAcceptedV1{
			AssignmentID: e.AssignmentID.String(), WorkID: e.WorkID.String(),
			Stage: e.Stage.String(), Visit: e.Visit.Int(),
			Route: e.Route.Target().String(), RouteReason: e.Route.Reason().String(),
		}, nil
	case domain.AssignmentCancelled:
		return typeAssignmentCancelled, AssignmentStream(e.AssignmentID), assignmentCancelledV1{
			AssignmentID: e.AssignmentID.String(), Reason: e.Reason.String(),
		}, nil
	case domain.AssignmentHandedToFixer:
		return typeAssignmentHandedToFixer, AssignmentStream(e.AssignmentID), assignmentHandedToFixerV1{
			AssignmentID: e.AssignmentID.String(), WorkID: e.WorkID.String(), Stage: e.Stage.String(),
			Visit: e.Visit.Int(), Attempt: e.Attempt.Int(), Reason: e.Reason.String(),
		}, nil
	}
	return "", "", nil, fmt.Errorf("encode %T: %w", e, errUnknownEvent)
}

func decodeEvent(eventType string, payload json.RawMessage) (domain.Event, Stream, error) {
	var d decoder
	var event domain.Event
	switch eventType {
	case typeWorkStarted:
		var p workStartedV1
		d.unmarshal(payload, &p)
		event = domain.WorkStarted{
			WorkID: d.workID(p.WorkID), WorkType: d.workType(p.WorkType), Description: d.description(p.Description),
			Branch: d.branch(p.Branch),
		}
	case typeStageEntered:
		var p stageEnteredV1
		d.unmarshal(payload, &p)
		event = domain.StageEntered{
			WorkID: d.workID(p.WorkID), Stage: d.stage(p.Stage), Visit: d.visit(p.Visit),
			Gate: d.gate(p.Gate), Budget: d.budget(p.Budget),
		}
	case typeWorkCompleted:
		var p workCompletedV1
		d.unmarshal(payload, &p)
		event = domain.WorkCompleted{WorkID: d.workID(p.WorkID)}
	case typeWorkAbandoned:
		var p workAbandonedV1
		d.unmarshal(payload, &p)
		event = domain.WorkAbandoned{WorkID: d.workID(p.WorkID), Owner: d.owner(p.Owner), Reason: d.reason(p.Reason)}
	case typeFixerEntered:
		var p fixerEnteredV1
		d.unmarshal(payload, &p)
		event = domain.FixerEntered{
			WorkID: d.workID(p.WorkID), From: d.stage(p.From), FromVisit: d.visit(p.FromVisit), Visit: d.visit(p.Visit),
			Mode: d.fixerMode(p.Mode), Reason: d.reason(p.Reason),
		}
	case typeReworkLimitReached:
		var p reworkLimitReachedV1
		d.unmarshal(payload, &p)
		event = domain.ReworkLimitReached{WorkID: d.workID(p.WorkID), Reworks: d.reworkCount(p.Reworks)}
	case typeReworkStarted:
		var p reworkStartedV1
		d.unmarshal(payload, &p)
		event = domain.ReworkStarted{WorkID: d.workID(p.WorkID), Target: d.stage(p.Target), Visit: d.visit(p.Visit), Reason: d.reason(p.Reason)}
	case typeReworkBudgetExtended:
		var p reworkBudgetExtendedV1
		d.unmarshal(payload, &p)
		event = domain.ReworkBudgetExtended{
			WorkID: d.workID(p.WorkID), Owner: d.owner(p.Owner),
			Additional: d.reworkBudget(p.Additional), NewBudget: d.reworkBudget(p.NewBudget),
		}
	case typeAssignmentOpened:
		var p assignmentOpenedV1
		d.unmarshal(payload, &p)
		event = domain.AssignmentOpened{
			AssignmentID: d.assignmentID(p.AssignmentID), WorkID: d.workID(p.WorkID), Stage: d.stage(p.Stage),
			Visit: d.visit(p.Visit), Gate: d.gate(p.Gate), Budget: d.budget(p.Budget),
		}
	case typeAttemptFailed:
		var p attemptFailedV1
		d.unmarshal(payload, &p)
		event = domain.AttemptFailed{
			AssignmentID: d.assignmentID(p.AssignmentID), Attempt: d.attempt(p.Attempt), Reason: d.reason(p.Reason),
		}
	case typeSubmittedForApproval:
		var p submittedForApprovalV1
		d.unmarshal(payload, &p)
		event = domain.SubmittedForApproval{AssignmentID: d.assignmentID(p.AssignmentID), Attempt: d.attempt(p.Attempt)}
	case typeAssignmentRejected:
		var p assignmentRejectedV1
		d.unmarshal(payload, &p)
		event = domain.AssignmentRejected{
			AssignmentID: d.assignmentID(p.AssignmentID), Owner: d.owner(p.Owner),
			Feedback: d.feedback(p.Feedback), Attempt: d.attempt(p.Attempt),
		}
	case typeAssignmentEscalated:
		var p assignmentEscalatedV1
		d.unmarshal(payload, &p)
		event = domain.AssignmentEscalated{
			AssignmentID: d.assignmentID(p.AssignmentID), AttemptsUsed: d.attemptCount(p.AttemptsUsed), Budget: d.budget(p.Budget),
		}
	case typeBudgetExtended:
		var p budgetExtendedV1
		d.unmarshal(payload, &p)
		event = domain.BudgetExtended{
			AssignmentID: d.assignmentID(p.AssignmentID), Owner: d.owner(p.Owner),
			Additional: d.budget(p.Additional), NewBudget: d.budget(p.NewBudget),
		}
	case typeAssignmentAccepted:
		var p assignmentAcceptedV1
		d.unmarshal(payload, &p)
		accepted := domain.AssignmentAccepted{
			AssignmentID: d.assignmentID(p.AssignmentID), WorkID: d.workID(p.WorkID),
			Stage: d.stage(p.Stage), Visit: d.visit(p.Visit),
		}
		if p.Route != "" || p.RouteReason != "" {
			accepted.Route = d.route(p.Route, p.RouteReason)
		}
		event = accepted
	case typeAssignmentCancelled:
		var p assignmentCancelledV1
		d.unmarshal(payload, &p)
		event = domain.AssignmentCancelled{AssignmentID: d.assignmentID(p.AssignmentID), Reason: d.reason(p.Reason)}
	case typeAssignmentHandedToFixer:
		var p assignmentHandedToFixerV1
		d.unmarshal(payload, &p)
		event = domain.AssignmentHandedToFixer{
			AssignmentID: d.assignmentID(p.AssignmentID), WorkID: d.workID(p.WorkID), Stage: d.stage(p.Stage),
			Visit: d.visit(p.Visit), Attempt: d.attempt(p.Attempt), Reason: d.reason(p.Reason),
		}
	default:
		return nil, "", fmt.Errorf("decode %q: %w", eventType, errUnknownEvent)
	}
	if d.err != nil {
		return nil, "", fmt.Errorf("decode %s payload: %w", eventType, d.err)
	}
	_, stream, _, err := encodeEvent(event)
	return event, stream, err
}

func encodeOwner(o domain.Owner) ownerV1 { return ownerV1{Name: o.Name(), Email: o.Email()} }

func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// decoder turns payload fields into domain values, keeping the first error.
type decoder struct{ err error }

func (d *decoder) keep(err error) {
	if d.err == nil {
		d.err = err
	}
}

func (d *decoder) unmarshal(payload json.RawMessage, v any) { d.keep(strictUnmarshal(payload, v)) }

func (d *decoder) workID(s string) domain.WorkID {
	v, err := domain.NewWorkID(s)
	d.keep(err)
	return v
}

func (d *decoder) assignmentID(s string) domain.AssignmentID {
	v, err := domain.ParseAssignmentID(s)
	d.keep(err)
	return v
}

func (d *decoder) workType(s string) domain.WorkType {
	v, err := domain.ParseWorkType(s)
	d.keep(err)
	return v
}

func (d *decoder) stage(s string) domain.Stage {
	v, err := domain.ParseStage(s)
	d.keep(err)
	return v
}

func (d *decoder) gate(s string) domain.Gate {
	v, err := domain.ParseGate(s)
	d.keep(err)
	return v
}

func (d *decoder) visit(n int) domain.Visit {
	v, err := domain.NewVisit(n)
	d.keep(err)
	return v
}

func (d *decoder) budget(n int) domain.AttemptBudget {
	v, err := domain.NewAttemptBudget(n)
	d.keep(err)
	return v
}

func (d *decoder) route(target, reason string) domain.Route {
	v, err := domain.NewRoute(d.stage(target), d.reason(reason))
	d.keep(err)
	return v
}

func (d *decoder) fixerMode(s string) domain.FixerMode {
	v, err := domain.ParseFixerMode(s)
	d.keep(err)
	return v
}

func (d *decoder) reworkCount(n int) domain.ReworkCount {
	v, err := domain.NewReworkCount(n)
	d.keep(err)
	return v
}

func (d *decoder) reworkBudget(n int) domain.ReworkBudget {
	v, err := domain.NewReworkBudget(n)
	d.keep(err)
	return v
}

func (d *decoder) attempt(n int) domain.Attempt {
	v, err := domain.NewAttempt(n)
	d.keep(err)
	return v
}

func (d *decoder) attemptCount(n int) domain.AttemptCount {
	v, err := domain.NewAttemptCount(n)
	d.keep(err)
	return v
}

func (d *decoder) description(s string) domain.Description {
	v, err := domain.NewDescription(s)
	d.keep(err)
	return v
}

func (d *decoder) feedback(s string) domain.Feedback {
	v, err := domain.NewFeedback(s)
	d.keep(err)
	return v
}

func (d *decoder) reason(s string) domain.Reason {
	v, err := domain.NewReason(s)
	d.keep(err)
	return v
}

func (d *decoder) branch(s string) domain.Branch {
	v, err := domain.NewBranch(s)
	d.keep(err)
	return v
}

func (d *decoder) owner(o ownerV1) domain.Owner {
	v, err := domain.NewOwner(o.Name, o.Email)
	d.keep(err)
	return v
}
