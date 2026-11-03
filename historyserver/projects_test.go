package historyserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// post sends body (nil for none) to u as bearer, and decodes the answer
// into v.
func post(t *testing.T, u, bearer string, body any, v any) int {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if v != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
	}
	return resp.StatusCode
}

func TestServer_AMemberCreatesAProject_AndTheServerMakesItsSecret(t *testing.T) {
	_, _, base := serve(t, secret)
	ctx := context.Background()

	var created wire.ProjectSecret
	require.Equal(t, http.StatusCreated, post(t, base+"/v1/projects", ana, wire.NewProject{ID: "payments"}, &created))
	assert.Equal(t, "payments", created.Project)
	assert.True(t, strings.HasPrefix(created.Secret, "gfps_"), created.Secret)
	assert.Len(t, created.Secret, len("gfps_")+64)

	sync := clientOf(t, base, "payments", created.Secret)
	_, err := sync.SendEvents(ctx, wire.Events{Events: []string{started}})
	require.NoError(t, err, "the secret syncs the new project")
	_, err = clientOf(t, base, "wallets", created.Secret).Streams(ctx)
	assert.Equal(t, wire.ProblemUnauthorized, refusal(t, err).Error, "and no other")

	var me wire.Me
	get(t, base+"/v1/me", &me)
	require.Len(t, me.Projects, 2, "jordi sees the project ana created: it's their organisation's")
	assert.Equal(t, "payments", me.Projects[0].ID)
	assert.Equal(t, "ana", me.Projects[0].CreatedBy)
	resp, _ := fetch(t, base+"/v1/projects/payments/works", bob)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "bob's organisation has no project payments")
}

func TestServer_RefusesAProject_ItCantCreate(t *testing.T) {
	_, _, base := serve(t, secret)
	for name, c := range map[string]struct {
		bearer string
		body   any
		status int
		kind   string
	}{
		"an id taken in this organisation":    {token, wire.NewProject{ID: "wallets"}, 409, wire.ProblemTaken},
		"an id taken in another organisation": {token, wire.NewProject{ID: "ledger"}, 409, wire.ProblemTaken},
		"an id with capitals":                 {token, wire.NewProject{ID: "Payments"}, 400, wire.ProblemInvalid},
		"no id":                               {token, wire.NewProject{}, 400, wire.ProblemInvalid},
		"the project secret":                  {secret, wire.NewProject{ID: "payments"}, 403, wire.ProblemForbidden},
		"nobody":                              {"wrong", wire.NewProject{ID: "payments"}, 401, wire.ProblemUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			var p wire.Problem
			assert.Equal(t, c.status, post(t, base+"/v1/projects", c.bearer, c.body, &p), p.Message)
			assert.Equal(t, c.kind, p.Error)
		})
	}
}

func TestServer_RotatingASecret_RetiresTheOldOne(t *testing.T) {
	client, _, base := serve(t, secret)
	ctx := context.Background()
	_, err := client.SendEvents(ctx, wire.Events{Events: []string{started}})
	require.NoError(t, err)

	var rotated wire.ProjectSecret
	require.Equal(t, http.StatusOK, post(t, base+"/v1/projects/wallets/secret", ana, nil, &rotated))
	assert.NotEqual(t, secret, rotated.Secret)
	_, err = client.Streams(ctx)
	assert.Equal(t, wire.ProblemUnauthorized, refusal(t, err).Error, "the old secret syncs no more")
	heads, err := clientOf(t, base, "wallets", rotated.Secret).Streams(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, heads["work-w-1"].Version, "the history stays")

	var me wire.Me
	get(t, base+"/v1/me", &me)
	assert.Equal(t, "ana", me.Projects[0].SecretBy)

	var p wire.Problem
	assert.Equal(t, http.StatusNotFound, post(t, base+"/v1/projects/wallets/secret", bob, nil, &p), "another organisation can't rotate it")
	assert.Equal(t, http.StatusForbidden, post(t, base+"/v1/projects/wallets/secret", rotated.Secret, nil, &p), "nor can the secret itself")
}
