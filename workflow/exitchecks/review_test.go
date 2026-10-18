package exitchecks_test

import (
	"strings"
	"testing"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

// passedReview is review's artifact as the review skill writes it, with a
// Passed verdict and no blocking finding.
const passedReview = "# review\n\n## Verdict\nPassed\n\n" +
	"## Risk map\n| # | Rule or behaviour at stake | Where | Why risky | Protected by | Check yourself |\n|---|---|---|---|---|---|\n" +
	"| R1 | the server listens on one port | server/server.go:3 | a wrong port is an outage | server_test.go | curl the port |\n\n" +
	"## Agreed behaviour\n| Item | Implemented in | Proven by | OK |\n|---|---|---|---|\n| A1 port 8080 | server/server.go:3 | server_test.go | ✓ |\n\n" +
	"## Should fix\n- S1 server/server.go:3: name the port.\n"

func reviewed(t *testing.T, artifact string) domain.CheckResult {
	t.Helper()
	m := newModule(t)
	m.begin(domain.StageReview)
	m.write(domain.ArtifactFor(WorkID, domain.StageReview, Visit1).String(), artifact)
	return m.check(domain.StageReview, exitchecks.Config{})[domain.CheckReviewVerdictConsistent]
}

func TestReviewVerdictConsistent_PassedWithNoBlockingFinding_Passes(t *testing.T) {
	passes(t, reviewed(t, passedReview))
	passes(t, reviewed(t, passedReview+"\n## Blocking\n\nNone.\n"))
}

func TestReviewVerdictConsistent_RefusesAPassTheArtifactDisagreesWith(t *testing.T) {
	blocking := "\n## Blocking\n### B1 a debit can overdraw\n- Where: server/server.go:3\n\n### B2 no spec for refunds\n- Where: server/server.go:4\n"
	cases := map[string]struct {
		artifact string
		want     string
	}{
		"blocking findings": {passedReview + blocking, "the verdict says Passed but ## Blocking lists B1, B2"},
		"a Failed verdict": {strings.Replace(passedReview, "Passed\n", "Failed: 2 blocking\n", 1) + blocking,
			`the verdict says "Failed: 2 blocking", not Passed`},
		"no verdict":           {strings.Replace(passedReview, "## Verdict\nPassed\n", "", 1), "no '## Verdict' section"},
		"no risk map":          {strings.Replace(passedReview, "## Risk map", "## Risks", 1), "no '## Risk map' section"},
		"no agreed behaviour":  {strings.Replace(passedReview, "## Agreed behaviour", "## Behaviour", 1), "no '## Agreed behaviour' section"},
		"an empty risk map":    {"## Verdict\nPassed\n\n## Risk map\n\n## Agreed behaviour\n- A1 ✓\n", "the '## Risk map' section is empty"},
		"the skill's template": {"## Verdict\nPassed | Failed: <n> blocking\n" + blocking, "## Blocking lists B1, B2"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fails(t, reviewed(t, c.artifact), ".gofast/works/w1/review-v1.md: ", c.want)
		})
	}
}
