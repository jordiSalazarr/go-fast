package exitchecks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// BottleneckHeading is the section of measure's artifact, and of the
// fixer's in performance mode, that diagnoses what dominates the cost.
const BottleneckHeading = "Bottleneck"

// HasBottleneck reports whether an artifact's `## Bottleneck` section says
// something.
func HasBottleneck(content string) bool {
	body, found := markdownSection(content, BottleneckHeading)
	return found && body != ""
}

// BaselinePath is where gf keeps a work's baseline, relative to the
// repository root: one result per target NFR, as measure measured them
// before the work changed the code.
func BaselinePath(work domain.WorkID) string {
	return eventlog.Dir("") + "/" + eventlog.RuntimeDir + "/baselines/" + work.String() + "/nfrs.json"
}

// WriteBaseline records the measurements the work recorded as its
// baseline, as the stress run of the measure visit measured them.
func WriteBaseline(root string, work domain.WorkID, results []StressResult) error {
	path := filepath.Join(root, filepath.FromSlash(BaselinePath(work)))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// ReadBaseline returns the work's baseline; ok is false when it has none.
func ReadBaseline(root string, work domain.WorkID) (results []StressResult, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(BaselinePath(work))))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, false, fmt.Errorf("read %s: %w", BaselinePath(work), err)
	}
	return results, true, nil
}

// MeasurementOf is a recorded result's measurement.
func MeasurementOf(r StressResult) (domain.Measurement, error) {
	ms := func(f float64) time.Duration { return time.Duration(f * float64(time.Millisecond)) }
	return domain.NewMeasurement(domain.MeasurementValues{P95: ms(r.P95Ms), P99: ms(r.P99Ms), ErrorRate: r.ErrorRate, RPS: r.RPS, Requests: r.Requests})
}

// MeasuredBaseline is what a measure visit's latest stress run measured of
// the target NFRs, each judged against its NFR's current targets, and the
// raw results gf records as the baseline.
func MeasuredBaseline(root string, id domain.AssignmentID, targets []domain.NFRID) ([]domain.NFRResult, []StressResult, error) {
	_, results, ok, err := ReadStressResults(root, id)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("measure %s: no stress run measured anything: %w", id, domain.ErrBaselineIncomplete)
	}
	nfrs, err := LoadNFRs(root)
	if err != nil {
		return nil, nil, err
	}
	var judged []domain.NFRResult
	var kept []StressResult
	for _, target := range targets {
		i := slices.IndexFunc(results, func(r StressResult) bool { return r.NFR == target.String() && r.Problem == "" })
		j := slices.IndexFunc(nfrs, func(n domain.NFR) bool { return n.ID() == target })
		if i < 0 || j < 0 {
			return nil, nil, fmt.Errorf("measure %s: %s: %w", id, target, domain.ErrBaselineIncomplete)
		}
		m, err := MeasurementOf(results[i])
		if err != nil {
			return nil, nil, err
		}
		judged = append(judged, nfrs[j].Evaluate(m))
		kept = append(kept, results[i])
	}
	return judged, kept, nil
}

// baselineRecorded: measure's stress run measured every target NFR.
func (c *checkRun) baselineRecorded() (finding, error) {
	run, err := c.runStress()
	if err != nil {
		return finding{}, err
	}
	if len(run.problems) > 0 {
		return fail("not measured: the stress scripts aren't valid (see stress-scripts-valid)"), nil
	}
	var missing []string
	for _, target := range c.visit.TargetNFRs {
		i := slices.IndexFunc(run.outcomes, func(o nfrOutcome) bool { return o.nfr.ID() == target })
		switch {
		case i < 0:
			missing = append(missing, target.String()+": no script ran")
		case run.outcomes[i].problem != "":
			missing = append(missing, target.String()+": "+run.outcomes[i].problem)
		}
	}
	if len(missing) > 0 {
		return c.ran(fail("not every target NFR was measured: %s", strings.Join(missing, "; "))), nil
	}
	var measured []string
	for _, o := range run.outcomes {
		if slices.Contains(c.visit.TargetNFRs, o.nfr.ID()) {
			measured = append(measured, o.nfr.ID().String()+" "+o.result.Measurement().Describe())
		}
	}
	return finding{passed: true, note: strings.Join(measured, "; ")}, nil
}

// diagnosisWritten: the artifact's `## Bottleneck` says what dominates, with
// evidence: it references a file of the work's artifact directory.
func (c *checkRun) diagnosisWritten() (finding, error) {
	artifact := domain.ArtifactFor(c.visit.Work, c.visit.Stage, c.visit.Visit)
	content, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(artifact.String())))
	if errors.Is(err, os.ErrNotExist) {
		return fail("%s is missing", artifact), nil
	}
	if err != nil {
		return finding{}, err
	}
	return c.diagnosisIn(string(content), artifact)
}

// diagnosisIn judges an artifact's `## Bottleneck` section.
func (c *checkRun) diagnosisIn(content string, artifact domain.RepoPath) (finding, error) {
	body, found := markdownSection(content, BottleneckHeading)
	switch {
	case !found:
		return fail("%s has no '## %s' section: say what dominates the cost, with evidence", artifact, BottleneckHeading), nil
	case body == "":
		return fail("the '## %s' section of %s is empty", BottleneckHeading, artifact), nil
	}
	ok, err := ReferencesEvidence(c.Root, c.visit.Work, artifact, body)
	if err != nil || ok {
		return pass, err
	}
	return fail("the '## %s' section references no file in %s/: save the profiles and measurements there and name them as evidence",
		BottleneckHeading, domain.ArtifactDir(c.visit.Work)), nil
}

// ReferencesEvidence reports whether text names a file of the work's
// artifact directory other than artifact itself: by its path from the
// repository root, from the artifact directory, or by its name.
func ReferencesEvidence(root string, work domain.WorkID, artifact domain.RepoPath, text string) (bool, error) {
	dir := domain.ArtifactDir(work).String()
	found := false
	err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || found {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		repoPath := filepath.ToSlash(rel)
		if repoPath == artifact.String() {
			return nil
		}
		inDir := strings.TrimPrefix(repoPath, dir+"/")
		found = strings.Contains(text, repoPath) || strings.Contains(text, inDir) || strings.Contains(text, d.Name())
		return nil
	})
	return found, err
}

// comparedToBaseline describes, for each target NFR this run measured, how
// it moved since the work's baseline; empty when the work has none.
func (c *checkRun) comparedToBaseline(outcomes []nfrOutcome) (string, error) {
	if len(c.visit.TargetNFRs) == 0 || c.visit.Stage != domain.StageStressTesting {
		return "", nil
	}
	baseline, ok, err := ReadBaseline(c.Root, c.visit.Work)
	if err != nil || !ok {
		return "", err
	}
	var parts []string
	for _, o := range outcomes {
		if o.problem != "" || !slices.Contains(c.visit.TargetNFRs, o.nfr.ID()) {
			continue
		}
		i := slices.IndexFunc(baseline, func(r StressResult) bool { return r.NFR == o.nfr.ID().String() })
		if i < 0 {
			continue
		}
		before, err := MeasurementOf(baseline[i])
		if err != nil {
			return "", err
		}
		parts = append(parts, o.nfr.ID().String()+": "+domain.CompareToBaseline(o.nfr, before, o.result.Measurement()))
	}
	return strings.Join(parts, "; "), nil
}
