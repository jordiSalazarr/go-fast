package exitchecks_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

func writeNFRs(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(exitchecks.NFRsPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

const walletNFRs = `{
  "nfrs": [
    {
      "id": "wallet-transaction-latency",
      "description": "Posting a transaction to a wallet stays fast under concurrent load",
      "areas": ["internal/wallet/**", "internal/ledger/**"],
      "operation": "POST /wallets/{id}/transactions",
      "load": { "vus": 50, "duration": "60s" },
      "targets": { "p95": "150ms", "p99": "400ms", "maxErrorRate": 0.001, "minRps": 200 }
    },
    {
      "id": "user-lookup",
      "areas": ["internal/users/**"],
      "operation": "GET /users/{id}",
      "load": { "vus": 5, "duration": "10s" },
      "targets": { "p99": "50ms" }
    }
  ]
}`

func TestLoadNFRs_WithoutAFile_IsNoNFRs(t *testing.T) {
	nfrs, err := exitchecks.LoadNFRs(t.TempDir())

	require.NoError(t, err)
	assert.Empty(t, nfrs)
}

func TestLoadNFRs_ReadsEveryNFR(t *testing.T) {
	root := t.TempDir()
	writeNFRs(t, root, walletNFRs)

	nfrs, err := exitchecks.LoadNFRs(root)

	require.NoError(t, err)
	require.Len(t, nfrs, 2)
	wallet := nfrs[0]
	assert.Equal(t, "wallet-transaction-latency", wallet.ID().String())
	assert.Equal(t, "Posting a transaction to a wallet stays fast under concurrent load", wallet.Description())
	assert.Equal(t, "POST /wallets/{id}/transactions", wallet.Operation().String())
	assert.Equal(t, 50, wallet.Load().VUs())
	assert.Equal(t, 60*time.Second, wallet.Load().Duration())
	assert.Equal(t, "p95 ≤ 150ms, p99 ≤ 400ms, error rate ≤ 0.1%, rps ≥ 200", wallet.Targets().Describe())
	var globs []string
	for _, a := range wallet.Areas() {
		globs = append(globs, a.String())
	}
	assert.Equal(t, []string{"internal/wallet/**", "internal/ledger/**"}, globs)
	assert.Equal(t, "p99 ≤ 50ms", nfrs[1].Targets().Describe(), "targets are optional one by one")
	assert.True(t, wallet.Covers(domain.NewChangedFiles(Must(domain.NewRepoPath(root, "internal/ledger/entry.go")))))
}

func TestLoadNFRs_RejectsWhatItCannotUse(t *testing.T) {
	nfr := func(fields string) string {
		return `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "1ms"}` + fields + `}]}`
	}
	for name, content := range map[string]string{
		"not JSON":           `{`,
		"unknown top field":  `{"nfrs": [], "slo": []}`,
		"unknown nfr field":  nfr(`, "owner": "me"`),
		"unknown target":     `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {"p50": "1ms"}}]}`,
		"invalid duration":   `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "a minute"}, "targets": {"p95": "1ms"}}]}`,
		"invalid p95":        `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "fast"}}]}`,
		"no target":          `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {}}]}`,
		"no targets":         `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}}]}`,
		"no load":            `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "targets": {"p95": "1ms"}}]}`,
		"zero vus":           `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 0, "duration": "1s"}, "targets": {"p95": "1ms"}}]}`,
		"no areas":           `{"nfrs": [{"id": "x", "areas": [], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "1ms"}}]}`,
		"absolute area":      `{"nfrs": [{"id": "x", "areas": ["/a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "1ms"}}]}`,
		"bad operation":      `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "/a", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "1ms"}}]}`,
		"bad id":             `{"nfrs": [{"id": "a b", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "1ms"}}]}`,
		"error rate above 1": `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {"maxErrorRate": 2}}]}`,
		"duplicate id": `{"nfrs": [
			{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "1ms"}},
			{"id": "x", "areas": ["b/**"], "operation": "GET /b", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "1ms"}}]}`,
		"two JSON values":       `{"nfrs": []} {"nfrs": []}`,
		"string vus":            `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": "1", "duration": "1s"}, "targets": {"p95": "1ms"}}]}`,
		"load under a second":   `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "500ms"}, "targets": {"p95": "1ms"}}]}`,
		"load duration missing": `{"nfrs": [{"id": "x", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1}, "targets": {"p95": "1ms"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeNFRs(t, root, content)

			_, err := exitchecks.LoadNFRs(root)

			var nfrsErr *exitchecks.NFRsError
			require.ErrorAs(t, err, &nfrsErr)
		})
	}
}

func TestLoadNFRs_SaysWhichNFRAndFieldIsWrong(t *testing.T) {
	root := t.TempDir()
	writeNFRs(t, root, `{"nfrs": [{"id": "slow", "areas": ["a/**"], "operation": "GET /a", "load": {"vus": 1, "duration": "a minute"}, "targets": {"p95": "1ms"}}]}`)

	_, err := exitchecks.LoadNFRs(root)

	require.EqualError(t, err, `.gofast/nfrs.json: nfrs[0] ("slow"): "load.duration" must be a positive Go duration such as "150ms" or "60s", not "a minute"`)
}
