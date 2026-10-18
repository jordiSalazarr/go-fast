package guardagentactions

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
)

const root = "/repo"

// Given / when / then on the pure decision:
//
//	given facts about the workflow and whether the session drives,
//	when the hook input arrives,
//	then this output.

func toolCall(agentType, tool string, input map[string]string) claudehooks.Input {
	raw, _ := json.Marshal(input)
	in := claudehooks.Input{SessionID: "s1", CWD: root, HookEventName: "PreToolUse", ToolName: tool, ToolInput: raw, AgentType: agentType}
	if agentType != "" {
		in.AgentID = "agent-1"
	}
	return in
}

func bash(command string) claudehooks.Input {
	return toolCall("gofast:implement", "Bash", map[string]string{"command": command})
}

func write(agentType, path string) claudehooks.Input {
	return toolCall(agentType, "Write", map[string]string{"file_path": path, "content": "x"})
}

func on(stage domain.Stage) stageFacts {
	return onStage(WorkID, stage, Visit1, domain.DefaultDecisionsDir())
}

func denied(reason string) claudehooks.Output { return claudehooks.Deny(reason) }

var allowed = claudehooks.Nothing()

func TestRule1_OwnerOnlyCommandsAreDeniedInAnySegment(t *testing.T) {
	approveReason := "Only the owner can approve. Ask the owner to run `gf approve` in their own terminal."
	for _, command := range []string{
		"gf approve",
		"/usr/local/bin/gf approve",
		"~/go/bin/gf --dir . approve",
		"gf --dir=/repo approve",
		"go test ./... && gf approve",
		"go vet ./...; gf approve",
		"echo y | gf approve",
		"true || gf approve",
		"echo $(gf approve)",
		"echo \"$(gf approve)\"",
		"echo `gf approve`",
		"(gf approve)",
		"env gf approve",
		"env -i HOME=/tmp gf approve",
		"FOO=1 gf approve",
		"sudo gf approve",
		"go run ./cmd/gf approve",
		"go run -race ./cmd/gf --dir . approve",
		"go run github.com/jordiSalazarr/go-fast/cmd/gf@latest approve",
		"bash -c 'gf approve'",
		"sh -lc \"cd /repo && gf approve\"",
		"eval gf approve",
		"if true; then gf approve; fi",
		"gf status\ngf approve",
		// The program word is only known at run time.
		"env -i PATH=/usr/bin:/bin \"$(command -v gf)\" approve",
		"\"$(command -v gf)\" approve",
		"$(command -v gf) approve",
		"`which gf` approve",
		"$G approve",
		"${G} --dir . approve",
		"G=gf; $G approve",
		"\"\" approve",
		"bash -c '$G approve'",
	} {
		t.Run(command, func(t *testing.T) {
			assert.Equal(t, denied(approveReason), decide(bash(command), on(domain.StageSpecify), true, root))
		})
	}
}

func TestRule1_EachOwnerOnlyCommandHasItsOwnReason(t *testing.T) {
	cases := map[string]string{
		"gf reject 'no'":                         "Only the owner can reject a submission. Ask the owner to run `gf reject \"<feedback>\"` in their own terminal.",
		"gf extend 2":                            "Only the owner can extend a budget. Ask the owner to run `gf extend <attempts>` in their own terminal.",
		"gf rework-extend 1":                     "Only the owner can allow more rework. Ask the owner to run `gf rework-extend <n>` in their own terminal.",
		"cd x && GF=1 gf rework-extend 2":        "Only the owner can allow more rework. Ask the owner to run `gf rework-extend <n>` in their own terminal.",
		"gf stress skip 'docs only'":             "Only the owner can override the stress-testing decision. Review decides it with `gf submit --passed --stress <include|skip> \"<reason>\"`; otherwise ask the owner to run `gf stress <include|skip> \"<reason>\"` in their own terminal.",
		"gf abandon 'give up'":                   "Only the owner can abandon work. Ask the owner to run `gf abandon \"<reason>\"` in their own terminal.",
		"gf return specify 'specs miss refunds'": "Only the owner can send work back to an earlier stage. Ask the owner to run `gf return <stage> \"<reason>\"` in their own terminal.",
		"gf log accept":                          "Only the owner can accept a changed event log. Ask the owner to run `gf log accept` in their own terminal.",
		"gf nfrs adopt --yes":                    "Only the owner can adopt NFR proposals. Write them in the design's nfrs.proposed-v<visit>.json and ask the owner to run `gf nfrs adopt` in their own terminal.",
	}
	for command, reason := range cases {
		assert.Equal(t, denied(reason), decide(bash(command), on(domain.StageSpecify), true, root), command)
	}
}

func TestRule1_AgentCommandsAndMentionsAreAllowed(t *testing.T) {
	for _, command := range []string{
		"gf status --json",
		"gf submit --passed",
		"gf submit --failed 'approve flow broken'",
		"gf submit --passed --stress include 'new query per request'",
		"k6 run .gofast/works/w1/stress/wallet.js",
		"go test ./...",
		"git commit -m 'gf approve wording'",
		"grep -r approve .",
		"echo gfapprove",
		"git log --oneline",
	} {
		t.Run(command, func(t *testing.T) {
			assert.Equal(t, allowed, decide(bash(command), on(domain.StageImplement), true, root))
		})
	}
}

// Probes the guard does not catch: gf refuses them, since they run without a
// terminal (see cmd/gf's end-to-end tests).
func TestRule1_ProbesLeftToGf(t *testing.T) {
	for _, command := range []string{
		"cp \"$(command -v gf)\" /tmp/x && /tmp/x approve",
		"/tmp/x approve",
		"python3 -c \"import os; os.system('gf approve')\"",
		"echo approve | xargs gf",
		"$EDITOR approve.md",
	} {
		t.Run(command, func(t *testing.T) {
			assert.Equal(t, allowed, decide(bash(command), on(domain.StageImplement), true, root))
		})
	}
}

func TestRule2_GofastFilesAndGF_ACTORAreOffLimits(t *testing.T) {
	for _, command := range []string{
		"cat .gofast/events.jsonl",
		"echo '{}' >> .gofast/events.jsonl",
		"rm .gofast/events.lock",
		"tail .gofast/gf.log",
		"GF_ACTOR=owner gf status",
		"unset GF_ACTOR",
		"echo '{}' >> .gofast/event?.jsonl",
		"rm -rf .gofast/runtime",
		"cat .gofast/runtime/log.sum",
		"ls .gofast/gf*",
	} {
		t.Run(command, func(t *testing.T) {
			out := decide(bash(command), on(domain.StageImplement), true, root)
			assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision)
			assert.Contains(t, out.HookSpecificOutput.PermissionDecisionReason, "Agents may not touch")
		})
	}
}

func TestRule3_ClearingTheEnvironmentIsDenied(t *testing.T) {
	for _, command := range []string{
		"env -i PATH=/usr/bin:/bin gf status",
		"env -i go test ./...",
		"env --ignore-environment make",
		"env -u HOME go test ./...",
		"env -uHOME go test ./...",
		"env --unset=HOME go test ./...",
		"env -iv true",
		"env - true",
		"/usr/bin/env -i true",
		"env FOO=1 -i true",
		"go vet ./... && env -i true",
		"sudo env -i true",
		"unset HOME",
		"unset -v PATH; go test ./...",
		"bash -c 'unset X'",
		"if true; then unset X; fi",
	} {
		t.Run(command, func(t *testing.T) {
			assert.Equal(t, denied(clearEnvironmentReason), decide(bash(command), on(domain.StageImplement), true, root))
		})
	}
}

func TestRule3_OtherUsesOfEnvAreAllowed(t *testing.T) {
	for _, command := range []string{
		"env",
		"env GOFLAGS=-race go test ./...",
		"env -C /tmp ls",
		"/usr/bin/env python3 script.py",
		"go env GOPATH",
		"printenv HOME",
		"grep -r unset .",
		"echo 'env -i' > notes.txt",
		"git commit -m 'unset the flag'",
	} {
		t.Run(command, func(t *testing.T) {
			assert.Equal(t, allowed, decide(bash(command), on(domain.StageImplement), true, root))
		})
	}
}

func TestRules1To3_ApplyEverywhere_EvenOutsideADriveAndForOtherAgents(t *testing.T) {
	mainNotDriving := toolCall("", "Bash", map[string]string{"command": "gf approve"})
	explore := toolCall("Explore", "Bash", map[string]string{"command": "cat .gofast/events.jsonl"})

	assert.Equal(t, "deny", decide(mainNotDriving, noActiveWork(), false, root).HookSpecificOutput.PermissionDecision)
	assert.Equal(t, "deny", decide(explore, noActiveWork(), false, root).HookSpecificOutput.PermissionDecision)
}

func TestRules1To3_FailClosed_WhenTheStateOrInputCannotBeRead(t *testing.T) {
	assert.Equal(t, "deny", decide(bash("gf approve"), unreadable(), true, root).HookSpecificOutput.PermissionDecision)

	broken := claudehooks.Input{SessionID: "s1", ToolName: "Bash", ToolInput: json.RawMessage(`"not an object"`)}
	out := decide(broken, unreadable(), false, root)
	assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision)
}

func TestRule4_WriteScopeOfEachStage(t *testing.T) {
	cases := []struct {
		stage domain.Stage
		path  string
		want  claudehooks.Output
	}{
		{domain.StageDiscovery, "/repo/.gofast/works/w1/discovery-v1.md", allowed},
		{domain.StageDiscovery, "/repo/login/login.go",
			denied("During 'discovery' you may only write the stage artifact `.gofast/works/w1/discovery-v1.md`.")},
		{domain.StageDiscovery, "/repo/login/login_test.go",
			denied("During 'discovery' you may only write the stage artifact `.gofast/works/w1/discovery-v1.md`.")},
		{domain.StageSpecify, "/repo/login/login_test.go", allowed},
		{domain.StageSpecify, "/repo/login/testdata/case.json", allowed},
		{domain.StageSpecify, "/repo/.gofast/works/w1/specify-v1.md", allowed},
		{domain.StageSpecify, "/repo/login/login.go", allowed}, // a skeleton: skeletons-only checks it on submit
		{domain.StageSpecify, "/repo/go.mod",
			denied("During 'specify' you may only write tests, new skeleton files (non-test .go files that only declare) and the stage artifact `.gofast/works/w1/specify-v1.md`.")},
		{domain.StageCharacterize, "/repo/login/login.go",
			denied("During 'characterize' you may only write tests and the stage artifact `.gofast/works/w1/characterize-v1.md`.")},
		{domain.StageImplement, "/repo/login/login.go", allowed},
		{domain.StageImplement, "/repo/.gofast/events.jsonl",
			denied("During 'implement' you may write anything except gofast's own files in .gofast/ (.gofast/events.jsonl); the stage artifact is `.gofast/works/w1/implement-v1.md`.")},
		{domain.StageImplement, "/repo/.gofast/works/w0/implement-v1.md",
			denied("During 'implement' you may write anything except gofast's own files in .gofast/ (.gofast/works/w0/implement-v1.md); the stage artifact is `.gofast/works/w1/implement-v1.md`.")},
		{domain.StageImplement, "/tmp/scratch.go",
			denied("During 'implement' you may only write inside the repository; /tmp/scratch.go is outside it.")},
		{domain.StageReview, "/repo/login/login.go",
			denied("During 'review' you may only write the stage artifact `.gofast/works/w1/review-v1.md`.")},
		{domain.StageIntegrationTesting, "/repo/.gofast/works/w1/integration-testing-v1.md", allowed},
	}
	for _, c := range cases {
		t.Run(c.stage.String()+" "+c.path, func(t *testing.T) {
			assert.Equal(t, c.want, decide(write("gofast:"+c.stage.String(), c.path), on(c.stage), false, root))
		})
	}
}

func TestRule4_TheCheckConfigurationIsTheOwners_OnEveryStage(t *testing.T) {
	for _, stage := range domain.Stages() {
		out := decide(write("gofast:"+stage.String(), "/repo/.gofast/checks.json"), on(stage), false, root)

		assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision, stage.String())
	}
}

// Stress testing writes its k6 scripts in the artifact directory, and
// nothing else.
func TestRule4_StressTestingWritesItsScriptsAndArtifact(t *testing.T) {
	agent := "gofast:stress-testing"
	on := on(domain.StageStressTesting)
	assert.Equal(t, allowed, decide(write(agent, "/repo/.gofast/works/w1/stress/wallet-latency.js"), on, false, root))
	assert.Equal(t, allowed, decide(write(agent, "/repo/.gofast/works/w1/stress-testing-v1.md"), on, false, root))
	assert.Equal(t, denied("During 'stress-testing' you may only write the stage artifact `.gofast/works/w1/stress-testing-v1.md` and k6 scripts in `.gofast/works/w1/stress/`."),
		decide(write(agent, "/repo/server/wallet.go"), on, false, root))
}

// Once the work has a baseline, its target NFRs' scripts are frozen: the
// guard denies writing them, for fast feedback; stress-scripts-valid is the
// authority.
func TestRule4_TheBaselinesScriptsAreFrozen(t *testing.T) {
	latency := Must(domain.NewNFRID("wallet-latency"))
	frozen := Must(domain.NewFrozenScript(latency, domain.StressScriptFor(WorkID, latency), domain.HashOf([]byte("export default function () {}"))))
	for _, stage := range []domain.Stage{domain.StageStressTesting, domain.StageMeasure} {
		facts := on(stage)
		facts.frozen = []domain.FrozenScript{frozen}
		agent := "gofast:" + stage.String()
		edit := toolCall(agent, "Edit", map[string]string{"file_path": "/repo/.gofast/works/w1/stress/wallet-latency.js", "old_string": "a", "new_string": "b"})

		want := denied(".gofast/works/w1/stress/wallet-latency.js changed since measure recorded the baseline: restore it. " +
			"A different script needs a new baseline: abandon the work and start again with the new script.")
		assert.Equal(t, want, decide(write(agent, "/repo/.gofast/works/w1/stress/wallet-latency.js"), facts, false, root), stage.String())
		assert.Equal(t, want, decide(edit, facts, false, root), stage.String())
		assert.Equal(t, allowed, decide(write(agent, "/repo/.gofast/works/w1/stress/wallet-burst.js"), facts, false, root),
			"%s: a script for a non-target NFR", stage)
	}
}

// The NFRs are the owner's: no stage agent may change their targets, not even
// the stress-testing agent, whose scripts live in its artifact directory.
func TestRule4_TheNFRsAreTheOwners_OnEveryStage(t *testing.T) {
	for _, stage := range domain.Stages() {
		for _, path := range []string{"/repo/.gofast/nfrs.json", ".gofast/nfrs.json"} {
			out := decide(write("gofast:"+stage.String(), path), on(stage), false, root)

			require.NotNil(t, out.HookSpecificOutput, "%s %s", stage, path)
			assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision, "%s %s", stage, path)
		}
	}
	out := decide(write("", "/repo/.gofast/nfrs.json"), on(domain.StageImplement), true, root)
	require.NotNil(t, out.HookSpecificOutput, "the main session while it drives")
	assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision)
}

func TestRule4_RelativePathsResolveAgainstCwd(t *testing.T) {
	in := write("gofast:review", "login/login_test.go")
	in.CWD = "/repo"

	out := decide(in, on(domain.StageReview), false, root)

	assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision)
}

func TestRule4_EditAndNotebookEditAreGuardedToo(t *testing.T) {
	edit := toolCall("gofast:review", "Edit", map[string]string{"file_path": "/repo/a.go", "old_string": "a", "new_string": "b"})
	notebook := toolCall("gofast:review", "NotebookEdit", map[string]string{"notebook_path": "/repo/a.ipynb", "new_source": "x"})

	assert.Equal(t, "deny", decide(edit, on(domain.StageReview), false, root).HookSpecificOutput.PermissionDecision)
	assert.Equal(t, "deny", decide(notebook, on(domain.StageReview), false, root).HookSpecificOutput.PermissionDecision)
}

func TestRule4_AppliesToTheMainSessionOnlyWhileDriving(t *testing.T) {
	main := write("", "/repo/login/login.go")

	assert.Equal(t, "deny", decide(main, on(domain.StageReview), true, root).HookSpecificOutput.PermissionDecision)
	assert.Equal(t, allowed, decide(main, on(domain.StageReview), false, root))
}

// Grill runs in the main session: while it drives, the session may write
// grill's artifacts, this work's decision document, glossaries and ADRs,
// nothing else.
func TestRule4_GrillWritesItsArtifactsTheWorksDecisionDocumentGlossariesAndADRs_FromTheMainSession(t *testing.T) {
	grillScope := denied("During 'grill' you may only write the stage artifacts in `.gofast/works/w1/`, this work's decision document, `docs/decisions/<YYYY-MM-DD>-w1-<slug>.md`, glossaries (`GLOSSARY.md`, `GLOSSARY-MAP.md`) and ADRs (`.md` files under a `docs/adr/` directory).")
	for path, want := range map[string]claudehooks.Output{
		"/repo/.gofast/works/w1/grill-v1.md":            allowed,
		"/repo/.gofast/works/w1/grill-v1.json":          allowed,
		"/repo/docs/decisions/2026-10-12-w1-wallets.md": allowed,
		"/repo/GLOSSARY.md":                             allowed,
		"/repo/src/billing/GLOSSARY.md":                 allowed,
		"/repo/docs/adr/0001-event-sourced-wallets.md":  allowed,
		"/repo/docs/adr/0001-x.go":                      grillScope,
		"/repo/GLOSSARY.txt":                            grillScope,
		"/repo/docs/decisions/2026-10-12-w0-wallets.md": grillScope,
		"/repo/docs/decisions/notes.md":                 grillScope,
		"/repo/login/login.go":                          grillScope,
		"/repo/docs/decisions/README.md":                denied("docs/decisions/README.md is generated by gf from the accepted grills; nobody else writes it."),
	} {
		assert.Equal(t, want, decide(write("", path), on(domain.StageGrill), true, root), path)
	}
}

// The index of the decisions directory is gf's, on every stage, even
// implement's.
func TestRule4_TheDecisionsIndexIsGfs_OnEveryStage(t *testing.T) {
	for _, stage := range []domain.Stage{domain.StageImplement, domain.StageSpecify, domain.StageGrill} {
		out := decide(write("gofast:"+stage.String(), "/repo/docs/decisions/README.md"), on(stage), true, root)
		assert.Equal(t, denied("docs/decisions/README.md is generated by gf from the accepted grills; nobody else writes it."), out, stage.String())
	}
	assert.Equal(t, allowed, decide(write("gofast:implement", "/repo/docs/decisions/2026-10-12-w0-x.md"), on(domain.StageImplement), true, root))
}

func TestRule4_DoesNotApplyToNonGofastAgents(t *testing.T) {
	explore := write("Explore", "/repo/login/login.go")

	assert.Equal(t, allowed, decide(explore, on(domain.StageReview), true, root))
}

func TestRule4_FailsOpenWithAMessage_WhenTheStateCannotBeRead(t *testing.T) {
	out := decide(write("gofast:review", "/repo/login/login.go"), unreadable(), false, root)

	assert.Equal(t, claudehooks.Tell("gofast could not read the workflow state, so this write was not checked against the stage's write scope. Details are in .gofast/gf.log."), out)
}

func TestRule4_WithoutActiveWork_StageAgentsMayNotWrite(t *testing.T) {
	out := decide(write("gofast:implement", "/repo/a.go"), noActiveWork(), false, root)

	assert.Equal(t, denied("There is no active gofast work, so there is no stage to write for. Run /gofast:drive to start one."), out)
}

func TestOtherToolsAreNotGuarded(t *testing.T) {
	read := toolCall("gofast:review", "Read", map[string]string{"file_path": "/repo/.gofast/events.jsonl"})

	assert.Equal(t, allowed, decide(read, on(domain.StageReview), true, root))
}

// On a refactor, implement may change only the accepted plan's areas, and
// add new files under the directories they name.
func TestRule4_RefactorImplement_StaysInThePlansAreas(t *testing.T) {
	plan := domain.NewRefactorPlan(domain.RefactorPlanValues{Areas: []string{"internal/wallet/**", "internal/ledger/posting.go"}})
	writes, err := domain.WritesOf(domain.StageImplement, Must(domain.PathFor(domain.WorkTypeRefactor)), WorkID,
		domain.ScopeFacts{Decisions: domain.DefaultDecisionsDir(), Plan: &plan})
	require.NoError(t, err)
	existing := map[string]bool{"internal/wallet/service.go": true, "internal/ledger/posting.go": true, "internal/ledger/journal.go": true, "cmd/server/main.go": true}
	facts := on(domain.StageImplement).withWrites(writes, func(p domain.RepoPath) bool { return existing[p.String()] })
	agent := "gofast:implement"

	assert.Equal(t, allowed, decide(write(agent, "/repo/internal/wallet/service.go"), facts, false, root))
	assert.Equal(t, allowed, decide(write(agent, "/repo/internal/wallet/slices/post/handler.go"), facts, false, root))
	assert.Equal(t, allowed, decide(write(agent, "/repo/internal/ledger/posting_split.go"), facts, false, root), "a new file under a named directory")
	assert.Equal(t, allowed, decide(write(agent, "/repo/.gofast/works/w1/implement-v1.md"), facts, false, root))
	assert.Equal(t, denied("During 'implement' of a refactor you may only change the accepted plan's areas (internal/wallet/**, internal/ledger/posting.go) and add new files under their directories; internal/ledger/journal.go is outside them. If the plan is wrong, say so in your artifact and submit --failed."),
		decide(write(agent, "/repo/internal/ledger/journal.go"), facts, false, root))
	assert.Equal(t, "deny", decide(write(agent, "/repo/cmd/server/new.go"), facts, false, root).HookSpecificOutput.PermissionDecision)
}

func loadSkill(agent, name string) claudehooks.Input {
	input, _ := json.Marshal(map[string]string{"skill": name, "args": ""})
	return claudehooks.Input{SessionID: "s1", CWD: "/repo", HookEventName: "PreToolUse", AgentID: "a1", AgentType: agent, ToolName: "Skill", ToolInput: input}
}

func readFile(agent, path string) claudehooks.Input {
	input, _ := json.Marshal(map[string]string{"file_path": path})
	return claudehooks.Input{SessionID: "s1", CWD: "/repo", HookEventName: "PreToolUse", AgentID: "a1", AgentType: agent, ToolName: "Read", ToolInput: input}
}

// Rule 5: a stage's catalog skill is denied until the work has a diagnosis.
func TestRule5_CatalogSkillsUnlockAfterTheDiagnosis(t *testing.T) {
	catalog, problems := domain.NewSkillCatalog(map[string][]string{"optimize": {"concurrency"}})
	require.Empty(t, problems)
	agent := "gofast:optimize"
	locked := on(domain.StageOptimize).withSkills(catalog, domain.Diagnosis{Stage: domain.StageOptimize})
	unlocked := on(domain.StageOptimize).withSkills(catalog, domain.Diagnosis{Stage: domain.StageOptimize, Baseline: true, MeasureAccepted: true})
	reason := "'concurrency' unlocks after the diagnosis: write `## Bottleneck` with evidence first."

	assert.Equal(t, denied(reason), decide(loadSkill(agent, "gofast:concurrency"), locked, false, root))
	assert.Equal(t, denied(reason), decide(loadSkill(agent, "concurrency"), locked, false, root))
	assert.Equal(t, allowed, decide(loadSkill(agent, "gofast:concurrency"), unlocked, false, root))
	assert.Equal(t, allowed, decide(loadSkill(agent, "gofast:grill"), locked, false, root), "not in the catalog")
	assert.Equal(t, allowed, decide(loadSkill(agent, "other:concurrency"), locked, false, root), "another plugin's skill")

	assert.Equal(t, denied(reason), decide(readFile(agent, "/home/u/.claude/plugins/gofast/skills/concurrency/SKILL.md"), locked, false, root))
	assert.Equal(t, allowed, decide(readFile(agent, "/home/u/.claude/plugins/gofast/skills/concurrency/SKILL.md"), unlocked, false, root))
	assert.Equal(t, allowed, decide(readFile(agent, "/repo/internal/wallet/service.go"), locked, false, root))
}
