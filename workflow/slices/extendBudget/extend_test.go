package extendbudget_test

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	extendbudget "github.com/jordiSalazarr/go-fast/workflow/slices/extendBudget"
)

var owner = eventlog.OwnerActor(Owner)

func extendingBy(n int) func(*eventlog.Session) error {
	return func(log *eventlog.Session) error {
		_, _, err := extendbudget.ExtendBudget(log, owner, Branch, Owner, Budget(n))
		return err
	}
}

func TestGivenEscalatedAssignment_WhenTheOwnerExtendsTheBudget_ThenItReopensWithTheNewBudget(t *testing.T) {
	eventlogtest.Given(t, EscalatedOn(domain.StageImplement)...).
		When(extendingBy(2)).
		Then(domain.BudgetExtended{
			AssignmentID: AssignmentOn(domain.StageImplement), Owner: Owner, Additional: Budget(2), NewBudget: Budget(5),
		}).
		ThenRecordedBy(owner)
}

func TestGivenOpenAssignment_WhenTheOwnerExtendsTheBudget_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageImplement)...).
		When(extendingBy(2)).
		ThenRejected(domain.ErrNotEscalated)
}

func TestGivenNoActiveWork_WhenTheOwnerExtendsTheBudget_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, Completed()...).
		When(extendingBy(2)).
		ThenRejected(domain.ErrNoActiveWork)
}

func TestGivenAnAgent_WhenRunningExtend_ThenItIsRefusedBeforeTouchingTheLog(t *testing.T) {
	agent := Must(caller.Resolve(func(string) string { return "agent" }, nil))
	opened := false
	cmd := extendbudget.NewCommand(
		func() (*eventlog.Store, error) { opened = true; return nil, errors.New("must not open") },
		func() (caller.Caller, error) { return agent, nil },
		func() (domain.Branch, error) { return Branch, nil },
	)
	cmd.SetArgs([]string{"2"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()

	require.ErrorIs(t, err, caller.ErrOwnerOnly)
	assert.False(t, opened)
}
