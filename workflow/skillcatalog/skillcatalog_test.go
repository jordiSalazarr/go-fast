package skillcatalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/skillcatalog"
)

func TestLoad_ReportsUnknownStagesAndMissingSkills(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "skills", "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skills", "catalog.json"),
		[]byte(`{"stages": {"optimize": ["memory", "postgres"], "optimise": ["memory"]}}`), 0o644))

	catalog, problems, err := skillcatalog.Load(root)

	require.NoError(t, err)
	assert.Equal(t, []string{"memory", "postgres"}, catalog.Skills(domain.StageOptimize))
	require.Len(t, problems, 2)
	assert.Contains(t, problems[0], `stage "optimise" is not a gofast stage`)
	assert.Equal(t, `stage optimize lists skill "postgres", which has no directory skills/postgres/`, problems[1])
}

func TestLoad_WithoutACatalog_LocksNothing(t *testing.T) {
	catalog, problems, err := skillcatalog.Load(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, problems)
	assert.Empty(t, catalog.Stages())
}

func TestLoad_RejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "skills"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skills", "catalog.json"), []byte(`{"stage": {}}`), 0o644))
	_, _, err := skillcatalog.Load(root)
	assert.ErrorContains(t, err, `unknown field "stage"`)
}
