package agentsessions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func TestDrivingMarker(t *testing.T) {
	s := Open(t.TempDir())

	driving, err := s.IsDriving("session/../1")
	require.NoError(t, err)
	assert.False(t, driving)

	require.NoError(t, s.MarkDriving("session/../1"))
	driving, err = s.IsDriving("session/../1")
	require.NoError(t, err)
	assert.True(t, driving)

	require.NoError(t, s.ClearDriving("session/../1"))
	require.NoError(t, s.ClearDriving("session/../1"), "clearing twice is fine")
	driving, _ = s.IsDriving("session/../1")
	assert.False(t, driving)
}

func TestAgentStartMarker(t *testing.T) {
	s := Open(t.TempDir())
	id := domain.AssignmentIDFor(must(domain.NewWorkID("w1")), domain.StageImplement, domain.FirstVisit())

	_, found, err := s.AgentStart("agent-1")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, s.RecordAgentStart("agent-1", AgentStart{Assignment: id, Version: 3}))
	start, found, err := s.AgentStart("agent-1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, AgentStart{Assignment: id, Version: 3}, start)

	require.NoError(t, s.ForgetAgent("agent-1"))
	_, found, _ = s.AgentStart("agent-1")
	assert.False(t, found)
}

func TestEmptyIDsAreRejected(t *testing.T) {
	s := Open(t.TempDir())
	require.Error(t, s.MarkDriving(""))
	require.Error(t, s.RecordAgentStart("", AgentStart{}))
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
