package extendreworkbudget_test

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	extendreworkbudget "github.com/jordiSalazarr/go-fast/workflow/slices/extendReworkBudget"
)

var owner = eventlog.OwnerActor(Owner)

func extendingBy(n int) func(*eventlog.Session) error {
	return func(log *eventlog.Session) error {
		_, err := extendreworkbudget.ExtendReworkBudget(log, owner, Branch, Owner, Must(domain.NewReworkBudget(n)))
		return err
	}
}

func atReworkLimit() []domain.Event {
	return append(ReworkedOnReview(3), OpenedVisit(domain.StageReview, 4), HandedToFixerOnVisit(domain.StageReview, 4, "suite fails"),
		domain.ReworkLimitReached{WorkID: WorkID, Reworks: Must(domain.NewReworkCount(3))})
}

func TestGivenTheReworkLimit_WhenTheOwnerExtendsTheReworkBudget_ThenItIsExtended(t *testing.T) {
	eventlogtest.Given(t, atReworkLimit()...).
		When(extendingBy(2)).
		Then(domain.ReworkBudgetExtended{
			WorkID: WorkID, Owner: Owner, Additional: Must(domain.NewReworkBudget(2)), NewBudget: Must(domain.NewReworkBudget(5)),
		}).
		ThenRecordedBy(owner)
}

func TestGivenWorkInProgress_WhenTheOwnerExtendsTheReworkBudget_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageReview)...).
		When(extendingBy(1)).
		ThenRejected(domain.ErrNotAtReworkLimit)
}

func TestGivenNoActiveWork_WhenTheOwnerExtendsTheReworkBudget_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, Completed()...).
		When(extendingBy(1)).
		ThenRejected(domain.ErrNoActiveWork)
}

func TestGivenAnAgentOrNoTerminal_WhenRunningReworkExtend_ThenItIsRefusedBeforeTouchingTheLog(t *testing.T) {
	agent := Must(caller.Resolve(func(string) string { return "agent" }, nil, true))
	noTerminal := Must(caller.Resolve(func(string) string { return "" }, func(string) (string, error) { return "Jordi", nil }, false))
	for name, c := range map[string]caller.Caller{"agent": agent, "no terminal": noTerminal} {
		t.Run(name, func(t *testing.T) {
			opened := false
			cmd := extendreworkbudget.NewCommand(
				func() (*eventlog.Store, error) { opened = true; return nil, errors.New("must not open") },
				automations.Runner{},
				func() (caller.Caller, error) { return c, nil },
				func() (domain.Branch, error) { return Branch, nil },
			)
			cmd.SetArgs([]string{"1"})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			err := cmd.Execute()

			require.ErrorIs(t, err, caller.ErrOwnerOnly)
			assert.False(t, opened)
		})
	}
}
