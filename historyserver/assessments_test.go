package historyserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/historyserver"
	"github.com/jordiSalazarr/go-fast/workflow/remote"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// troubled holds w-1, whose discovery failed an attempt, was rejected and
// escalated, went to the fixer, was reworked into a second visit, and was
// returned to by the owner from plan; w-2, abandoned; and w, whose id
// prefixes w-1's.
func troubled(t *testing.T) (base string, client *remote.Client) {
	t.Helper()
	c, _, base := serve(t, secret)
	events := []string{
		line("t1", "work-w-1", 1, "WorkStarted", `{"workId":"w-1","workType":"fix-bug","description":"Login button does nothing","branch":"main"}`),
		line("t2", "work-w-1", 2, "StageEntered", `{"workId":"w-1","stage":"discovery","visit":1,"gate":"human","budget":3}`),
		line("t3", "assignment-w-1-discovery-1", 1, "AssignmentOpened", `{"assignmentId":"w-1-discovery-1","workId":"w-1","stage":"discovery","visit":1,"gate":"human","budget":3}`),
		line("t4", "assignment-w-1-discovery-1", 2, "AttemptFailed", `{"assignmentId":"w-1-discovery-1","attempt":1,"reason":"no artifact"}`),
		line("t5", "assignment-w-1-discovery-1", 3, "AssignmentRejected", `{"assignmentId":"w-1-discovery-1","owner":{"name":"Jordi"},"feedback":"read the logs","attempt":2}`),
		line("t6", "assignment-w-1-discovery-1", 4, "AssignmentEscalated", `{"assignmentId":"w-1-discovery-1","attemptsUsed":3,"budget":3}`),
		line("t7", "work-w-1", 3, "FixerEntered", `{"workId":"w-1","from":"discovery","fromVisit":1,"visit":1,"mode":"diagnose","reason":"escalated"}`),
		line("t8", "work-w-1", 4, "ReworkStarted", `{"workId":"w-1","target":"discovery","visit":2,"reason":"the skill skips the logs"}`),
		line("t9", "work-w-1", 5, "StageEntered", `{"workId":"w-1","stage":"discovery","visit":2,"gate":"human","budget":3}`),
		line("ta", "work-w-1", 6, "WorkReturned", `{"workId":"w-1","owner":{"name":"Jordi"},"from":"plan","fromVisit":1,"target":"discovery","visit":3,"reason":"wrong cause"}`),
		line("tb", "work-w-2", 1, "WorkStarted", `{"workId":"w-2","workType":"refactor","description":"Ledger","branch":"ledger"}`),
		line("tc", "work-w-2", 2, "WorkAbandoned", `{"workId":"w-2","owner":{"name":"Jordi"},"reason":"not needed"}`),
		line("td", "work-w", 1, "WorkStarted", `{"workId":"w","workType":"fix-bug","description":"Prefix","branch":"p"}`),
	}
	_, err := c.SendEvents(context.Background(), wire.Events{Events: events})
	require.NoError(t, err)
	return base, c
}

func TestServer_FindsTheTroubles_OfEveryWork(t *testing.T) {
	base, _ := troubled(t)
	var troubles wire.Troubles
	get(t, base+"/v1/projects/wallets/troubles", &troubles)
	type seen struct {
		work, stage string
		visit       int
		kind, why   string
	}
	var got []seen
	for _, x := range troubles.Troubles {
		got = append(got, seen{x.Work, x.Stage, x.Visit, x.Type, x.Reason})
		assert.NotEmpty(t, x.Event)
		assert.False(t, x.OccurredAt.IsZero())
	}
	assert.Equal(t, []seen{
		{"w-2", "", 0, "WorkAbandoned", "not needed"},
		{"w-1", "plan", 1, "WorkReturned", "wrong cause"},
		{"w-1", "discovery", 1, "ReworkStarted", "the skill skips the logs"},
		{"w-1", "discovery", 1, "AssignmentEscalated", "3 of 3 attempts used"},
		{"w-1", "discovery", 1, "AssignmentRejected", "read the logs"},
		{"w-1", "discovery", 1, "AttemptFailed", "no artifact"},
	}, got, "newest first")

	get(t, base+"/v1/projects/wallets/troubles?type=AttemptFailed", &troubles)
	require.Len(t, troubles.Troubles, 1)
	get(t, base+"/v1/projects/wallets/troubles?stage=discovery", &troubles)
	assert.Len(t, troubles.Troubles, 4, "the return is plan's: the visit the owner sent back")

	var timeline wire.Timeline
	get(t, base+"/v1/projects/wallets/works/w-1", &timeline)
	require.Len(t, timeline.Troubles, 5)
	assert.Equal(t, "AttemptFailed", timeline.Troubles[0].Type, "a timeline's troubles in the order they occurred")
	assert.Equal(t, []wire.Assessment{}, timeline.Assessments)
}

// put sends body to u as bearer, and decodes the answer into v.
func put(t *testing.T, u, bearer string, body any, v any) int {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, _ := http.NewRequest(http.MethodPut, u, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if v != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
	}
	return resp.StatusCode
}

func TestServer_MembersAssessAVisitAndAWork_EachKeepingOneCurrentAssessment(t *testing.T) {
	base, sync := troubled(t)
	before, err := sync.Streams(context.Background())
	require.NoError(t, err)
	visit := base + "/v1/projects/wallets/works/w-1/visits/discovery/1/assessment"
	work := base + "/v1/projects/wallets/works/w-1/assessment"

	var a wire.Assessment
	require.Equal(t, http.StatusOK, put(t, visit, token, wire.Assess{Verdict: wire.VerdictGood}, &a))
	assert.Equal(t, "jordi", a.Member)
	assert.False(t, a.AssessedAt.IsZero())
	require.Equal(t, http.StatusOK, put(t, visit, token, wire.Assess{Verdict: wire.VerdictWentWrong, Reason: "  discovery never read the logs  "}, &a))
	assert.Equal(t, wire.Assessment{Work: "w-1", Stage: "discovery", Visit: 1, Member: "jordi", Verdict: wire.VerdictWentWrong, Reason: "discovery never read the logs", AssessedAt: a.AssessedAt}, a)
	require.Equal(t, http.StatusOK, put(t, visit, ana, wire.Assess{Verdict: wire.VerdictGood}, nil))
	require.Equal(t, http.StatusOK, put(t, work, ana, wire.Assess{Verdict: wire.VerdictWentWrong, Reason: "took three visits"}, nil))

	var timeline wire.Timeline
	get(t, base+"/v1/projects/wallets/works/w-1", &timeline)
	assert.Equal(t, []string{"ana w-1 went-wrong", "ana w-1/discovery/1 good", "jordi w-1/discovery/1 went-wrong"}, summary(timeline.Assessments),
		"one current assessment per member and target, newest first")

	var all wire.Assessments
	get(t, base+"/v1/projects/wallets/assessments", &all)
	assert.Equal(t, summary(timeline.Assessments), summary(all.Assessments))
	for query, want := range map[string][]string{
		"?verdict=went-wrong": {"ana w-1 went-wrong", "jordi w-1/discovery/1 went-wrong"},
		"?stage=discovery":    {"ana w-1/discovery/1 good", "jordi w-1/discovery/1 went-wrong"},
		"?member=jordi":       {"jordi w-1/discovery/1 went-wrong"},
		"?work=w-2":           {},
	} {
		get(t, base+"/v1/projects/wallets/assessments"+query, &all)
		assert.Equal(t, want, summary(all.Assessments), query)
	}

	after, err := sync.Streams(context.Background())
	require.NoError(t, err)
	assert.Equal(t, before, after, "assessing changes nothing a sync sees")
}

func summary(as []wire.Assessment) []string {
	out := []string{}
	for _, a := range as {
		target := a.Work
		if a.Stage != "" {
			target += "/" + a.Stage + "/" + string(rune('0'+a.Visit))
		}
		out = append(out, a.Member+" "+target+" "+a.Verdict)
	}
	return out
}

func TestServer_RefusesAnAssessment_ThatJudgesNothingItHolds(t *testing.T) {
	base, _ := troubled(t)
	visit := func(stage, n string) string {
		return base + "/v1/projects/wallets/works/w-1/visits/" + stage + "/" + n + "/assessment"
	}
	for name, c := range map[string]struct {
		url, bearer string
		body        any
		status      int
		kind        string
	}{
		"went wrong without a reason": {visit("discovery", "1"), token, wire.Assess{Verdict: wire.VerdictWentWrong, Reason: "  "}, 400, wire.ProblemInvalid},
		"good with a reason":          {visit("discovery", "1"), token, wire.Assess{Verdict: wire.VerdictGood, Reason: "fine"}, 400, wire.ProblemInvalid},
		"another verdict":             {visit("discovery", "1"), token, wire.Assess{Verdict: "meh"}, 400, wire.ProblemInvalid},
		"unknown fields":              {visit("discovery", "1"), token, map[string]string{"verdict": "good", "score": "5"}, 400, wire.ProblemInvalid},
		"a reason too long":           {visit("discovery", "1"), token, wire.Assess{Verdict: wire.VerdictWentWrong, Reason: string(bytes.Repeat([]byte("x"), 4001))}, 400, wire.ProblemInvalid},
		"a visit that isn't a number": {visit("discovery", "one"), token, wire.Assess{Verdict: wire.VerdictGood}, 400, wire.ProblemInvalid},
		"a visit the work never made": {visit("discovery", "9"), token, wire.Assess{Verdict: wire.VerdictGood}, 404, wire.ProblemNotFound},
		"a stage the work never made": {visit("review", "1"), token, wire.Assess{Verdict: wire.VerdictGood}, 404, wire.ProblemNotFound},
		"a work the server holds not": {base + "/v1/projects/wallets/works/w-9/assessment", token, wire.Assess{Verdict: wire.VerdictGood}, 404, wire.ProblemNotFound},
		"the project secret":          {visit("discovery", "1"), secret, wire.Assess{Verdict: wire.VerdictGood}, 403, wire.ProblemForbidden},
		"another organisation":        {visit("discovery", "1"), bob, wire.Assess{Verdict: wire.VerdictGood}, 404, wire.ProblemNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			var p wire.Problem
			assert.Equal(t, c.status, put(t, c.url, c.bearer, c.body, &p), p.Message)
			assert.Equal(t, c.kind, p.Error)
		})
	}
	var all wire.Assessments
	get(t, base+"/v1/projects/wallets/assessments", &all)
	assert.Empty(t, all.Assessments)

	var a wire.Assessment
	assert.Equal(t, http.StatusOK, put(t, visit("fixer", "1"), token, wire.Assess{Verdict: wire.VerdictGood}, &a), "the fixer's visits are stage visits too")
	assert.Equal(t, http.StatusOK, put(t, visit("discovery", "3"), token, wire.Assess{Verdict: wire.VerdictGood}, &a), "so is the visit a return started")
	assert.Equal(t, http.StatusOK, put(t, visit("plan", "1"), token, wire.Assess{Verdict: wire.VerdictGood}, &a), "and the visit it returned from")

	var p wire.Problem
	assert.Equal(t, http.StatusNotFound, put(t, base+"/v1/projects/wallets/works/w/visits/1-discovery/1/assessment", token, wire.Assess{Verdict: wire.VerdictGood}, &p),
		"w-1's assignments aren't w's")
	var timeline wire.Timeline
	get(t, base+"/v1/projects/wallets/works/w", &timeline)
	assert.Empty(t, timeline.Troubles)
	assert.Len(t, timeline.Events, 1)
}

func TestStore_KeepsEveryAssessment_AndBacksThemUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.db")
	store, err := historyserver.Open(path)
	require.NoError(t, err)
	ctx := context.Background()
	_, err = store.AppendEvents(ctx, "wallets", wire.Events{Events: []string{started, entered}})
	require.NoError(t, err)
	for _, verdict := range []wire.Assess{{Verdict: wire.VerdictGood}, {Verdict: wire.VerdictWentWrong, Reason: "on second thought"}} {
		_, err := store.Assess(ctx, "wallets", "jordi", historyserver.Target{Work: "w-1", Stage: "discovery", Visit: 1}, verdict)
		require.NoError(t, err)
	}
	require.NoError(t, store.Backup(ctx, filepath.Join(dir, "backup.db")))
	require.NoError(t, store.Close())
	assert.ErrorContains(t, historyserver.ExecForTest(path, `UPDATE assessments SET verdict = 'good'`), "assessments are append-only")
	assert.ErrorContains(t, historyserver.ExecForTest(path, `DELETE FROM assessments`), "assessments are append-only")

	backup, err := historyserver.Open(filepath.Join(dir, "backup.db"))
	require.NoError(t, err)
	defer backup.Close()
	current, err := backup.Assessments(ctx, "wallets", historyserver.AssessmentFilter{})
	require.NoError(t, err)
	require.Len(t, current.Assessments, 1)
	assert.Equal(t, "on second thought", current.Assessments[0].Reason)
	assert.Equal(t, 2, historyserver.CountForTest(t, filepath.Join(dir, "backup.db"), `SELECT count(*) FROM assessments`), "the replaced one is kept too")
}
