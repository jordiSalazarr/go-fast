package exitchecks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const walletScript = `import http from 'k6/http';
import { check } from 'k6';

// Data setup: one wallet per run. Not thresholds: gf judges the run.
export function setup() {
  const res = http.post(` + "`${__ENV.BASE_URL}/wallets`" + `, JSON.stringify({ owner: "load", duration: "ignored" }));
  return { id: res.json('id') };
}

export const options = {
  discardResponseBodies: false,
  tags: { suite: "stress" },
};

export default function (data) {
  const body = JSON.stringify({ amount: 5, vus: 'a field of the request, not an option' });
  http.post(` + "`${__ENV.BASE_URL}/wallets/${data.id}/transactions`" + `, body);
}
`

func TestScriptProblems_AScriptThatOnlyGeneratesLoad_HasNone(t *testing.T) {
	assert.Empty(t, scriptProblems(walletScript))
	assert.Empty(t, scriptProblems("import http from 'k6/http';\nexport default function () { http.get(`${__ENV.BASE_URL}/`); }\n"))
	assert.Empty(t, scriptProblems("// export const options = { thresholds: {} };\n/* handleSummary */\nexport default function () {}\n"), "comments")
	assert.Empty(t, scriptProblems("const s = 'export const options = { vus: 10 }';\nexport default function () {}\n"), "strings")
}

// Given a script, when gf reads it, then everything that sets the load,
// throttles it or judges the run is refused.
func TestScriptProblems_RefusesWhatSetsOrJudgesTheRun(t *testing.T) {
	cases := map[string]struct {
		src  string
		want []string
	}{
		"thresholds": {`export const options = { thresholds: { http_req_duration: ['p(95)<150'] } };`,
			[]string{"sets options.thresholds (gf sets the load from the NFR)"}},
		"quoted thresholds": {`export let options = { "thresholds": {} };`,
			[]string{"sets options.thresholds (gf sets the load from the NFR)"}},
		"handleSummary": {"export function handleSummary(data) { return { stdout: 'ok' }; }",
			[]string{"defines handleSummary (gf measures the run itself)"}},
		"handleSummary re-exported": {"function s() {}\nexport { s as handleSummary };",
			[]string{"defines handleSummary (gf measures the run itself)"}},
		"vus and duration": {"export const options = {\n  vus: 1,\n  duration: '1s',\n};",
			[]string{"sets options.vus (gf sets the load from the NFR)", "sets options.duration (gf sets the load from the NFR)"}},
		"scenarios with an executor": {`export const options = { scenarios: { s: { executor: 'constant-vus', vus: 1, duration: '1s' } } };`,
			[]string{"sets options.scenarios (gf sets the load from the NFR)", "sets options.vus (gf sets the load from the NFR)", "sets options.duration (gf sets the load from the NFR)"}},
		"stages":                 {`export const options = { stages: [{ duration: '1s', target: 1 }] };`, []string{"sets options.stages (gf sets the load from the NFR)", "sets options.duration (gf sets the load from the NFR)"}},
		"iterations":             {`export const options = { iterations: 10 };`, []string{"sets options.iterations (gf sets the load from the NFR)"}},
		"rps throttles the load": {`export const options = { rps: 5 };`, []string{"sets options.rps (gf sets the load from the NFR)"}},
		"min iteration duration": {`export const options = { minIterationDuration: '1s' };`, []string{"sets options.minIterationDuration (gf sets the load from the NFR)"}},
		"shorthand":              {"const thresholds = {};\nexport const options = { thresholds };", []string{"sets options.thresholds (gf sets the load from the NFR)"}},
		"assigned later":         {"export const options = {};\noptions.thresholds = {};", []string{"sets options.thresholds"}},
		"assigned by index":      {"export const options = {};\noptions['vus'] = 3;", []string{"sets options.vus"}},
		"not a literal":          {"export const options = makeOptions();", []string{"exports options that aren't an object literal; use `export const options = {...}`"}},
		"spread":                 {"const base = { vus: 1 };\nexport const options = { ...base };", []string{"spreads other values into options; list each option in the literal"}},
		"computed key":           {"const k = 'vus';\nexport const options = { [k]: 1 };", []string{"uses computed keys in options; write each option's name"}},
		"exported by name":       {"const options = { vus: 1 };\nexport { options };", []string{"exports options by name; export them as `export const options = {...}`"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, scriptProblems(c.src))
		})
	}
}
