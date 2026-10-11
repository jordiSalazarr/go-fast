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
		if stage == domain.StageFixer {
			continue // its agent comes with gf status' fixer view
		}
		body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", stage.String()+".md"))
		require.NoError(t, err)
		names := make([]string, 0, len(stage.RequiredChecks()))
		for _, c := range stage.RequiredChecks() {
			names = append(names, "`"+c.String()+"`")
		}
		assert.Contains(t, string(body), "`gf submit --passed` runs these checks itself: "+strings.Join(names, ", ")+".", stage.String())
	}
}
