// Package eventlogtest runs given / when / then scenarios against a real
// event log in a temporary directory.
//
//	eventlogtest.Given(t, pastEvents...).
//		When(func(log *eventlog.Session) error { ... }).
//		Then(newEvents...)
package eventlogtest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// GivenActor records the past events of a scenario.
var GivenActor = eventlog.AutomationActor("given")

type Scenario struct {
	t     *testing.T
	store *eventlog.Store
	given int
	added []eventlog.Recorded
	err   error
	ran   bool
}

// Given appends past events, each to its own stream, in order.
func Given(t *testing.T, history ...domain.Event) *Scenario {
	t.Helper()
	store, err := eventlog.Open(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		for _, e := range history {
			stream, err := eventlog.StreamOf(e)
			if err != nil {
				return err
			}
			records, _ := s.ReadAll()
			version := eventlog.NewHistory(records).Version(stream)
			if err := s.Append(stream, version, GivenActor, e); err != nil {
				return err
			}
		}
		return nil
	}), "given history must append")
	return &Scenario{t: t, store: store, given: len(history)}
}

// When runs a command under the exclusive lock and captures the events it
// appended and the error it returned.
func (s *Scenario) When(command func(log *eventlog.Session) error) *Scenario {
	s.t.Helper()
	s.ran = true
	require.NoError(s.t, s.store.Exclusive(func(log *eventlog.Session) error {
		s.err = command(log)
		records, err := log.ReadAll()
		s.added = records[s.given:]
		return err
	}))
	return s
}

// WhenQueried runs a query under the shared lock.
func (s *Scenario) WhenQueried(query func(log *eventlog.Snapshot) error) {
	s.t.Helper()
	require.NoError(s.t, s.store.Shared(query))
}

// Then asserts the command succeeded and appended exactly these events.
func (s *Scenario) Then(want ...domain.Event) *Scenario {
	s.t.Helper()
	require.True(s.t, s.ran, "Then without When")
	require.NoError(s.t, s.err)
	assert.Equal(s.t, want, s.events())
	return s
}

// ThenRejected asserts the command failed with target and appended nothing.
func (s *Scenario) ThenRejected(target error) *Scenario {
	s.t.Helper()
	require.True(s.t, s.ran, "ThenRejected without When")
	require.ErrorIs(s.t, s.err, target)
	assert.Empty(s.t, s.events(), "a rejected command appends nothing")
	return s
}

// ThenRecordedBy asserts every new event carries the actor.
func (s *Scenario) ThenRecordedBy(actor eventlog.Actor) *Scenario {
	s.t.Helper()
	for _, r := range s.added {
		assert.Equal(s.t, actor, r.Actor, "actor of %T", r.Event)
	}
	return s
}

// Err is the error the command returned, for assertions beyond ErrorIs.
func (s *Scenario) Err() error { return s.err }

func (s *Scenario) events() []domain.Event {
	var events []domain.Event
	for _, r := range s.added {
		events = append(events, r.Event)
	}
	return events
}
