package historyserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/historyserver"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Skill improvements: wallets and payments are projects of acme, and
// ledger of globex. wallets' history recorded aggregate-design at aggV1.

const paymentsSecret = "p4yments"

var (
	aggV1 = hash("aggregate-design v1 manifest")
	aggV2 = hash("aggregate-design v2 manifest")
)

// improvements is a server whose wallets history loaded aggregate-design
// at aggV1 in w-1's design visit; it returns the server's URL and store.
func improvements(t *testing.T) (string, *historyserver.Store) {
	t.Helper()
	client, store, base := serve(t, secret)
	historyserver.CreateProjectForTest(t, store, "acme", "payments", paymentsSecret)
	_, err := client.SendEvents(context.Background(), wire.Events{Events: []string{
		line("si-w1", "work-w-1", 1, "WorkStarted", `{"workId":"w-1","workType":"new-feature","description":"wallets","branch":"main"}`),
		line("si-l1", "observations-w-1", 1, "SkillLoaded", `{"workId":"w-1","stage":"design","visit":1,"skill":"aggregate-design","load":"preloaded","sha256":"`+aggV1+`"}`),
	}})
	require.NoError(t, err)
	return base, store
}

// call sends body (nil for none) to u as bearer, and returns the status
// and the answer's bytes.
func call(t *testing.T, method, u, bearer string, body any) (int, []byte) {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req, err := http.NewRequest(method, u, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, out
}

// decoded is the answer as T; the zero T when it isn't one.
func decoded[T any](raw []byte) T {
	var v T
	_ = json.Unmarshal(raw, &v)
	return v
}

func improvementsURL(base, project string) string {
	return base + "/v1/projects/" + project + "/skill-improvements"
}

// proposedImprovement is jordi's improvement id of aggregate-design aggV1
// in wallets.
func proposedImprovement(t *testing.T, base, id string) {
	t.Helper()
	status, body := call(t, http.MethodPost, improvementsURL(base, "wallets"), token,
		wire.ProposeSkillImprovement{ID: id, Skill: "aggregate-design", Improves: aggV1, Text: "Name the business intent behind an update."})
	require.Equal(t, http.StatusCreated, status, "propose %s: %s", id, body)
}

// approvedImprovement is id, proposed and approved by ana with scope.
func approvedImprovement(t *testing.T, base, id, scope string) {
	t.Helper()
	proposedImprovement(t, base, id)
	status, body := call(t, http.MethodPost, improvementsURL(base, "wallets")+"/"+id+"/approve", ana, wire.ApproveSkillImprovement{Scope: scope, Revision: 1})
	require.Equal(t, http.StatusOK, status, "approve %s: %s", id, body)
}

// approvedIDs are the ids project's secret reads as approved.
func approvedIDs(t *testing.T, base, project, projectSecret string) []string {
	t.Helper()
	status, body := call(t, http.MethodGet, improvementsURL(base, project)+"/approved", projectSecret, nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
	ids := []string{}
	for _, i := range decoded[wire.ApprovedSkillImprovements](body).Improvements {
		ids = append(ids, i.ID)
	}
	return ids
}

// gofast:scenario A1
// Rule: a member writes an improvement of a recorded instruction version, from a went-wrong assessment; it is proposed at revision 1.
func TestProposeSkillImprovement_AMemberWritesAnImprovementOfARecordedVersion(t *testing.T) {
	// Given
	base, _ := improvements(t)

	// When
	status, body := call(t, http.MethodPost, improvementsURL(base, "wallets"), token, wire.ProposeSkillImprovement{
		ID: "si-1", Skill: "aggregate-design", Improves: aggV1, Text: "  Name the business intent behind an update.  ",
		FromAssessment: &wire.AssessmentRef{Work: "w-1", Stage: "design", Visit: 1},
	})

	// Then
	require.Equal(t, http.StatusCreated, status, "%s", body)
	got := decoded[wire.SkillImprovement](body)
	assert.Equal(t, wire.SkillImprovement{ID: "si-1", Project: "wallets", Skill: "aggregate-design", Improves: aggV1,
		Text: "Name the business intent behind an update.", Revision: 1, State: wire.ImprovementProposed, Author: "jordi",
		FromAssessment: &wire.AssessmentRef{Work: "w-1", Stage: "design", Visit: 1}}, got)
}

// gofast:scenario A1
// Rule: an improvement names an instruction version its project has recorded.
func TestProposeSkillImprovement_RefusesAVersionTheProjectNeverRecorded(t *testing.T) {
	// Given
	base, _ := improvements(t)

	// When
	_, body := call(t, http.MethodPost, improvementsURL(base, "wallets"), token,
		wire.ProposeSkillImprovement{ID: "si-1", Skill: "aggregate-design", Improves: aggV2, Text: "Name the intent."})

	// Then
	assert.Equal(t, wire.ProblemUnknownInstructionVersion, decoded[wire.Problem](body).Error, "%s", body)
}

// gofast:scenario A1
// Rule: a proposed improvement's text can be edited.
func TestReviseSkillImprovement_AMemberEditsAProposedImprovement(t *testing.T) {
	// Given
	base, _ := improvements(t)
	proposedImprovement(t, base, "si-1")

	// When
	status, body := call(t, http.MethodPost, improvementsURL(base, "wallets")+"/si-1/revise", ana, wire.ReviseSkillImprovement{Text: "Name the business intent, never UpdateX."})

	// Then
	require.Equal(t, http.StatusOK, status, "%s", body)
	got := decoded[wire.SkillImprovement](body)
	assert.Equal(t, "Name the business intent, never UpdateX.", got.Text)
	assert.Equal(t, 2, got.Revision)
	assert.Equal(t, wire.ImprovementProposed, got.State)
}

// gofast:scenario A2
// Rule: any member, its author included, approves an improvement for this project.
func TestApproveSkillImprovement_ItsAuthorApprovesItForThisProject(t *testing.T) {
	// Given
	base, _ := improvements(t)
	proposedImprovement(t, base, "si-1") // written by jordi

	// When
	status, body := call(t, http.MethodPost, improvementsURL(base, "wallets")+"/si-1/approve", token, wire.ApproveSkillImprovement{Scope: wire.ScopeProject, Revision: 1})

	// Then
	require.Equal(t, http.StatusOK, status, "%s", body)
	got := decoded[wire.SkillImprovement](body)
	assert.Equal(t, wire.ImprovementApproved, got.State)
	assert.Equal(t, wire.ScopeProject, got.Scope)
	assert.Equal(t, "jordi", got.ApprovedBy)
}

// gofast:scenario A2
// Rule: an approved improvement's text can no longer change.
func TestReviseSkillImprovement_IsRefusedOnceApproved(t *testing.T) {
	// Given
	base, _ := improvements(t)
	approvedImprovement(t, base, "si-1", wire.ScopeProject)

	// When
	_, body := call(t, http.MethodPost, improvementsURL(base, "wallets")+"/si-1/revise", token, wire.ReviseSkillImprovement{Text: "Something else."})

	// Then
	assert.Equal(t, wire.ProblemImprovementTextFixed, decoded[wire.Problem](body).Error, "%s", body)
}

// gofast:scenario A2
// Rule: an approval approves the revision its approver read.
func TestApproveSkillImprovement_IsRefusedWhenRevisedSinceRead(t *testing.T) {
	// Given
	base, _ := improvements(t)
	proposedImprovement(t, base, "si-1")
	status, body := call(t, http.MethodPost, improvementsURL(base, "wallets")+"/si-1/revise", token, wire.ReviseSkillImprovement{Text: "Revised."})
	require.Equal(t, http.StatusOK, status, "%s", body)

	// When
	_, body = call(t, http.MethodPost, improvementsURL(base, "wallets")+"/si-1/approve", ana, wire.ApproveSkillImprovement{Scope: wire.ScopeProject, Revision: 1})

	// Then
	assert.Equal(t, wire.ProblemImprovementRevisedSinceRead, decoded[wire.Problem](body).Error, "%s", body)
}

// gofast:scenario A3
// Rule: an all-projects approval reaches every project of the organisation, including one created later, and no other organisation's.
func TestApprovedSkillImprovements_AnAllProjectsApprovalReachesEveryProjectOfTheOrganisation(t *testing.T) {
	// Given
	base, store := improvements(t)
	approvedImprovement(t, base, "si-1", wire.ScopeAllProjects)
	historyserver.CreateProjectForTest(t, store, "acme", "created-later", "l4ter")

	// When
	later := approvedIDs(t, base, "created-later", "l4ter")

	// Then
	assert.Equal(t, []string{"si-1"}, later)
	assert.Equal(t, []string{"si-1"}, approvedIDs(t, base, "payments", paymentsSecret))
	assert.Equal(t, []string{}, approvedIDs(t, base, "ledger", "l3dger"), "globex is another organisation")
}

// gofast:scenario A4
// Rule: a withdrawn improvement is no longer served to any clone.
func TestWithdrawSkillImprovement_AWithdrawnImprovementIsNoLongerServed(t *testing.T) {
	// Given
	base, _ := improvements(t)
	approvedImprovement(t, base, "si-1", wire.ScopeAllProjects)

	// When
	status, body := call(t, http.MethodPost, improvementsURL(base, "wallets")+"/si-1/withdraw", token, struct{}{})

	// Then
	require.Equal(t, http.StatusOK, status, "%s", body)
	assert.Equal(t, wire.ImprovementWithdrawn, decoded[wire.SkillImprovement](body).State)
	assert.Equal(t, []string{}, approvedIDs(t, base, "wallets", secret))
	assert.Equal(t, []string{}, approvedIDs(t, base, "payments", paymentsSecret))
}

// gofast:scenario A12
// Rule: an improvement approved for one project only is never served to another project's clones.
func TestApprovedSkillImprovements_AProjectOnlyApprovalIsNotServedToAnotherProject(t *testing.T) {
	// Given
	base, _ := improvements(t)
	approvedImprovement(t, base, "si-1", wire.ScopeProject)

	// When
	payments := approvedIDs(t, base, "payments", paymentsSecret)

	// Then
	assert.Equal(t, []string{}, payments)
	assert.Equal(t, []string{"si-1"}, approvedIDs(t, base, "wallets", secret))
}

// gofast:scenario A15
// Rule: the project secret reads its project's approved, non-withdrawn improvements, and only those, in approval order.
func TestApprovedSkillImprovements_TheSecretReadsOnlyItsProjectsApprovedImprovements(t *testing.T) {
	// Given
	base, _ := improvements(t)
	approvedImprovement(t, base, "si-2", wire.ScopeProject)
	approvedImprovement(t, base, "si-1", wire.ScopeAllProjects)
	proposedImprovement(t, base, "si-proposed")
	approvedImprovement(t, base, "si-withdrawn", wire.ScopeProject)
	status, body := call(t, http.MethodPost, improvementsURL(base, "wallets")+"/si-withdrawn/withdraw", token, struct{}{})
	require.Equal(t, http.StatusOK, status, "%s", body)

	// When
	status, body = call(t, http.MethodGet, improvementsURL(base, "wallets")+"/approved", secret, nil)

	// Then
	require.Equal(t, http.StatusOK, status, "%s", body)
	got := decoded[wire.ApprovedSkillImprovements](body).Improvements
	require.Len(t, got, 2, "%s", body)
	assert.Equal(t, []string{"si-2", "si-1"}, []string{got[0].ID, got[1].ID}, "in approval order")
	assert.Equal(t, wire.ApprovedSkillImprovement{ID: "si-2", Skill: "aggregate-design", Improves: aggV1,
		Text: "Name the business intent behind an update.", Revision: 1, Scope: wire.ScopeProject, ApprovedAt: got[0].ApprovedAt}, got[0])
	assert.False(t, got[0].ApprovedAt.IsZero())
	assert.Equal(t, wire.ScopeAllProjects, got[1].Scope)
}

// gofast:scenario A15
// Rule: the approved list is the secret's: a member's token is refused.
func TestApprovedSkillImprovements_RefusesAMembersToken(t *testing.T) {
	// Given
	base, _ := improvements(t)
	approvedImprovement(t, base, "si-1", wire.ScopeProject)

	// When
	status, body := call(t, http.MethodGet, improvementsURL(base, "wallets")+"/approved", token, nil)

	// Then
	assert.Equal(t, http.StatusForbidden, status, "%s", body)
	assert.Equal(t, wire.ProblemForbidden, decoded[wire.Problem](body).Error)
}

// gofast:scenario A15
// Rule: every other read with the project secret is still refused, the improvements list and an improvement included.
func TestApprovedSkillImprovements_TheSecretStillReadsNothingElse(t *testing.T) {
	// Given
	base, _ := improvements(t)
	approvedImprovement(t, base, "si-1", wire.ScopeProject)

	for _, path := range []string{"", "/si-1", "?skill=aggregate-design"} {
		// When
		status, body := call(t, http.MethodGet, improvementsURL(base, "wallets")+path, secret, nil)

		// Then
		assert.Equal(t, http.StatusForbidden, status, "%s: %s", path, body)
	}
}

// landing is an event of project's skill-improvements stream, synced by
// owner.
func landing(id string, version int, owner, eventType, payload string) string {
	clock++
	return fmt.Sprintf(`{"id":%q,"position":%d,"stream":"skill-improvements","version":%d,"type":%q,"schema":1,"occurredAt":"2026-11-04T09:%02d:%02dZ","actor":{"kind":"owner","name":%q},"payload":%s}`,
		id, clock, version, eventType, clock/60, clock%60, owner, payload)
}

// gofast:scenario A17
// Rule: per improvement, the viewer shows the projects and owners that applied, declined or could not apply it.
func TestSkillImprovementsOfOrganisation_ShowsWhereAnImprovementLanded(t *testing.T) {
	// Given
	base, _ := improvements(t)
	approvedImprovement(t, base, "si-1", wire.ScopeAllProjects)
	_, err := clientOf(t, base, "wallets", secret).SendEvents(context.Background(), wire.Events{Events: []string{
		landing("lw1", 1, "Jordi", "skill-improvement.applied", `{"improvementId":"si-1","skill":"aggregate-design","before":"`+aggV1+`","after":"`+aggV2+`","summary":"names the intent"}`),
		landing("lw2", 2, "Jordi", "skill-improvement.declined", `{"improvementId":"si-1","skill":"aggregate-design","version":"`+aggV1+`"}`),
	}})
	require.NoError(t, err)
	_, err = clientOf(t, base, "payments", paymentsSecret).SendEvents(context.Background(), wire.Events{Events: []string{
		landing("lp1", 1, "Ana", "skill-improvement.not-applied", `{"improvementId":"si-1","skill":"aggregate-design","version":"`+aggV1+`","cause":"conflict","reason":"the skill says the opposite"}`),
	}})
	require.NoError(t, err)

	// When
	status, body := call(t, http.MethodGet, improvementsURL(base, "wallets")+"/si-1", ana, nil)

	// Then
	require.Equal(t, http.StatusOK, status, "%s", body)
	type seen struct{ project, owner, outcome, reason, before, after string }
	var got []seen
	for _, l := range decoded[wire.SkillImprovement](body).Landings {
		got = append(got, seen{l.Project, l.Owner, l.Outcome, l.Reason, l.Before, l.After})
	}
	assert.ElementsMatch(t, []seen{
		{"wallets", "Jordi", wire.LandingApplied, "", aggV1, aggV2},
		{"wallets", "Jordi", wire.LandingDeclined, "", "", ""},
		{"payments", "Ana", wire.LandingNotApplied, "the skill says the opposite", "", ""},
	}, got)
}

// gofast:scenario A17
// Rule: per stage visit, the timeline shows the improvements the loaded instruction version contained.
func TestServer_ATimelineShowsTheImprovementsEachVisitsInstructionVersionContained(t *testing.T) {
	// Given
	client, _, base := serve(t, secret)
	_, err := client.SendEvents(context.Background(), wire.Events{Events: []string{
		line("tw1", "work-w-1", 1, "WorkStarted", `{"workId":"w-1","workType":"new-feature","description":"wallets","branch":"main"}`),
		line("tl1", "observations-w-1", 1, "SkillLoaded", `{"workId":"w-1","stage":"design","visit":1,"skill":"aggregate-design","load":"preloaded","sha256":"`+aggV2+`","improvements":["si-1","si-2"]}`),
	}})
	require.NoError(t, err)

	// When
	var timeline struct {
		Visits []struct {
			Stage        string `json:"stage"`
			Instructions []struct {
				Name         string   `json:"name"`
				Improvements []string `json:"improvements"`
			} `json:"instructions"`
		} `json:"visits"`
	}
	get(t, base+"/v1/projects/wallets/works/w-1", &timeline)

	// Then
	require.Len(t, timeline.Visits, 1)
	require.Len(t, timeline.Visits[0].Instructions, 1)
	assert.Equal(t, "aggregate-design", timeline.Visits[0].Instructions[0].Name)
	assert.Equal(t, []string{"si-1", "si-2"}, timeline.Visits[0].Instructions[0].Improvements)
}
