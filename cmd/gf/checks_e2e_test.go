package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChecks_ShowsTheConfigurationAndEachStagesChecks(t *testing.T) {
	r := newRepo(t)

	out := r.gf("checks")
	assert.Contains(t, out, "defaults; .gofast/checks.json does not exist")
	assert.Contains(t, out, "integration  not configured: integration-passes fails until the owner adds one to .gofast/checks.json")
	assert.Contains(t, out, "  specify              artifact-written, specs-fail\n")
	assert.Contains(t, out, "  implement            artifact-written, build-passes, specs-pass, specs-untouched, suite-passes\n")

	r.write(".gofast/checks.json", `{"integration": {"command": ["go", "test", "-tags=integration", "./..."], "timeout": "15m"}, "timeout": "5m"}`)
	out = r.gf("checks")
	assert.Contains(t, out, "Exit-check configuration (.gofast/checks.json):")
	assert.Contains(t, out, "timeout      5m0s per check command")
	assert.Contains(t, out, "integration  go test -tags=integration ./... (timeout 15m0s)")

	r.write(".gofast/checks.json", `{"timeout": "soon"}`)
	res := r.gfFails(nil, "checks")
	assert.Equal(t, "The exit-check configuration .gofast/checks.json is invalid: \"timeout\" must be a positive Go duration such as \"10m\", not \"soon\". The owner fixes it; `gf checks` shows the result.\n", res.stderr)
}
