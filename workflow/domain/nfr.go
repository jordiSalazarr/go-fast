package domain

import (
	"fmt"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Non-functional requirements (NFRs) are owner-owned data: how an operation
// of the repository's service must behave under load. Agents read them and
// can't change them. Parsing them is the boundary's job; the domain receives
// typed values.

// NFRID identifies one NFR.
type NFRID struct{ value string }

func NewNFRID(s string) (NFRID, error) {
	if !idPattern.MatchString(s) {
		return NFRID{}, fmt.Errorf("nfr id %q: %w", s, ErrInvalidNFR)
	}
	return NFRID{value: s}, nil
}

func (id NFRID) String() string { return id.value }
func (id NFRID) isZero() bool   { return id.value == "" }

// AreaGlob is a glob over repository paths: slash-separated, relative to the
// repository root. A "**" segment matches any number of segments, none
// included; any other segment is matched as by path.Match.
type AreaGlob struct{ pattern string }

func NewAreaGlob(s string) (AreaGlob, error) {
	invalid := func(why string) (AreaGlob, error) {
		return AreaGlob{}, fmt.Errorf("area %q %s: %w", s, why, ErrInvalidNFR)
	}
	if s == "" || strings.HasPrefix(s, "/") || strings.Contains(s, `\`) {
		return invalid("must be a slash-separated path relative to the repository root")
	}
	for _, seg := range strings.Split(s, "/") {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return invalid("must not have empty, '.' or '..' segments")
		case seg != "**" && strings.Contains(seg, "**"):
			return invalid("may use ** only as a whole segment")
		}
		if _, err := path.Match(seg, ""); err != nil {
			return invalid("is not a valid glob")
		}
	}
	return AreaGlob{pattern: s}, nil
}

func (g AreaGlob) String() string { return g.pattern }

// Matches reports whether the path is in the area.
func (g AreaGlob) Matches(p RepoPath) bool {
	if p.isZero() || g.pattern == "" {
		return false
	}
	return matchSegments(strings.Split(g.pattern, "/"), strings.Split(p.value, "/"))
}

// dir is the directory the area names: its leading segments up to the first
// one with a wildcard, or, for a single file, the file's directory. ok is
// false when that is the repository root.
func (g AreaGlob) dir() (RepoPath, bool) {
	segments := strings.Split(g.pattern, "/")
	i := slices.IndexFunc(segments, func(seg string) bool { return strings.ContainsAny(seg, "*?[") })
	if i < 0 {
		i = len(segments) - 1
	}
	if i == 0 {
		return RepoPath{}, false
	}
	return RepoPath{value: strings.Join(segments[:i], "/")}, true
}

func matchSegments(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchSegments(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, _ := path.Match(pattern[0], name[0])
	return ok && matchSegments(pattern[1:], name[1:])
}

// Operation is the HTTP request an NFR is about: a method and a path, whose
// {name} segments stand for any value, e.g. "POST /wallets/{id}/transactions".
type Operation struct {
	method string
	path   string
}

var httpMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

func NewOperation(s string) (Operation, error) {
	method, p, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok || !slices.Contains(httpMethods, method) || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, " ?#") {
		return Operation{}, fmt.Errorf("operation %q must be an HTTP method and a path, e.g. \"POST /wallets/{id}/transactions\": %w", s, ErrInvalidNFR)
	}
	return Operation{method: method, path: p}, nil
}

func (o Operation) Method() string { return o.method }
func (o Operation) Path() string   { return o.path }
func (o Operation) String() string { return o.method + " " + o.path }
func (o Operation) isZero() bool   { return o.method == "" }

// Matches reports whether a request is this operation. The request path has
// no query; a "${}" segment is a value k6 grouped with http.url, and matches
// any segment.
func (o Operation) Matches(method, requestPath string) bool {
	if method != o.method {
		return false
	}
	want := strings.Split(o.path, "/")
	got := strings.Split(requestPath, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		param := strings.HasPrefix(want[i], "{") && strings.HasSuffix(want[i], "}")
		switch {
		case got[i] == "${}" || want[i] == got[i]:
		case param && got[i] != "":
		default:
			return false
		}
	}
	return true
}

// Load is the load an NFR holds under: this many virtual users, each sending
// requests one after another, for this long.
type Load struct {
	vus      int
	duration time.Duration
}

func NewLoad(vus int, duration time.Duration) (Load, error) {
	if vus < 1 || duration <= 0 {
		return Load{}, fmt.Errorf("load of %d vus for %s must have positive vus and duration: %w", vus, duration, ErrInvalidNFR)
	}
	return Load{vus: vus, duration: duration}, nil
}

func (l Load) VUs() int                { return l.vus }
func (l Load) Duration() time.Duration { return l.duration }
func (l Load) isZero() bool            { return l.vus == 0 }

// TargetValues are the targets an NFR sets; nil means no target.
type TargetValues struct {
	P95, P99     *time.Duration
	MaxErrorRate *float64 // a fraction of the requests, from 0 to 1
	MinRPS       *float64
}

// Targets are what an NFR's measurement must meet. Each is optional; at
// least one is set.
type Targets struct{ v TargetValues }

func NewTargets(v TargetValues) (Targets, error) {
	invalid := func(why string) (Targets, error) { return Targets{}, fmt.Errorf("targets: %s: %w", why, ErrInvalidNFR) }
	switch {
	case v.P95 == nil && v.P99 == nil && v.MaxErrorRate == nil && v.MinRPS == nil:
		return invalid("set at least one of p95, p99, maxErrorRate, minRps")
	case v.P95 != nil && *v.P95 <= 0, v.P99 != nil && *v.P99 <= 0:
		return invalid("p95 and p99 must be positive durations")
	case v.MaxErrorRate != nil && !(*v.MaxErrorRate >= 0 && *v.MaxErrorRate <= 1):
		return invalid("maxErrorRate must be a fraction from 0 to 1")
	case v.MinRPS != nil && !(*v.MinRPS > 0 && !math.IsInf(*v.MinRPS, 1)):
		return invalid("minRps must be a positive number")
	}
	return Targets{v: copyTargets(v)}, nil
}

func copyTargets(v TargetValues) TargetValues {
	clone := func(p *time.Duration) *time.Duration {
		if p == nil {
			return nil
		}
		d := *p
		return &d
	}
	cloneF := func(p *float64) *float64 {
		if p == nil {
			return nil
		}
		f := *p
		return &f
	}
	return TargetValues{P95: clone(v.P95), P99: clone(v.P99), MaxErrorRate: cloneF(v.MaxErrorRate), MinRPS: cloneF(v.MinRPS)}
}

// Values returns the targets; nil means no target.
func (t Targets) Values() TargetValues { return copyTargets(t.v) }

func (t Targets) isZero() bool {
	return t.v.P95 == nil && t.v.P99 == nil && t.v.MaxErrorRate == nil && t.v.MinRPS == nil
}

// MetBy reports whether a measurement meets every target, and describes each
// target it misses with the measured value against the target, e.g.
// "p95 212ms > 150ms". A value equal to its target meets it.
func (t Targets) MetBy(m Measurement) (bool, []string) {
	var missed []string
	if p := t.v.P95; p != nil && m.p95 > *p {
		missed = append(missed, "p95 "+formatLatency(m.p95)+" > "+formatLatency(*p))
	}
	if p := t.v.P99; p != nil && m.p99 > *p {
		missed = append(missed, "p99 "+formatLatency(m.p99)+" > "+formatLatency(*p))
	}
	if r := t.v.MaxErrorRate; r != nil && m.errorRate > *r {
		missed = append(missed, "error rate "+formatRate(m.errorRate)+" > "+formatRate(*r))
	}
	if r := t.v.MinRPS; r != nil && m.rps < *r {
		missed = append(missed, "rps "+formatRPS(m.rps)+" < "+formatRPS(*r))
	}
	return len(missed) == 0, missed
}

// Describe lists the targets, e.g. "p95 ≤ 150ms, minRps ≥ 200".
func (t Targets) Describe() string {
	var parts []string
	if p := t.v.P95; p != nil {
		parts = append(parts, "p95 ≤ "+formatLatency(*p))
	}
	if p := t.v.P99; p != nil {
		parts = append(parts, "p99 ≤ "+formatLatency(*p))
	}
	if r := t.v.MaxErrorRate; r != nil {
		parts = append(parts, "error rate ≤ "+formatRate(*r))
	}
	if r := t.v.MinRPS; r != nil {
		parts = append(parts, "rps ≥ "+formatRPS(*r))
	}
	return strings.Join(parts, ", ")
}

// formatLatency writes a latency in milliseconds, to 0.1ms.
func formatLatency(d time.Duration) string {
	return strconv.FormatFloat(math.Round(float64(d)/float64(time.Millisecond)*10)/10, 'f', -1, 64) + "ms"
}

// formatRate writes a fraction as a percentage, to 0.01%.
func formatRate(f float64) string {
	return strconv.FormatFloat(math.Round(f*10000)/100, 'f', -1, 64) + "%"
}

// formatRPS writes requests per second, to 0.1.
func formatRPS(f float64) string {
	return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64)
}

// MeasurementValues are what a stress run measured of one operation.
type MeasurementValues struct {
	P95, P99  time.Duration
	ErrorRate float64 // the fraction of requests that failed
	RPS       float64
	Requests  int
}

// Measurement is how an operation behaved under an NFR's load.
type Measurement struct {
	p95, p99  time.Duration
	errorRate float64
	rps       float64
	requests  int
}

func NewMeasurement(v MeasurementValues) (Measurement, error) {
	if v.P95 < 0 || v.P99 < 0 || v.Requests < 0 || !(v.RPS >= 0) || !(v.ErrorRate >= 0 && v.ErrorRate <= 1) {
		return Measurement{}, fmt.Errorf("measurement %+v: %w", v, ErrInvalidMeasurement)
	}
	return Measurement{p95: v.P95, p99: v.P99, errorRate: v.ErrorRate, rps: v.RPS, requests: v.Requests}, nil
}

func (m Measurement) Values() MeasurementValues {
	return MeasurementValues{P95: m.p95, P99: m.p99, ErrorRate: m.errorRate, RPS: m.rps, Requests: m.requests}
}

// Describe lists every measured value, e.g. "p95 212ms, p99 380ms, error
// rate 0%, rps 240.5, 14430 requests".
func (m Measurement) Describe() string {
	return fmt.Sprintf("p95 %s, p99 %s, error rate %s, rps %s, %d requests",
		formatLatency(m.p95), formatLatency(m.p99), formatRate(m.errorRate), formatRPS(m.rps), m.requests)
}

// NFR is one non-functional requirement: an operation of the service that,
// under a load, must meet targets. It covers areas of the repository: a
// change there can affect it.
type NFR struct {
	id          NFRID
	description string
	areas       []AreaGlob
	operation   Operation
	load        Load
	targets     Targets
}

// NewNFR needs an id, at least one area, an operation, a load and targets.
// The description is optional.
func NewNFR(id NFRID, description string, areas []AreaGlob, operation Operation, load Load, targets Targets) (NFR, error) {
	if err := requirePresent(required{"nfr id", id}, required{"operation", operation}, required{"load", load}, required{"targets", targets}); err != nil {
		return NFR{}, fmt.Errorf("nfr %s: %w", id, err)
	}
	if len(areas) == 0 {
		return NFR{}, fmt.Errorf("nfr %s: needs at least one area: %w", id, ErrInvalidNFR)
	}
	return NFR{id: id, description: strings.TrimSpace(description), areas: slices.Clone(areas), operation: operation, load: load, targets: targets}, nil
}

func (n NFR) ID() NFRID            { return n.id }
func (n NFR) Description() string  { return n.description }
func (n NFR) Areas() []AreaGlob    { return slices.Clone(n.areas) }
func (n NFR) Operation() Operation { return n.operation }
func (n NFR) Load() Load           { return n.load }
func (n NFR) Targets() Targets     { return n.targets }

// Covers reports whether any changed file is in one of the NFR's areas.
func (n NFR) Covers(changed ChangedFiles) bool {
	for _, p := range changed.paths {
		for _, a := range n.areas {
			if a.Matches(p) {
				return true
			}
		}
	}
	return false
}

// Evaluate judges a measurement of the NFR's operation against its targets.
func (n NFR) Evaluate(m Measurement) NFRResult {
	met, missed := n.targets.MetBy(m)
	return NFRResult{nfr: n.id, measurement: m, met: met, missed: missed}
}

// Covering returns the NFRs that cover the changed files, in order.
func Covering(nfrs []NFR, changed ChangedFiles) []NFR {
	var covering []NFR
	for _, n := range nfrs {
		if n.Covers(changed) {
			covering = append(covering, n)
		}
	}
	return covering
}

// NFRResult is an NFR's measurement judged against its targets.
type NFRResult struct {
	nfr         NFRID
	measurement Measurement
	met         bool
	missed      []string
}

func (r NFRResult) NFR() NFRID               { return r.nfr }
func (r NFRResult) Measurement() Measurement { return r.measurement }
func (r NFRResult) Met() bool                { return r.met }

// Missed describes each target the measurement missed.
func (r NFRResult) Missed() []string { return slices.Clone(r.missed) }

// Summary says how the NFR went: "wallet-transaction-latency: p95 212ms >
// 150ms" when it missed a target, "wallet-transaction-latency: met" when not.
func (r NFRResult) Summary() string {
	if r.met {
		return r.nfr.value + ": met"
	}
	return r.nfr.value + ": " + strings.Join(r.missed, ", ")
}

// Equal reports whether two NFRs are the same in every field.
func (n NFR) Equal(o NFR) bool {
	return n.id == o.id && n.description == o.description && slices.Equal(n.areas, o.areas) &&
		n.operation == o.operation && n.load == o.load && n.targets.equal(o.targets)
}

func (t Targets) equal(o Targets) bool {
	sameDuration := func(a, b *time.Duration) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	sameFloat := func(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	return sameDuration(t.v.P95, o.v.P95) && sameDuration(t.v.P99, o.v.P99) &&
		sameFloat(t.v.MaxErrorRate, o.v.MaxErrorRate) && sameFloat(t.v.MinRPS, o.v.MinRPS)
}

// NFRReplacement is a repository NFR and the proposed NFR with its id that
// replaces it.
type NFRReplacement struct{ Old, New NFR }

// NFRAdoption is what adopting a design's NFR proposals does to the
// repository's NFRs: a proposed NFR with an existing id replaces it, one
// with a new id is added, and one equal to an existing NFR changes nothing.
// Proposals never remove an NFR.
type NFRAdoption struct {
	added    []NFR
	replaced []NFRReplacement
	result   []NFR
}

// AdoptNFRs plans the adoption of proposed NFRs into the current ones.
func AdoptNFRs(current, proposed []NFR) NFRAdoption {
	var a NFRAdoption
	result := slices.Clone(current)
	for _, p := range proposed {
		i := slices.IndexFunc(result, func(n NFR) bool { return n.id == p.id })
		switch {
		case i < 0:
			a.added = append(a.added, p)
			result = append(result, p)
		case !result[i].Equal(p):
			a.replaced = append(a.replaced, NFRReplacement{Old: result[i], New: p})
			result[i] = p
		}
	}
	a.result = result
	return a
}

// Added are the proposed NFRs with new ids, in proposal order.
func (a NFRAdoption) Added() []NFR { return slices.Clone(a.added) }

// Replaced are the NFRs the proposals change, in proposal order.
func (a NFRAdoption) Replaced() []NFRReplacement { return slices.Clone(a.replaced) }

// Pending reports whether the current NFRs don't contain the proposed ones
// exactly yet.
func (a NFRAdoption) Pending() bool { return len(a.added)+len(a.replaced) > 0 }

// Result is the NFRs after adoption: the current ones in their order, with
// the replacements in place, then the added ones.
func (a NFRAdoption) Result() []NFR { return slices.Clone(a.result) }

// CanAdoptNFRProposals says whether the owner may adopt NFR proposals now:
// only while design waits for approval. Then no stage is open, so no
// stage's scope check sees the NFRs change, and the owner approves the
// design knowing which NFRs the work will be held to.
func CanAdoptNFRProposals(state AssignmentState) error {
	if waiting, ok := state.(AwaitingApproval); ok && waiting.stage == StageDesign {
		return nil
	}
	return ErrNFRAdoptionClosed
}
