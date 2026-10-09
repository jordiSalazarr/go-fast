package caller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

func env(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

func git(values map[string]string) func(string) (string, error) {
	return func(k string) (string, error) { return values[k], nil }
}

func TestResolve_Owner(t *testing.T) {
	c, err := Resolve(env(nil), git(map[string]string{"user.name": "Jordi", "user.email": "j@x.io"}))
	require.NoError(t, err)

	assert.Equal(t, eventlog.Actor{Kind: eventlog.ActorOwner, Name: "Jordi <j@x.io>"}, c.Actor())
	owner, err := c.RequireOwner("approve", "gf approve")
	require.NoError(t, err)
	assert.Equal(t, "Jordi", owner.Name())
}

func TestResolve_Agent_IsRefusedOwnerCommands(t *testing.T) {
	c, err := Resolve(env(map[string]string{EnvActor: "agent"}), git(nil))
	require.NoError(t, err)

	assert.Equal(t, eventlog.ActorAgent, c.Actor().Kind)
	_, err = c.RequireOwner("approve", "gf approve")
	var ownerOnly *OwnerOnlyError
	require.ErrorAs(t, err, &ownerOnly)
	assert.Equal(t, "gf approve", ownerOnly.Command)
}

func TestResolve_OwnerWithoutGitName_Fails(t *testing.T) {
	_, err := Resolve(env(nil), git(nil))
	require.ErrorIs(t, err, ErrNoOwnerIdentity)
}
