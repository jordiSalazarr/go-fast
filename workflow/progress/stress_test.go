package progress_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

func ptr[T any](v T) *T { return &v }

var walletNFR = Must(domain.NewNFR(Must(domain.NewNFRID("wallet-latency")), "", []domain.AreaGlob{Must(domain.NewAreaGlob("server/wallet/**"))},
	Must(domain.NewOperation("POST /wallets/{id}/transactions")), Must(domain.NewLoad(50, time.Minute)),
	Must(domain.NewTargets(domain.TargetValues{P95: ptr(150 * time.Millisecond)}))))

// changed is a work that changed these files, in a repository with the
// wallet NFR.
func changed(names ...string) progress.Facts {
	return progress.Facts{Stress: func(domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error) {
		var paths []domain.RepoPath
		for _, n := range names {
			paths = append(paths, Must(domain.NewRepoPath("/repo", n)))
		}
		return []domain.NFR{walletNFR}, domain.NewChangedFiles(paths...), nil
	}}
}

func queried(t *testing.T, facts progress.Facts, history ...domain.Event) progress.View {
	t.Helper()
	var view progress.View
	eventlogtest.Given(t, history...).WhenQueried(func(log *eventlog.Snapshot) error {
		var err error
		view, err = progress.Read(log, Branch, facts)
		return err
	})
	return view
}

func text(v progress.View) string {
	var b strings.Builder
	progress.RenderText(&b, v)
	return b.String()
}

var walletMandatory = progress.MandatoryNFRView{
	ID: "wallet-latency", Areas: []string{"server/wallet/**"}, Operation: "POST /wallets/{id}/transactions",
	Load: "50 vus for 1m0s", Targets: "p95 ≤ 150ms", Script: ".gofast/works/w1/stress/wallet-latency.js",
}

func TestGivenReviewOfAChangeInAnNFRsArea_WhenQueryingStatus_ThenStressTestingIsMandatory(t *testing.T) {
	v := queried(t, changed("server/wallet/post.go"), OpenOn(domain.StageReview)...)

	require.NotNil(t, v.Current.Stress)
	assert.Equal(t, []progress.MandatoryNFRView{walletMandatory}, v.Current.Stress.Mandatory)
	assert.Nil(t, v.Current.Stress.Decision)
	assert.Equal(t, []string{`gf submit --passed --stress include "<reason>"`, `gf submit --failed "<reason>"`}, v.Next.Commands)
	assert.Contains(t, v.Next.Message, "stress testing is mandatory: the change touches the areas of wallet-latency")
	assert.Contains(t, text(v), "Stress testing is mandatory: the change touches the areas of these NFRs:\n"+
		"  wallet-latency (server/wallet/**): POST /wallets/{id}/transactions, 50 vus for 1m0s; p95 ≤ 150ms\n")
}

func TestGivenReviewOfAChangeOutsideEveryNFR_WhenQueryingStatus_ThenReviewChooses(t *testing.T) {
	v := queried(t, changed("server/server.go"), OpenOn(domain.StageReview)...)

	assert.Empty(t, v.Current.Stress.Mandatory)
	assert.Equal(t, []string{`gf submit --passed --stress <include|skip> "<reason>"`, `gf submit --failed "<reason>"`}, v.Next.Commands)
	assert.Contains(t, text(v), "Stress testing: no NFR covers this change; include it if the change can affect behaviour under load.\n")
}

func TestGivenTheNFRsCantBeRead_WhenQueryingStatus_ThenItSaysSo(t *testing.T) {
	broken := progress.Facts{Stress: func(domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error) {
		return nil, domain.ChangedFiles{}, errors.New(".gofast/nfrs.json: not valid JSON for gf")
	}}

	v := queried(t, broken, OpenOn(domain.StageReview)...)

	assert.Equal(t, "gf could not tell which NFRs this change makes mandatory: .gofast/nfrs.json: not valid JSON for gf", v.Current.Stress.Problem)
	assert.Equal(t, `gf submit --passed --stress <include|skip> "<reason>"`, v.Next.Commands[0])
}

func TestGivenReviewSkippedStressTesting_WhenQueryingStatus_ThenItShowsTheDecisionAndTheSkippedStage(t *testing.T) {
	v := queried(t, changed("server/server.go"), OpenOn(domain.StageIntegrationTesting)...)

	assert.Equal(t, &progress.StressDecisionView{
		Decision: "skip", Reason: "no change affects behaviour under load", ForcedBy: []string{}, DecidedBy: progress.DecidedByReview,
	}, v.Current.Stress.Decision)
	last := v.Work.Stages[len(v.Work.Stages)-1]
	assert.Equal(t, progress.StageView{Stage: "stress-testing", Gate: "auto", Budget: 3, Status: progress.StageSkipped, Optional: true}, last)
	assert.Contains(t, text(v), "  [skipped] stress-testing       auto gate, optional\n")
	assert.Contains(t, text(v), "Stress decision: skip, by review: no change affects behaviour under load\n")
}

func TestGivenTheOwnerIncludedStressTesting_WhenQueryingStatus_ThenTheDecisionIsTheirs(t *testing.T) {
	forced := Must(domain.NewStressChoice(domain.StressInclude, Reason("the owner wants it"), []domain.NFRID{walletNFR.ID()}))
	given := append(OpenOn(domain.StageIntegrationTesting),
		domain.StressDecided{WorkID: WorkID, Decision: forced.Decision(), Reason: forced.Reason(), ForcedBy: forced.ForcedBy(), Owner: Owner})

	v := queried(t, changed("server/wallet/post.go"), given...)

	assert.Equal(t, &progress.StressDecisionView{
		Decision: "include", Reason: "the owner wants it", ForcedBy: []string{"wallet-latency"}, DecidedBy: progress.DecidedByOwner,
	}, v.Current.Stress.Decision)
	assert.Equal(t, progress.StagePending, v.Work.Stages[len(v.Work.Stages)-1].Status)
	assert.Contains(t, text(v), "Stress decision: include (mandatory: wallet-latency), by owner: the owner wants it\n")
}

func TestGivenStressTestingAfterAnAttempt_WhenQueryingStatus_ThenItShowsTheMandatoryNFRsAndTheMeasurements(t *testing.T) {
	facts := changed("server/wallet/post.go")
	facts.StressResults = func(id domain.AssignmentID) (int, []exitchecks.StressResult, bool, error) {
		require.Equal(t, AssignmentOn(domain.StageStressTesting), id)
		return 1, []exitchecks.StressResult{{
			NFR: "wallet-latency", Targets: "p95 ≤ 150ms", Missed: []string{"p95 212ms > 150ms"},
			Measured: "p95 212ms, p99 300ms, error rate 0%, rps 240, 14400 requests", P95Ms: 212, P99Ms: 300, RPS: 240, Requests: 14400,
		}}, true, nil
	}
	given := OpenOn(domain.StageStressTesting, Failed(domain.StageStressTesting, 1, "stress-scripts-valid: wallet-latency.js sets options.rps"))

	v := queried(t, facts, given...)

	st := v.Current.Stress
	require.NotNil(t, st)
	assert.Equal(t, []progress.MandatoryNFRView{walletMandatory}, st.Mandatory)
	assert.Equal(t, &progress.StressResultsView{Attempt: 1, RunDir: ".gofast/runtime/checks/w1-stress-testing-1-attempt1/", NFRs: []exitchecks.StressResult{{
		NFR: "wallet-latency", Targets: "p95 ≤ 150ms", Missed: []string{"p95 212ms > 150ms"},
		Measured: "p95 212ms, p99 300ms, error rate 0%, rps 240, 14400 requests", P95Ms: 212, P99Ms: 300, RPS: 240, Requests: 14400,
	}}}, st.Results)
	assert.Equal(t, []string{"artifact-written", "stress-scripts-valid", "nfrs-met"}, v.Current.Checks)
	out := text(v)
	assert.Contains(t, out, "    script: .gofast/works/w1/stress/wallet-latency.js\n")
	assert.Contains(t, out, "Last stress run (attempt 1, .gofast/runtime/checks/w1-stress-testing-1-attempt1/):\n"+
		"  wallet-latency: p95 212ms > 150ms (p95 212ms, p99 300ms, error rate 0%, rps 240, 14400 requests; targets p95 ≤ 150ms)\n")
}

func TestGivenTheFixerAfterStressTesting_WhenQueryingStatus_ThenItIsInPerformanceModeWithTheRun(t *testing.T) {
	facts := progress.Facts{StressResults: func(domain.AssignmentID) (int, []exitchecks.StressResult, bool, error) {
		return 1, []exitchecks.StressResult{{NFR: "wallet-latency"}}, true, nil
	}}
	given := append(OpenOn(domain.StageStressTesting, HandedToFixer(domain.StageStressTesting, 1, "nfrs-met: wallet-latency: p95 212ms > 150ms.")),
		domain.FixerEntered{WorkID: WorkID, From: domain.StageStressTesting, FromVisit: Visit1, Visit: Visit1, Mode: domain.FixerModePerformance,
			Reason: Reason("nfrs-met: wallet-latency: p95 212ms > 150ms.")},
		StageEntered(domain.StageFixer), Opened(domain.StageFixer))

	v := queried(t, facts, given...)

	require.NotNil(t, v.Current.Fixer)
	assert.Equal(t, "performance", v.Current.Fixer.Mode)
	assert.Equal(t, ".gofast/runtime/checks/w1-stress-testing-1-attempt1/", v.Current.Fixer.StressRunDir)
	assert.Equal(t, ".gofast/runtime/checks/w1-stress-testing-1-attempt1.log", v.Current.Fixer.CheckLog)
	assert.Nil(t, v.Current.Stress)
	assert.Contains(t, text(v), "Handed over by: stress-testing (visit 1), performance: nfrs-met: wallet-latency: p95 212ms > 150ms.\n")
	assert.Contains(t, text(v), "Its stress run (k6 output and logs): .gofast/runtime/checks/w1-stress-testing-1-attempt1/\n")
}
