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

var identity = git(map[string]string{"user.name": "Jordi", "user.email": "j@x.io"})

func TestResolve_Owner(t *testing.T) {
	c, err := Resolve(env(nil), identity, true)
	require.NoError(t, err)

	assert.Equal(t, eventlog.Actor{Kind: eventlog.ActorOwner, Name: "Jordi <j@x.io>"}, c.Actor())
	owner, err := c.RequireOwner("approve", "gf approve")
	require.NoError(t, err)
	assert.Equal(t, "Jordi", owner.Name())
}

func TestResolve_Agent_IsRefusedOwnerCommands(t *testing.T) {
	c, err := Resolve(env(map[string]string{EnvActor: "agent"}), git(nil), true)
	require.NoError(t, err)

	assert.Equal(t, eventlog.ActorAgent, c.Actor().Kind)
	_, err = c.RequireOwner("approve", "gf approve")
	var ownerOnly *OwnerOnlyError
	require.ErrorAs(t, err, &ownerOnly)
	assert.Equal(t, "gf approve", ownerOnly.Command)
	assert.False(t, ownerOnly.NoTerminal)
}

func TestResolve_OwnerWithoutTerminal_IsRefusedOwnerCommands(t *testing.T) {
	c, err := Resolve(env(nil), identity, false)
	require.NoError(t, err)

	assert.Equal(t, eventlog.ActorOwner, c.Actor().Kind, "agent commands still run as the owner")
	_, err = c.RequireOwner("approve", "gf approve")
	var ownerOnly *OwnerOnlyError
	require.ErrorAs(t, err, &ownerOnly)
	assert.True(t, ownerOnly.NoTerminal)
}

func TestResolve_OwnerWithoutGitName_Fails(t *testing.T) {
	_, err := Resolve(env(nil), git(nil), true)
	require.ErrorIs(t, err, ErrNoOwnerIdentity)
}
