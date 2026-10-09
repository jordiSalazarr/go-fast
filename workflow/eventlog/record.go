package eventlog

import (
	"errors"
	"fmt"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// Recorded is a domain event as stored in the log, with its envelope.
type Recorded struct {
	ID         string
	Position   int
	Stream     Stream
	Version    int
	Schema     int
	OccurredAt time.Time
	Actor      Actor
	Event      domain.Event
}

// Stream names one aggregate instance in the log.
type Stream string

func WorkStream(id domain.WorkID) Stream             { return Stream("work-" + id.String()) }
func AssignmentStream(id domain.AssignmentID) Stream { return Stream("assignment-" + id.String()) }

// ActorKind says who caused an event.
type ActorKind string

const (
	ActorOwner      ActorKind = "owner"
	ActorAgent      ActorKind = "agent"
	ActorAutomation ActorKind = "automation"
)

// Actor is who caused an event: the owner, an agent or an automation.
type Actor struct {
	Kind ActorKind
	Name string
}

func OwnerActor(owner domain.Owner) Actor { return Actor{Kind: ActorOwner, Name: owner.String()} }
func AgentActor(name string) Actor        { return Actor{Kind: ActorAgent, Name: name} }
func AutomationActor(name string) Actor   { return Actor{Kind: ActorAutomation, Name: name} }

var errInvalidActor = errors.New("invalid actor")

func (a Actor) validate() error {
	switch a.Kind {
	case ActorOwner, ActorAgent, ActorAutomation:
	default:
		return fmt.Errorf("actor kind %q: %w", a.Kind, errInvalidActor)
	}
	if a.Name == "" {
		return fmt.Errorf("actor without name: %w", errInvalidActor)
	}
	return nil
}

// Events turns a slice of aggregate events into domain events for Append.
func Events[E domain.Event](events []E) []domain.Event {
	out := make([]domain.Event, len(events))
	for i, e := range events {
		out[i] = e
	}
	return out
}

// StreamOf is the stream an event belongs to.
func StreamOf(e domain.Event) (Stream, error) {
	_, stream, _, err := encodeEvent(e)
	return stream, err
}
