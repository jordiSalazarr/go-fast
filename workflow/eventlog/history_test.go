package eventlog

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// visitsOf records the work entering each stage visit in turn, and the
// assignments accepted among them.
func visitsOf(entered []domain.StageEntered, accepted ...domain.AssignmentID) History {
	var records []Recorded
	for _, e := range entered {
		records = append(records, Recorded{Stream: WorkStream(workID), Event: e})
		id := domain.AssignmentIDFor(workID, e.Stage, e.Visit)
		records = append(records, Recorded{Stream: AssignmentStream(id), Event: domain.AssignmentOpened{AssignmentID: id, WorkID: workID, Stage: e.Stage, Visit: e.Visit, Gate: domain.GateAuto, Budget: budget3}})
		for _, a := range accepted {
			if a == id {
				records = append(records, Recorded{Stream: AssignmentStream(id), Event: domain.AssignmentAccepted{AssignmentID: id, WorkID: workID, Stage: e.Stage, Visit: e.Visit}})
			}
		}
	}
	return NewHistory(records)
}

func TestAccepted_AreEveryAcceptedVisitOfTheStage_OldestFirst_WithTheVisitEnteredNext(t *testing.T) {
	enter := func(s domain.Stage, n int) domain.StageEntered {
		return domain.StageEntered{WorkID: workID, Stage: s, Visit: must(domain.NewVisit(n)), Gate: domain.GateAuto, Budget: budget3}
	}
	id := func(s domain.Stage, n int) domain.AssignmentID {
		return domain.AssignmentIDFor(workID, s, must(domain.NewVisit(n)))
	}
	h := visitsOf([]domain.StageEntered{
		enter(domain.StageSpecify, 1), enter(domain.StageImplement, 1),
		enter(domain.StageSpecify, 2), enter(domain.StageImplement, 2),
		enter(domain.StageSpecify, 3),
	}, id(domain.StageSpecify, 1), id(domain.StageImplement, 1), id(domain.StageSpecify, 2))

	assert.Equal(t, []AcceptedVisit{
		{Accepted: id(domain.StageSpecify, 1), End: id(domain.StageImplement, 1)},
		{Accepted: id(domain.StageSpecify, 2), End: id(domain.StageImplement, 2)},
	}, h.Accepted(workID, domain.StageSpecify))
}
