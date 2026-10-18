package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func TestParseSkillLoad(t *testing.T) {
	for _, l := range []domain.SkillLoad{domain.SkillInvoked, domain.SkillPreloaded, {}} {
		got, err := domain.ParseSkillLoad(l.Name())
		require.NoError(t, err)
		assert.Equal(t, l, got)
	}
	assert.Equal(t, "invoked", domain.SkillLoad{}.String(), "loads recorded before gf told them apart were invoked")
	_, err := domain.ParseSkillLoad("guessed")
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}
