package eventlog

import (
	"errors"
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

var ErrStreamNotFound = errors.New("stream not found")

// History indexes recorded events by stream and rebuilds aggregate state.
type History struct {
	streams map[Stream][]Recorded
	works   []domain.WorkID
}

func NewHistory(records []Recorded) History {
	h := History{streams: map[Stream][]Recorded{}}
	for _, r := range records {
		h.streams[r.Stream] = append(h.streams[r.Stream], r)
		if started, ok := r.Event.(domain.WorkStarted); ok {
			h.works = append(h.works, started.WorkID)
		}
	}
	return h
}

// Version is the current version of a stream; 0 if it has no events.
func (h History) Version(s Stream) int { return len(h.streams[s]) }

// Records returns the recorded events of a stream in order.
func (h History) Records(s Stream) []Recorded { return append([]Recorded(nil), h.streams[s]...) }

// Work rebuilds a work and returns it with its stream version.
func (h History) Work(id domain.WorkID) (domain.WorkState, int, error) {
	events, err := streamEvents[domain.WorkEvent](h, WorkStream(id))
	if err != nil {
		return nil, 0, err
	}
	state, err := domain.RebuildWork(events)
	if err != nil {
		return nil, 0, fmt.Errorf("load work %s: %w", id, err)
	}
	return state, len(events), nil
}

// Works rebuilds every work in the order they were started.
func (h History) Works() ([]domain.WorkState, error) {
	works := make([]domain.WorkState, 0, len(h.works))
	for _, id := range h.works {
		w, _, err := h.Work(id)
		if err != nil {
			return nil, err
		}
		works = append(works, w)
	}
	return works, nil
}

// Assignment rebuilds an assignment and returns it with its stream version.
func (h History) Assignment(id domain.AssignmentID) (domain.AssignmentState, int, error) {
	events, err := streamEvents[domain.AssignmentEvent](h, AssignmentStream(id))
	if err != nil {
		return nil, 0, err
	}
	state, err := domain.RebuildAssignment(events)
	if err != nil {
		return nil, 0, fmt.Errorf("load assignment %s: %w", id, err)
	}
	return state, len(events), nil
}

func streamEvents[E domain.Event](h History, s Stream) ([]E, error) {
	records := h.streams[s]
	if len(records) == 0 {
		return nil, fmt.Errorf("load %s: %w", s, ErrStreamNotFound)
	}
	events := make([]E, 0, len(records))
	for _, r := range records {
		e, ok := r.Event.(E)
		if !ok {
			return nil, fmt.Errorf("load %s: event at position %d is %T: %w", s, r.Position, r.Event, domain.ErrInconsistentHistory)
		}
		events = append(events, e)
	}
	return events, nil
}

// ActiveWork rebuilds the repository's active work and returns it with its
// stream version.
func (h History) ActiveWork() (domain.InProgressWork, int, error) {
	works, err := h.Works()
	if err != nil {
		return domain.InProgressWork{}, 0, err
	}
	w, err := domain.ActiveWorkIn(works)
	if err != nil {
		return domain.InProgressWork{}, 0, fmt.Errorf("load active work: %w", err)
	}
	return w, h.Version(WorkStream(w.ID())), nil
}
