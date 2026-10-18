package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

const repoRoot = "/repo"

func repoPath(t *testing.T, p string) domain.RepoPath {
	t.Helper()
	rp, err := domain.NewRepoPath(repoRoot, p)
	require.NoError(t, err)
	return rp
}

func TestNewRepoPath(t *testing.T) {
	cases := map[string]string{
		"/repo/a/b.go":              "a/b.go",
		"a/./b.go":                  "a/b.go",
		"a/../b.go":                 "b.go",
		"/repo/x/../.gofast/gf.log": ".gofast/gf.log",
	}
	for in, want := range cases {
		assert.Equal(t, want, repoPath(t, in).String(), in)
	}

	for _, outside := range []string{"/elsewhere/a.go", "../a.go", "/repo/../a.go", "/repository/a.go"} {
		_, err := domain.NewRepoPath(repoRoot, outside)
		require.ErrorIs(t, err, domain.ErrPathOutsideRepository, outside)
	}
	for _, invalid := range []string{"", "/repo", "."} {
		_, err := domain.NewRepoPath(repoRoot, invalid)
		require.ErrorIs(t, err, domain.ErrInvalidRepoPath, invalid)
	}
}

func TestArtifactPaths(t *testing.T) {
	assert.Equal(t, ".gofast/works/w1", domain.ArtifactDir(workID).String())
	assert.Equal(t, ".gofast/works/w1/specify-v1.md", domain.ArtifactFor(workID, domain.StageSpecify, visit1).String())
	assert.Equal(t, ".gofast/works/w1/design-v2.json", domain.DesignFor(workID, must(domain.NewVisit(2))).String())
	assert.Equal(t, ".gofast/works/w1/nfrs.proposed-v1.json", domain.NFRProposalsFor(workID, visit1).String())
	assert.Equal(t, ".gofast/works/w1/event-model-v1.json", domain.EventModelFor(workID, visit1).String())
	assert.Equal(t, ".gofast/works/w1/grill-v2.json", domain.GrillFor(workID, must(domain.NewVisit(2))).String())
}

func TestEachStageHasItsWriteScope(t *testing.T) {
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageEventModelling.WriteScope())
	assert.Equal(t, domain.WriteScopeArtifactsAndDecision, domain.StageGrill.WriteScope())
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageDiscovery.WriteScope())
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageDesign.WriteScope())
	assert.Equal(t, domain.WriteScopeTestsSkeletonsAndArtifacts, domain.StageSpecify.WriteScope())
	assert.Equal(t, domain.WriteScopeTestsAndArtifacts, domain.StageCharacterize.WriteScope())
	assert.Equal(t, domain.WriteScopeAnything, domain.StageImplement.WriteScope())
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageReview.WriteScope())
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageIntegrationTesting.WriteScope())
}

func TestWriteScopeAllows(t *testing.T) {
	paths := []string{
		".gofast/works/w1/discovery-v1.md",            // 0: this work's artifact
		".gofast/works/w1/notes/extra.md",             // 1: inside this work's artifact dir
		".gofast/works/w0/discovery-v1.md",            // 2: another work's artifact
		".gofast/events.jsonl",                        // 3
		".gofast/events.lock",                         // 4
		".gofast/gf.log",                              // 5
		".gofast/.gitignore",                          // 6
		".gofast/runtime/driving/x",                   // 7
		".gofast/works/w1",                            // 8: the artifact dir itself, not a file in it
		"login/login_test.go",                         // 9
		"login/testdata/broken.json",                  // 10
		"testdata/top.txt",                            // 11
		"login/login.go",                              // 12
		"login/test_helpers.go",                       // 13
		"login/testdata",                              // 14: a file named testdata, not a dir
		".gofastish/notes.md",                         // 15: not .gofast/
		"docs/decisions/README.md",                    // 16: the index gf generates
		"docs/decisions/2026-10-12-w1-wallets.md",     // 17: this work's decision document
		"docs/decisions/2026-10-12-w0-wallets.md",     // 18: another work's
		"docs/decisions/notes.md",                     // 19
		"docs/decisions/old/2026-10-12-w1-wallets.md", // 20: not directly in the directory
		"docs/decisions/2026-10-12-w1-Wallets_v2.md",  // 21: not a slug
		"docs/decisions/12-10-2026-w1-wallets.md",     // 22: not a date first
	}
	want := map[domain.WriteScope][]bool{
		//                                  0     1     2      3      4      5      6      7      8      9      10     11     12     13     14     15     16     17     18     19     20     21     22
		domain.WriteScopeArtifacts:                  {true, true, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false},
		domain.WriteScopeArtifactsAndDecision:       {true, true, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, true, false, false, false, false, false},
		domain.WriteScopeTestsAndArtifacts:          {true, true, false, false, false, false, false, false, false, true, true, true, false, false, false, false, false, false, false, false, false, false, false},
		domain.WriteScopeTestsSkeletonsAndArtifacts: {true, true, false, false, false, false, false, false, false, true, true, true, true, true, false, false, false, false, false, false, false, false, false},
		domain.WriteScopeAnything:                   {true, true, false, false, false, false, false, false, false, true, true, true, true, true, true, true, false, true, true, true, true, true, true},
	}
	for scope, allowed := range want {
		for i, p := range paths {
			assert.Equalf(t, allowed[i], scope.Allows(repoPath(t, p), workID, decisions), "%s allows %s", scope, p)
		}
	}
}

// Grill also writes the repository's glossaries and ADRs, as the
// domain-modeling skill does while it interviews the owner; never the
// decisions index, nor gofast's own files.
func TestGrillsScope_AllowsGlossariesAndADRs(t *testing.T) {
	scope := domain.StageGrill.WriteScope()
	assert.Equal(t, "artifacts, the decision document, glossaries and ADRs", scope.String())
	for _, p := range []string{"GLOSSARY.md", "GLOSSARY-MAP.md", "src/x/GLOSSARY.md", "src/ordering/GLOSSARY-MAP.md",
		"docs/adr/0001-x.md", "billing/docs/adr/0002-y.md", "docs/adr/archive/0003-z.md"} {
		assert.True(t, scope.Allows(repoPath(t, p), workID, decisions), p)
	}
	adrDecisions := must(domain.NewDecisionsDir(repoPath(t, "docs/adr")))
	for _, p := range []string{"docs/adr/x.go", "GLOSSARY.txt", "glossary.md", "docs/adrs/x.md", "adr/0001-x.md", "mydocs/adr/0001-x.md",
		"docs/decisions/README.md", ".gofast/GLOSSARY.md", ".gofast/docs/adr/0001-x.md", "src/x.go"} {
		assert.False(t, scope.Allows(repoPath(t, p), workID, decisions), p)
	}
	assert.False(t, scope.Allows(repoPath(t, "docs/adr/README.md"), workID, adrDecisions), "the index, even in docs/adr")
	for _, stage := range domain.Stages() {
		if stage.WriteScope() == domain.WriteScopeArtifactsAndDecision || stage.WriteScope().CanChangeCode() {
			continue
		}
		assert.False(t, stage.WriteScope().Allows(repoPath(t, "GLOSSARY.md"), workID, decisions), "%s may not write glossaries", stage)
		assert.False(t, stage.WriteScope().Allows(repoPath(t, "docs/adr/0001-x.md"), workID, decisions), "%s may not write ADRs", stage)
	}
}

// Specify may add skeletons: any non-test Go file outside gofast's own
// files (skeletons-only checks they are new and do nothing). Characterize
// keeps tests and artifacts only.
func TestSpecifysScope_AllowsSkeletons_CharacterizesDoesnt(t *testing.T) {
	for _, p := range []string{"domain/wallet_freeze.go", "wallet.go", "domain/wallet_test.go", "domain/testdata/x.json"} {
		assert.True(t, domain.StageSpecify.WriteScope().Allows(repoPath(t, p), workID, decisions), p)
	}
	for _, p := range []string{"go.mod", "domain/wallet.md", ".gofast/x.go", "docs/decisions/README.md"} {
		assert.False(t, domain.StageSpecify.WriteScope().Allows(repoPath(t, p), workID, decisions), p)
	}
	assert.False(t, domain.StageCharacterize.WriteScope().Allows(repoPath(t, "domain/wallet_freeze.go"), workID, decisions))
	assert.True(t, domain.StageCharacterize.WriteScope().Allows(repoPath(t, "domain/wallet_test.go"), workID, decisions))
}

// The decisions directory is the owner's to choose, but never inside
// gofast's own files.
func TestDecisionsDir(t *testing.T) {
	dir := must(domain.NewDecisionsDir(repoPath(t, "adr")))
	assert.Equal(t, "adr/README.md", dir.Index().String())
	assert.True(t, dir.IsDecisionOf(repoPath(t, "adr/2026-10-12-w1-wallets.md"), workID))
	assert.False(t, dir.IsDecisionOf(repoPath(t, "docs/decisions/2026-10-12-w1-wallets.md"), workID))
	assert.Equal(t, "docs/decisions", domain.DefaultDecisionsDir().Path().String())
	for _, p := range []string{".gofast", ".gofast/decisions"} {
		_, err := domain.NewDecisionsDir(repoPath(t, p))
		assert.ErrorIs(t, err, domain.ErrInvalidRepoPath, p)
	}
}

func TestWriteScopeAllows_ZeroValuesAllowNothing(t *testing.T) {
	assert.False(t, domain.WriteScope{}.Allows(repoPath(t, "a.go"), workID, decisions))
	assert.False(t, domain.WriteScopeAnything.Allows(domain.RepoPath{}, workID, decisions))
	assert.False(t, domain.WriteScopeAnything.Allows(repoPath(t, "a.go"), domain.WorkID{}, decisions))
}

func TestWriteScopeViolations_OfEachScope(t *testing.T) {
	changed := domain.NewChangedFiles(
		repoPath(t, "login/login.go"),
		repoPath(t, "login/login_test.go"),
		repoPath(t, "login/testdata/case.json"),
		repoPath(t, ".gofast/works/w1/specify-v1.md"),
		repoPath(t, ".gofast/works/w0/specify-v1.md"),
		repoPath(t, ".gofast/events.jsonl"),
	)
	cases := map[domain.WriteScope][]string{
		domain.WriteScopeArtifacts:                  {".gofast/events.jsonl", ".gofast/works/w0/specify-v1.md", "login/login.go", "login/login_test.go", "login/testdata/case.json"},
		domain.WriteScopeTestsAndArtifacts:          {".gofast/events.jsonl", ".gofast/works/w0/specify-v1.md", "login/login.go"},
		domain.WriteScopeTestsSkeletonsAndArtifacts: {".gofast/events.jsonl", ".gofast/works/w0/specify-v1.md"},
		domain.WriteScopeAnything:                   {".gofast/events.jsonl", ".gofast/works/w0/specify-v1.md"},
	}
	for scope, want := range cases {
		var got []string
		for _, p := range scope.Violations(changed, workID, decisions) {
			got = append(got, p.String())
		}
		assert.Equal(t, want, got, scope.String())
	}
}

func TestWriteScopeViolations_NothingChanged_NoViolations(t *testing.T) {
	assert.Empty(t, domain.WriteScopeArtifacts.Violations(domain.ChangedFiles{}, workID, decisions))
}

func TestNewChangedFiles_IsASortedSet(t *testing.T) {
	changed := domain.NewChangedFiles(repoPath(t, "b.go"), repoPath(t, "a.go"), repoPath(t, "b.go"), domain.RepoPath{})

	assert.Equal(t, []domain.RepoPath{repoPath(t, "a.go"), repoPath(t, "b.go")}, changed.Paths())
}
