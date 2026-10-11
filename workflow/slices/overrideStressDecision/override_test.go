package overridestressdecision_test

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	overridestressdecision "github.com/jordiSalazarr/go-fast/workflow/slices/overrideStressDecision"
)

var owner = eventlog.OwnerActor(Owner)

func ptr[T any](v T) *T { return &v }

// walletNFR covers server/wallet/.
var walletNFR = Must(domain.NewNFR(Must(domain.NewNFRID("wallet-latency")), "", []domain.AreaGlob{Must(domain.NewAreaGlob("server/wallet/**"))},
	Must(domain.NewOperation("POST /wallets/{id}/transactions")), Must(domain.NewLoad(5, time.Second)),
	Must(domain.NewTargets(domain.TargetValues{MinRPS: ptr(10.0)}))))

// workChanged reports the NFRs and the files the work changed.
func workChanged(names ...string) overridestressdecision.StressFacts {
	return func(domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error) {
		var paths []domain.RepoPath
		for _, n := range names {
			paths = append(paths, Must(domain.NewRepoPath("/repo", n)))
		}
		return []domain.NFR{walletNFR}, domain.NewChangedFiles(paths...), nil
	}
}

func overriding(decision domain.StressDecision, facts overridestressdecision.StressFacts) func(*eventlog.Session) error {
	return func(log *eventlog.Session) error {
		_, err := overridestressdecision.OverrideStressDecision(log, owner, Branch, Owner, decision, Reason("the owner says so"), facts)
		return err
	}
}

func TestGivenWorkOnIntegrationTesting_WhenTheOwnerIncludesStressTesting_ThenTheirDecisionIsRecorded(t *testing.T) {
	eventlogtest.Given(t, WorkOn(domain.StageIntegrationTesting)...).
		When(overriding(domain.StressInclude, workChanged("server/server.go"))).
		Then(domain.StressDecided{WorkID: WorkID, Decision: domain.StressInclude, Reason: Reason("the owner says so"), Owner: Owner}).
		ThenRecordedBy(owner)
}

func TestGivenAChangeInAnNFRsArea_WhenTheOwnerSkipsStressTesting_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, WorkOn(domain.StageImplement)...).
		When(overriding(domain.StressSkip, workChanged("server/wallet/post.go"))).
		ThenRejected(domain.ErrStressForced)
}

func TestGivenAChangeInAnNFRsArea_WhenTheOwnerIncludesStressTesting_ThenItIsForcedByTheNFR(t *testing.T) {
	eventlogtest.Given(t, WorkOn(domain.StageReview)...).
		When(overriding(domain.StressInclude, workChanged("server/wallet/post.go"))).
		Then(domain.StressDecided{WorkID: WorkID, Decision: domain.StressInclude, Reason: Reason("the owner says so"),
			ForcedBy: []domain.NFRID{walletNFR.ID()}, Owner: Owner})
}

func TestGivenWorkPastIntegrationTesting_WhenTheOwnerOverrides_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, WorkOn(domain.StageStressTesting)...).
		When(overriding(domain.StressSkip, workChanged("server/server.go"))).
		ThenRejected(domain.ErrStressDecisionClosed)
}

func TestGivenWorkInTheFixer_WhenTheOwnerOverrides_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, InFixer(domain.StageReview)...).
		When(overriding(domain.StressInclude, workChanged())).
		ThenRejected(domain.ErrStressDecisionClosed)
}

func TestGivenNoActiveWork_WhenTheOwnerOverrides_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, Completed()...).
		When(overriding(domain.StressInclude, workChanged())).
		ThenRejected(domain.ErrNoActiveWork)
}

func TestGivenAnAgentOrNoTerminal_WhenRunningStress_ThenItIsRefusedBeforeTouchingTheLog(t *testing.T) {
	agent := Must(caller.Resolve(func(string) string { return "agent" }, nil, true))
	noTerminal := Must(caller.Resolve(func(string) string { return "" }, func(string) (string, error) { return "Jordi", nil }, false))
	for name, c := range map[string]caller.Caller{"agent": agent, "no terminal": noTerminal} {
		t.Run(name, func(t *testing.T) {
			opened := false
			cmd := overridestressdecision.NewCommand(
				func() (*eventlog.Store, error) { opened = true; return nil, errors.New("must not open") },
				automations.Runner{},
				func() (caller.Caller, error) { return c, nil },
				func() (domain.Branch, error) { return Branch, nil },
				workChanged(),
			)
			cmd.SetArgs([]string{"skip", "docs only"})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			err := cmd.Execute()

			require.ErrorIs(t, err, caller.ErrOwnerOnly)
			assert.False(t, opened)
		})
	}
}
