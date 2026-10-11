package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func nfrWith(id string, targets domain.TargetValues) domain.NFR {
	return must(domain.NewNFR(must(domain.NewNFRID(id)), "", areas("internal/wallet/**"),
		must(domain.NewOperation("POST /wallets/{id}/transactions")), must(domain.NewLoad(10, 10*time.Second)), must(domain.NewTargets(targets))))
}

func ids(nfrs []domain.NFR) []string {
	var out []string
	for _, n := range nfrs {
		out = append(out, n.ID().String())
	}
	return out
}

// Given the repository's NFRs and a design's proposals, when they are
// adopted, then a new id is added, an existing id is replaced, an equal NFR
// changes nothing, and none is removed.
func TestAdoptNFRs(t *testing.T) {
	latency := nfrWith("latency", domain.TargetValues{P95: ptr(ms(150))})
	errors := nfrWith("errors", domain.TargetValues{MaxErrorRate: ptr(0.01)})
	current := []domain.NFR{latency, errors}

	t.Run("a new id is added", func(t *testing.T) {
		throughput := nfrWith("throughput", domain.TargetValues{MinRPS: ptr(200.0)})
		a := domain.AdoptNFRs(current, []domain.NFR{throughput})
		assert.True(t, a.Pending())
		assert.Equal(t, []string{"throughput"}, ids(a.Added()))
		assert.Empty(t, a.Replaced())
		assert.Equal(t, []string{"latency", "errors", "throughput"}, ids(a.Result()))
	})
	t.Run("an existing id is replaced in place", func(t *testing.T) {
		faster := nfrWith("latency", domain.TargetValues{P95: ptr(ms(100)), MinRPS: ptr(200.0)})
		a := domain.AdoptNFRs(current, []domain.NFR{faster})
		assert.True(t, a.Pending())
		assert.Empty(t, a.Added())
		require.Len(t, a.Replaced(), 1)
		assert.True(t, a.Replaced()[0].Old.Equal(latency))
		assert.True(t, a.Replaced()[0].New.Equal(faster))
		assert.Equal(t, []string{"latency", "errors"}, ids(a.Result()))
		assert.True(t, a.Result()[0].Equal(faster))
	})
	t.Run("an equal NFR changes nothing", func(t *testing.T) {
		a := domain.AdoptNFRs(current, []domain.NFR{nfrWith("errors", domain.TargetValues{MaxErrorRate: ptr(0.01)})})
		assert.False(t, a.Pending())
		assert.Equal(t, []string{"latency", "errors"}, ids(a.Result()))
	})
	t.Run("no NFRs yet", func(t *testing.T) {
		a := domain.AdoptNFRs(nil, []domain.NFR{latency})
		assert.Equal(t, []string{"latency"}, ids(a.Added()))
		assert.Equal(t, []string{"latency"}, ids(a.Result()))
	})
}

func TestNFREqual(t *testing.T) {
	a := nfrWith("latency", domain.TargetValues{P95: ptr(ms(150))})
	assert.True(t, a.Equal(nfrWith("latency", domain.TargetValues{P95: ptr(ms(150))})))
	assert.False(t, a.Equal(nfrWith("latency", domain.TargetValues{P95: ptr(ms(151))})))
	assert.False(t, a.Equal(nfrWith("latency", domain.TargetValues{P95: ptr(ms(150)), MinRPS: ptr(1.0)})))
	assert.False(t, a.Equal(nfrWith("other", domain.TargetValues{P95: ptr(ms(150))})))
}

// Given an assignment, when the owner wants to adopt NFR proposals, then
// it is allowed only while design waits for approval.
func TestCanAdoptNFRProposals(t *testing.T) {
	assignment := func(stage domain.Stage, events ...domain.AssignmentEvent) domain.AssignmentState {
		id := domain.AssignmentIDFor(workID, stage, visit1)
		opened := domain.AssignmentOpened{AssignmentID: id, WorkID: workID, Stage: stage, Visit: visit1, Gate: domain.GateHuman, Budget: budget3}
		return must(domain.RebuildAssignment(append([]domain.AssignmentEvent{opened}, events...)))
	}
	submitted := func(stage domain.Stage) domain.AssignmentEvent {
		return domain.SubmittedForApproval{AssignmentID: domain.AssignmentIDFor(workID, stage, visit1), Attempt: attempt(1)}
	}

	assert.NoError(t, domain.CanAdoptNFRProposals(assignment(domain.StageDesign, submitted(domain.StageDesign))))
	assert.ErrorIs(t, domain.CanAdoptNFRProposals(assignment(domain.StageDesign)), domain.ErrNFRAdoptionClosed)
	assert.ErrorIs(t, domain.CanAdoptNFRProposals(assignment(domain.StageSpecify, submitted(domain.StageSpecify))), domain.ErrNFRAdoptionClosed)
	assert.ErrorIs(t, domain.CanAdoptNFRProposals(nil), domain.ErrNFRAdoptionClosed)
}
