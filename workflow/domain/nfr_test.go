package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func ms(n float64) time.Duration { return time.Duration(n * float64(time.Millisecond)) }
func ptr[T any](v T) *T          { return &v }

func areas(globs ...string) []domain.AreaGlob {
	var out []domain.AreaGlob
	for _, g := range globs {
		out = append(out, must(domain.NewAreaGlob(g)))
	}
	return out
}

func changedFiles(paths ...string) domain.ChangedFiles {
	var rps []domain.RepoPath
	for _, p := range paths {
		rps = append(rps, must(domain.NewRepoPath(repoRoot, p)))
	}
	return domain.NewChangedFiles(rps...)
}

// walletNFR covers the wallet and ledger packages.
func walletNFR(targets domain.TargetValues) domain.NFR {
	return must(domain.NewNFR(
		must(domain.NewNFRID("wallet-transaction-latency")), "Posting a transaction stays fast",
		areas("internal/wallet/**", "internal/ledger/**"),
		must(domain.NewOperation("POST /wallets/{id}/transactions")),
		must(domain.NewLoad(50, 60*time.Second)),
		must(domain.NewTargets(targets)),
	))
}

func measured(v domain.MeasurementValues) domain.Measurement { return must(domain.NewMeasurement(v)) }

// Given an NFR's areas, when files changed, then it covers them only if one
// of them is in an area.
func TestNFR_Covers(t *testing.T) {
	nfr := walletNFR(domain.TargetValues{P95: ptr(ms(150))})
	cases := map[string]struct {
		changed domain.ChangedFiles
		want    bool
	}{
		"a file in an area":               {changedFiles("internal/wallet/post.go"), true},
		"a file deep in an area":          {changedFiles("internal/ledger/store/sql/tx.go"), true},
		"one of several files in an area": {changedFiles("README.md", "internal/ledger/entry.go"), true},
		"no file in an area":              {changedFiles("internal/users/user.go", "cmd/server/main.go"), false},
		"a sibling with a shared prefix":  {changedFiles("internal/wallets/post.go"), false},
		"the area's directory itself":     {changedFiles("internal/wallet"), true},
		"nothing changed":                 {domain.NewChangedFiles(), false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, nfr.Covers(c.changed))
		})
	}
}

func TestAreaGlob_Matches(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"internal/wallet/**", "internal/wallet/a.go", true},
		{"internal/*/handler.go", "internal/wallet/handler.go", true},
		{"internal/*/handler.go", "internal/wallet/sub/handler.go", false},
		{"**/*_handler.go", "a/b/wallet_handler.go", true},
		{"**/*_handler.go", "wallet_handler.go", true},
		{"cmd/server/main.go", "cmd/server/main.go", true},
		{"cmd/server/main.go", "cmd/server/main_test.go", false},
		{"**", "anything/at/all.go", true},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, must(domain.NewAreaGlob(c.glob)).Matches(must(domain.NewRepoPath(repoRoot, c.path))), "%s ~ %s", c.glob, c.path)
	}
	for _, bad := range []string{"", "/abs/**", "a/../b", "a//b", "a/b**", "a/[", `a\b`} {
		_, err := domain.NewAreaGlob(bad)
		assert.ErrorIs(t, err, domain.ErrInvalidNFR, bad)
	}
}

func TestOperation(t *testing.T) {
	op := must(domain.NewOperation("POST /wallets/{id}/transactions"))
	assert.Equal(t, "POST", op.Method())
	assert.Equal(t, "/wallets/{id}/transactions", op.Path())
	assert.True(t, op.Matches("POST", "/wallets/42/transactions"))
	assert.True(t, op.Matches("POST", "/wallets/${}/transactions"), "k6's http.url grouping")
	assert.False(t, op.Matches("GET", "/wallets/42/transactions"), "another method")
	assert.False(t, op.Matches("POST", "/wallets//transactions"), "an empty value")
	assert.False(t, op.Matches("POST", "/wallets/42/transactions/7"), "a longer path")
	assert.False(t, op.Matches("POST", "/wallets/42"), "a shorter path")

	for _, bad := range []string{"", "POST", "/wallets", "post /wallets", "POST wallets", "POST /a?b=1", "FETCH /a"} {
		_, err := domain.NewOperation(bad)
		assert.ErrorIs(t, err, domain.ErrInvalidNFR, bad)
	}
}

func TestNFRValuesRejectInvalidInput(t *testing.T) {
	cases := map[string]struct{ err, target error }{
		"bad id":                {second(domain.NewNFRID("a b")), domain.ErrInvalidNFR},
		"zero vus":              {second(domain.NewLoad(0, time.Second)), domain.ErrInvalidNFR},
		"zero duration":         {second(domain.NewLoad(1, 0)), domain.ErrInvalidNFR},
		"no target":             {second(domain.NewTargets(domain.TargetValues{})), domain.ErrInvalidNFR},
		"zero p95":              {second(domain.NewTargets(domain.TargetValues{P95: ptr(time.Duration(0))})), domain.ErrInvalidNFR},
		"error rate above 1":    {second(domain.NewTargets(domain.TargetValues{MaxErrorRate: ptr(1.5)})), domain.ErrInvalidNFR},
		"negative error rate":   {second(domain.NewTargets(domain.TargetValues{MaxErrorRate: ptr(-0.1)})), domain.ErrInvalidNFR},
		"zero min rps":          {second(domain.NewTargets(domain.TargetValues{MinRPS: ptr(0.0)})), domain.ErrInvalidNFR},
		"negative measurement":  {second(domain.NewMeasurement(domain.MeasurementValues{P95: -1})), domain.ErrInvalidMeasurement},
		"error rate measured 2": {second(domain.NewMeasurement(domain.MeasurementValues{ErrorRate: 2})), domain.ErrInvalidMeasurement},
		"nfr without areas": {second(domain.NewNFR(must(domain.NewNFRID("x")), "", nil, must(domain.NewOperation("GET /")),
			must(domain.NewLoad(1, time.Second)), must(domain.NewTargets(domain.TargetValues{P95: ptr(ms(1))})))), domain.ErrInvalidNFR},
		"nfr without targets": {second(domain.NewNFR(must(domain.NewNFRID("x")), "", areas("a/**"), must(domain.NewOperation("GET /")),
			must(domain.NewLoad(1, time.Second)), domain.Targets{})), domain.ErrMissingValue},
	}
	for name, c := range cases {
		require.ErrorIs(t, c.err, c.target, name)
	}
}

// Given targets, when a measurement is judged, then each target it misses is
// described with the measured value against the target; a value equal to
// its target meets it.
func TestTargets_MetBy(t *testing.T) {
	all := must(domain.NewTargets(domain.TargetValues{P95: ptr(ms(150)), P99: ptr(ms(400)), MaxErrorRate: ptr(0.001), MinRPS: ptr(200.0)}))
	atTargets := domain.MeasurementValues{P95: ms(150), P99: ms(400), ErrorRate: 0.001, RPS: 200, Requests: 12000}
	cases := []struct {
		name   string
		change func(*domain.MeasurementValues)
		missed []string
	}{
		{"every value at its target", func(*domain.MeasurementValues) {}, nil},
		{"p95 above", func(m *domain.MeasurementValues) { m.P95 = ms(212) }, []string{"p95 212ms > 150ms"}},
		{"p95 just above", func(m *domain.MeasurementValues) { m.P95 = ms(150) + time.Microsecond*100 }, []string{"p95 150.1ms > 150ms"}},
		{"p99 above", func(m *domain.MeasurementValues) { m.P99 = ms(401.25) }, []string{"p99 401.3ms > 400ms"}},
		{"error rate above", func(m *domain.MeasurementValues) { m.ErrorRate = 0.0125 }, []string{"error rate 1.25% > 0.1%"}},
		{"rps below", func(m *domain.MeasurementValues) { m.RPS = 187.44 }, []string{"rps 187.4 < 200"}},
		{"everything missed", func(m *domain.MeasurementValues) {
			*m = domain.MeasurementValues{P95: ms(300), P99: ms(900), ErrorRate: 0.5, RPS: 10, Requests: 600}
		}, []string{"p95 300ms > 150ms", "p99 900ms > 400ms", "error rate 50% > 0.1%", "rps 10 < 200"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := atTargets
			c.change(&v)

			met, missed := all.MetBy(measured(v))

			assert.Equal(t, c.missed == nil, met)
			assert.Equal(t, c.missed, missed)
		})
	}
}

func TestTargets_OnlyTheSetTargetsAreJudged(t *testing.T) {
	p95Only := must(domain.NewTargets(domain.TargetValues{P95: ptr(ms(150))}))

	met, missed := p95Only.MetBy(measured(domain.MeasurementValues{P95: ms(100), P99: ms(5000), ErrorRate: 1, RPS: 0.1}))

	assert.True(t, met)
	assert.Empty(t, missed)
	assert.Equal(t, "p95 ≤ 150ms", p95Only.Describe())
}

func TestNFR_Evaluate(t *testing.T) {
	nfr := walletNFR(domain.TargetValues{P95: ptr(ms(150)), MinRPS: ptr(200.0)})

	missed := nfr.Evaluate(measured(domain.MeasurementValues{P95: ms(212), P99: ms(300), RPS: 250, Requests: 15000}))
	met := nfr.Evaluate(measured(domain.MeasurementValues{P95: ms(120), P99: ms(300), RPS: 250, Requests: 15000}))

	assert.False(t, missed.Met())
	assert.Equal(t, "wallet-transaction-latency: p95 212ms > 150ms", missed.Summary())
	assert.True(t, met.Met())
	assert.Equal(t, "wallet-transaction-latency: met", met.Summary())
	assert.Equal(t, "p95 120ms, p99 300ms, error rate 0%, rps 250, 15000 requests", met.Measurement().Describe())
}

func TestCovering_KeepsTheNFRsThatCoverTheChange(t *testing.T) {
	wallet := walletNFR(domain.TargetValues{P95: ptr(ms(150))})
	users := must(domain.NewNFR(must(domain.NewNFRID("user-lookup")), "", areas("internal/users/**"),
		must(domain.NewOperation("GET /users/{id}")), must(domain.NewLoad(10, time.Second)), must(domain.NewTargets(domain.TargetValues{P99: ptr(ms(50))}))))

	got := domain.Covering([]domain.NFR{wallet, users}, changedFiles("internal/users/find.go", "docs/x.md"))

	assert.Equal(t, []domain.NFR{users}, got)
	assert.Empty(t, domain.Covering([]domain.NFR{wallet, users}, changedFiles("docs/x.md")))
}
