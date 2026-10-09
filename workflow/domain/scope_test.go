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
}

func TestEachStageHasItsWriteScope(t *testing.T) {
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageDiscovery.WriteScope())
	assert.Equal(t, domain.WriteScopeTestsAndArtifacts, domain.StageSpecify.WriteScope())
	assert.Equal(t, domain.WriteScopeAnything, domain.StageImplement.WriteScope())
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageReview.WriteScope())
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageIntegrationTesting.WriteScope())
}

func TestWriteScopeAllows(t *testing.T) {
	paths := []string{
		".gofast/works/w1/discovery-v1.md", // 0: this work's artifact
		".gofast/works/w1/notes/extra.md",  // 1: inside this work's artifact dir
		".gofast/works/w0/discovery-v1.md", // 2: another work's artifact
		".gofast/events.jsonl",             // 3
		".gofast/events.lock",              // 4
		".gofast/gf.log",                   // 5
		".gofast/.gitignore",               // 6
		".gofast/runtime/driving/x",        // 7
		".gofast/works/w1",                 // 8: the artifact dir itself, not a file in it
		"login/login_test.go",              // 9
		"login/testdata/broken.json",       // 10
		"testdata/top.txt",                 // 11
		"login/login.go",                   // 12
		"login/test_helpers.go",            // 13
		"login/testdata",                   // 14: a file named testdata, not a dir
		".gofastish/notes.md",              // 15: not .gofast/
	}
	want := map[domain.WriteScope][]bool{
		//                                  0     1     2      3      4      5      6      7      8      9      10     11     12     13     14     15
		domain.WriteScopeArtifacts:         {true, true, false, false, false, false, false, false, false, false, false, false, false, false, false, false},
		domain.WriteScopeTestsAndArtifacts: {true, true, false, false, false, false, false, false, false, true, true, true, false, false, false, false},
		domain.WriteScopeAnything:          {true, true, false, false, false, false, false, false, false, true, true, true, true, true, true, true},
	}
	for scope, allowed := range want {
		for i, p := range paths {
			assert.Equalf(t, allowed[i], scope.Allows(repoPath(t, p), workID), "%s allows %s", scope, p)
		}
	}
}

func TestWriteScopeAllows_ZeroValuesAllowNothing(t *testing.T) {
	assert.False(t, domain.WriteScope{}.Allows(repoPath(t, "a.go"), workID))
	assert.False(t, domain.WriteScopeAnything.Allows(domain.RepoPath{}, workID))
	assert.False(t, domain.WriteScopeAnything.Allows(repoPath(t, "a.go"), domain.WorkID{}))
}
