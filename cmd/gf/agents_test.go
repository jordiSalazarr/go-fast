package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// Each stage agent names the exit checks gf runs for its stage, as the
// domain lists them.
func TestStageAgents_NameTheirExitChecks(t *testing.T) {
	for _, stage := range domain.Stages() {
		body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", stage.String()+".md"))
		require.NoError(t, err)
		names := make([]string, 0, len(stage.RequiredChecks()))
		for _, c := range stage.RequiredChecks() {
			names = append(names, "`"+c.String()+"`")
		}
		assert.Contains(t, string(body), "`gf submit --passed` runs these checks itself: "+strings.Join(names, ", ")+".", stage.String())
	}
}

// Every stage agent but the fixer reads the fixer's artifact first on a
// visit after a rework; the fixer chooses its route.
func TestStageAgents_KnowAboutRework(t *testing.T) {
	for _, stage := range domain.Stages() {
		body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", stage.String()+".md"))
		require.NoError(t, err)
		if stage == domain.StageFixer {
			assert.Contains(t, string(body), "gf submit --passed --route <target>")
			continue
		}
		assert.Contains(t, string(body), "`current.reworkOf.fixerArtifact`", stage.String())
	}
}
