package exitchecks_test

import (
	"strings"
	"testing"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

// skeleton declares what the freeze spec needs, doing nothing.
const skeleton = `package server

import (
	"errors"
	"time"
)

var errSkeleton = errors.New("skeleton: not implemented")

const MaxFreezeDays = 30

type FreezeReason string

type Wallet struct{ Frozen bool }

type WalletFrozen struct{ At time.Time }

func (w Wallet) Freeze(reason FreezeReason, at time.Time) (WalletFrozen, error) {
	return WalletFrozen{}, errSkeleton
}

func IsFrozen(w Wallet) bool { return false }

func Audit(w *Wallet) {}

func Days() (int, float64, string, *Wallet, []string) { return 0, 0.0, "", nil, []string{} }
`

// freezeSpec fails on the skeleton for the right reason: the behaviour is
// missing.
const freezeSpec = `package server

import (
	"testing"
	"time"
)

func TestFreeze_FreezesAnOpenWallet(t *testing.T) {
	if _, err := (Wallet{}).Freeze("fraud", time.Unix(0, 0)); err != nil {
		t.Fatalf("freeze: %v", err)
	}
}
`

// specifiedWith runs specify's checks after a visit that wrote the freeze
// spec and, as change does, skeletons.
func specifiedWith(t *testing.T, change func(m *module)) map[domain.ExitCheck]domain.CheckResult {
	t.Helper()
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/freeze_test.go", freezeSpec)
	m.writeArtifact(domain.StageSpecify)
	change(m)
	return m.check(domain.StageSpecify, exitchecks.Config{})
}

func TestSkeletonsOnly_AValidSkeletonPasses_AndItsSpecFailsForTheRightReason(t *testing.T) {
	results := specifiedWith(t, func(m *module) { m.write("server/freeze.go", skeleton) })
	passes(t, results[domain.CheckSkeletonsOnly])
	passes(t, results[domain.CheckSpecsFail])
}

func TestSkeletonsOnly_NoSkeletonPasses(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/server_test.go", failingSpec)
	m.writeArtifact(domain.StageSpecify)
	passes(t, m.check(domain.StageSpecify, exitchecks.Config{})[domain.CheckSkeletonsOnly])
}

func TestSkeletonsOnly_RefusesWhatDoesMoreThanDeclare(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"an if": {replace(skeleton, "func IsFrozen(w Wallet) bool { return false }",
			"func IsFrozen(w Wallet) bool {\n\tif w.Frozen {\n\t\treturn true\n\t}\n\treturn false\n}"),
			"server/freeze.go: func IsFrozen has logic"},
		"a panic": {replace(skeleton, "func Audit(w *Wallet) {}", "func Audit(w *Wallet) { panic(\"todo\") }"),
			"server/freeze.go: func Audit panics"},
		"a non-zero result": {replace(skeleton, "return WalletFrozen{}, errSkeleton", "return WalletFrozen{At: at}, nil"),
			"server/freeze.go: func (Wallet) Freeze has logic"},
		"a second var": {skeleton + "\nvar frozen = map[string]bool{}\n",
			"server/freeze.go: var frozen is not `var errSkeleton = errors.New(\"…\")`"},
		"a statement in an empty-result func": {replace(skeleton, "func Audit(w *Wallet) {}", "func Audit(w *Wallet) { w.Frozen = true }"),
			"server/freeze.go: func Audit has logic"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			results := specifiedWith(t, func(m *module) { m.write("server/freeze.go", c.src) })
			fails(t, results[domain.CheckSkeletonsOnly], c.want)
		})
	}
}

func TestSkeletonsOnly_ErrSkeletonOncePerPackage(t *testing.T) {
	results := specifiedWith(t, func(m *module) {
		m.write("server/freeze.go", skeleton)
		m.write("server/thaw.go", "package server\n\nimport \"errors\"\n\nvar errSkeleton = errors.New(\"skeleton\")\n")
	})
	fails(t, results[domain.CheckSkeletonsOnly], "server: 2 declarations of errSkeleton; declare it once per package")
}

func TestSkeletonsOnly_AnEditedExistingProductionFileFails(t *testing.T) {
	results := specifiedWith(t, func(m *module) {
		m.write("server/freeze.go", skeleton)
		m.write("server/server.go", "package server\n\nfunc Port() int { return 8080 }\n")
	})
	fails(t, results[domain.CheckSkeletonsOnly], "server/server.go: an existing production file changed; specify may only add skeleton files")
}

func TestSkeletonsFilled(t *testing.T) {
	implement := func(change func(m *module)) domain.CheckResult {
		t.Helper()
		m := newModule(t)
		m.begin(domain.StageSpecify)
		m.write("server/freeze.go", skeleton)
		m.write("server/freeze_test.go", freezeSpec)
		m.writeArtifact(domain.StageSpecify)
		m.begin(domain.StageImplement)
		m.writeArtifact(domain.StageImplement)
		change(m)
		return m.check(domain.StageImplement, exitchecks.Config{})[domain.CheckSkeletonsFilled]
	}
	fails(t, implement(func(m *module) {}), "errSkeleton is still in server/freeze.go; fill every skeleton specify wrote")
	passes(t, implement(func(m *module) {
		m.write("server/freeze.go", replace(replace(skeleton,
			"var errSkeleton = errors.New(\"skeleton: not implemented\")", "var ErrFrozen = errors.New(\"frozen\")\n\n// errSkeleton is gone."),
			"return WalletFrozen{}, errSkeleton", "return WalletFrozen{At: at}, nil"))
	}))
}

// replace replaces old in s, which must contain it.
func replace(s, old, new string) string {
	if !strings.Contains(s, old) {
		panic("no " + old)
	}
	return strings.Replace(s, old, new, 1)
}
