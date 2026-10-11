package exitchecks

import (
	"compress/gzip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func mustOp(s string) domain.Operation {
	op, err := domain.NewOperation(s)
	if err != nil {
		panic(err)
	}
	return op
}

var transactions = mustOp("POST /wallets/{id}/transactions")

// testdata/k6-wallet.json.gz was recorded with k6 v2.1.0 running
// testdata/k6-wallet.js (2 vus, 1s, the flags gf passes) against a server
// whose every 10th transaction answers 500. Each iteration posts a
// transaction plainly and with http.url (both count), gets the wallet and
// posts a transaction renamed with a custom name tag (neither counts);
// setup() creates the wallet (not part of the load).
func TestMeasure_ARecordedK6Run(t *testing.T) {
	f, err := os.Open("testdata/k6-wallet.json.gz")
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)

	m, err := measure(gz, transactions, "http://127.0.0.1:18090", time.Second)

	require.NoError(t, err)
	assert.Equal(t, domain.MeasurementValues{
		P95: 3686 * time.Microsecond, P99: 5304700 * time.Nanosecond,
		ErrorRate: 36.0 / 364.0, RPS: 364, Requests: 364,
	}, m.Values())
}

// point is k6's JSON line for one request.
func point(method, url, status, scenario string, ms float64) string {
	return `{"metric":"http_req_duration","type":"Point","data":{"time":"2026-10-11T12:00:00Z","value":` +
		strconv.FormatFloat(ms, 'f', -1, 64) + `,"tags":{"method":"` + method + `","url":"` + url +
		`","status":"` + status + `","scenario":"` + scenario + `"}}}`
}

func TestMeasure_PercentilesInterpolateLikeK6_AndOnlyTheLoadsOperationCounts(t *testing.T) {
	const base = "http://localhost:8080"
	var lines []string
	for i := 1; i <= 10; i++ { // 10ms, 20ms, ... 100ms
		lines = append(lines, point("POST", base+"/wallets/7/transactions", "201", "default", float64(10*i)))
	}
	lines = append(lines,
		point("POST", base+"/wallets/7/transactions", "201", "", 9000),                   // setup(): no scenario
		point("GET", base+"/wallets/7", "200", "default", 9000),                          // another operation
		point("POST", base+"/wallets/7/transactions/x", "201", "default", 9000),          // another path
		point("POST", "http://elsewhere/wallets/7/transactions", "201", "default", 9000), // another host
		point("POST", "renamed", "201", "default", 9000),                                 // a custom name tag
		`{"type":"Metric","data":{"name":"http_req_duration","type":"trend"},"metric":"http_req_duration"}`,
		`{"metric":"http_req_failed","type":"Point","data":{"value":1,"tags":{"scenario":"default"}}}`,
	)

	m, err := measure(strings.NewReader(strings.Join(lines, "\n")+"\n"), transactions, base+"/", 2*time.Second)

	require.NoError(t, err)
	// p(95) of 10..100 is at index 8.55: 90 + 0.55*10.
	assert.Equal(t, domain.MeasurementValues{P95: 95500 * time.Microsecond, P99: 99100 * time.Microsecond, RPS: 5, Requests: 10}, m.Values())
}

func TestMeasure_AFailedRequestIsAnErrorStatusOrNoResponse_WhateverTheScriptExpects(t *testing.T) {
	const base = "http://localhost:8080"
	lines := []string{
		point("POST", base+"/wallets/1/transactions", "201", "default", 1),
		point("POST", base+"/wallets/1/transactions", "409", "default", 1),
		point("POST", base+"/wallets/1/transactions", "503", "default", 1),
		point("POST", base+"/wallets/1/transactions", "0", "default", 1),
	}

	m, err := measure(strings.NewReader(strings.Join(lines, "\n")), transactions, base, time.Second)

	require.NoError(t, err)
	assert.Equal(t, 0.75, m.Values().ErrorRate)
}

func TestMeasure_NoRequestsOfTheOperation_MeasuresNothing(t *testing.T) {
	m, err := measure(strings.NewReader(point("GET", "http://a/", "200", "default", 1)), transactions, "http://a", time.Second)

	require.NoError(t, err)
	assert.Equal(t, domain.MeasurementValues{}, m.Values())
}

func TestRequestPath(t *testing.T) {
	for url, want := range map[string]string{
		"http://a:8080/wallets/1?x=1":    "/wallets/1",
		"http://a:8080":                  "/",
		"${}/wallets/${}/transactions":   "/wallets/${}/transactions",
		"http://a:80801/wallets":         "",
		"grouped":                        "",
		"http://a:8080/wallets/1#anchor": "/wallets/1",
	} {
		got, ok := requestPath(url, "http://a:8080")
		assert.Equal(t, want, got, url)
		assert.Equal(t, want != "", ok, url)
	}
}
