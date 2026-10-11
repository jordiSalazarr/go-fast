package exitchecks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// NFRsPath is the repository's non-functional requirements, relative to the
// repository root. Like ConfigPath, the owner writes and commits it, and it
// sits outside every artifact directory, so agents can read it but not
// change it.
const NFRsPath = ".gofast/nfrs.json"

// NFRsError says what is wrong with a file of NFRs: NFRsPath, or the NFRs a
// design proposes.
type NFRsError struct {
	Path    string // relative to the repository root; NFRsPath when empty
	Problem string
}

func (e *NFRsError) Error() string {
	if e.Path == "" {
		return NFRsPath + ": " + e.Problem
	}
	return e.Path + ": " + e.Problem
}

// nfrsFile is NFRsPath's JSON form.
type nfrsFile struct {
	NFRs []nfrJSON `json:"nfrs"`
}

type nfrJSON struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Areas       []string `json:"areas"`
	Operation   string   `json:"operation"`
	Load        *struct {
		VUs      int    `json:"vus"`
		Duration string `json:"duration"`
	} `json:"load"`
	Targets *struct {
		P95          *string  `json:"p95"`
		P99          *string  `json:"p99"`
		MaxErrorRate *float64 `json:"maxErrorRate"`
		MinRPS       *float64 `json:"minRps"`
	} `json:"targets"`
}

// LoadNFRs reads the NFRs of the repository at root. A missing file is no
// NFRs.
func LoadNFRs(root string) ([]domain.NFR, error) {
	nfrs, _, err := LoadNFRsFrom(root, NFRsPath)
	return nfrs, err
}

// LoadNFRsFrom reads a file of NFRs in NFRsPath's schema, by its path
// relative to root; exists is false when there is no such file.
func LoadNFRsFrom(root, path string) (nfrs []domain.NFR, exists bool, err error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read NFRs: %w", err)
	}
	nfrs, _, err = parseNFRs(data, path)
	return nfrs, true, err
}

// parseNFRs checks a file of NFRs and returns them with each one's JSON as
// written.
func parseNFRs(data []byte, path string) ([]domain.NFR, []json.RawMessage, error) {
	problem := func(s string) error {
		if path == NFRsPath {
			path = ""
		}
		return &NFRsError{Path: path, Problem: s}
	}
	var raw struct {
		NFRs []json.RawMessage `json:"nfrs"`
	}
	var file nfrsFile
	for _, into := range []any{&file, &raw} {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(into); err != nil {
			return nil, nil, problem("not valid JSON for gf: " + err.Error())
		}
		if dec.More() {
			return nil, nil, problem("not valid JSON for gf: more than one JSON value")
		}
	}
	var nfrs []domain.NFR
	seen := map[string]bool{}
	for i, n := range file.NFRs {
		nfr, err := n.toDomain()
		if err != nil {
			return nil, nil, problem(fmt.Sprintf("nfrs[%d] (%q): %s", i, n.ID, err))
		}
		if seen[n.ID] {
			return nil, nil, problem(fmt.Sprintf("nfrs[%d]: the id %q is used twice", i, n.ID))
		}
		seen[n.ID] = true
		nfrs = append(nfrs, nfr)
	}
	return nfrs, raw.NFRs, nil
}

// toDomain checks one NFR; its error says what is wrong in owner's terms.
func (n nfrJSON) toDomain() (domain.NFR, error) {
	id, err := domain.NewNFRID(n.ID)
	if err != nil {
		return domain.NFR{}, errors.New(`"id" must be letters, digits, '-' or '_', e.g. "wallet-transaction-latency"`)
	}
	if len(n.Areas) == 0 {
		return domain.NFR{}, errors.New(`"areas" must list at least one glob over repository paths, e.g. ["internal/wallet/**"]`)
	}
	var areas []domain.AreaGlob
	for _, a := range n.Areas {
		g, err := domain.NewAreaGlob(a)
		if err != nil {
			return domain.NFR{}, fmt.Errorf(`"areas": %q is not a glob relative to the repository root, e.g. "internal/wallet/**"`, a)
		}
		areas = append(areas, g)
	}
	operation, err := domain.NewOperation(n.Operation)
	if err != nil {
		return domain.NFR{}, fmt.Errorf(`"operation" must be an HTTP method and a path, e.g. "POST /wallets/{id}/transactions", not %q`, n.Operation)
	}
	if n.Load == nil {
		return domain.NFR{}, errors.New(`"load" is required, e.g. {"vus": 50, "duration": "60s"}`)
	}
	duration, err := parseDuration("load.duration", n.Load.Duration)
	if err != nil {
		return domain.NFR{}, err
	}
	if duration < time.Second {
		return domain.NFR{}, fmt.Errorf(`"load.duration" must be at least 1s, k6's shortest run, not %q`, n.Load.Duration)
	}
	load, err := domain.NewLoad(n.Load.VUs, duration)
	if err != nil {
		return domain.NFR{}, errors.New(`"load.vus" must be a positive whole number`)
	}
	if n.Targets == nil {
		return domain.NFR{}, errors.New(`"targets" is required, with at least one of p95, p99, maxErrorRate, minRps`)
	}
	values := domain.TargetValues{MaxErrorRate: n.Targets.MaxErrorRate, MinRPS: n.Targets.MinRPS}
	for _, t := range []struct {
		field string
		in    *string
		out   **time.Duration
	}{{"targets.p95", n.Targets.P95, &values.P95}, {"targets.p99", n.Targets.P99, &values.P99}} {
		if t.in == nil {
			continue
		}
		d, err := parseDuration(t.field, *t.in)
		if err != nil {
			return domain.NFR{}, err
		}
		*t.out = &d
	}
	targets, err := domain.NewTargets(values)
	if err != nil {
		return domain.NFR{}, errors.New(`"targets" needs at least one of p95, p99 (positive durations), maxErrorRate (a fraction from 0 to 1), minRps (a positive number)`)
	}
	return domain.NewNFR(id, n.Description, areas, operation, load, targets)
}

func parseDuration(field, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q must be a positive Go duration such as \"150ms\" or \"60s\", not %q", field, s)
	}
	return d, nil
}

// ErrNoNFRProposals: the design visit proposes no NFRs.
var ErrNoNFRProposals = errors.New("the design proposes no NFRs")

// NFRAdoptionPlan is adopting a design's NFR proposals into NFRsPath: what
// changes, and the file's content after it. NFRs the proposals don't touch
// keep their JSON as the owner wrote it; adopted ones are taken as the
// design wrote them.
type NFRAdoptionPlan struct {
	Adoption domain.NFRAdoption
	content  []byte
}

// PlanNFRAdoption plans adopting the NFRs proposed in a file, by its path
// relative to root, into the repository's NFRs. A missing proposals file is
// ErrNoNFRProposals; invalid NFRs on either side are an *NFRsError.
func PlanNFRAdoption(root string, proposals domain.RepoPath) (NFRAdoptionPlan, error) {
	read := func(path string) ([]byte, bool, error) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return data, err == nil, err
	}
	proposedData, ok, err := read(proposals.String())
	if err != nil {
		return NFRAdoptionPlan{}, fmt.Errorf("read NFR proposals: %w", err)
	}
	if !ok {
		return NFRAdoptionPlan{}, fmt.Errorf("%s: %w", proposals, ErrNoNFRProposals)
	}
	proposed, proposedRaw, err := parseNFRs(proposedData, proposals.String())
	if err != nil {
		return NFRAdoptionPlan{}, err
	}
	var current []domain.NFR
	var currentRaw []json.RawMessage
	if data, ok, err := read(NFRsPath); err != nil {
		return NFRAdoptionPlan{}, fmt.Errorf("read NFRs: %w", err)
	} else if ok {
		if current, currentRaw, err = parseNFRs(data, NFRsPath); err != nil {
			return NFRAdoptionPlan{}, err
		}
	}
	adoption := domain.AdoptNFRs(current, proposed)
	rawOf := func(id domain.NFRID) json.RawMessage {
		for i, n := range proposed {
			if n.ID() == id {
				return proposedRaw[i]
			}
		}
		return nil
	}
	merged := slices.Clone(currentRaw)
	for _, r := range adoption.Replaced() {
		i := slices.IndexFunc(current, func(n domain.NFR) bool { return n.ID() == r.Old.ID() })
		merged[i] = rawOf(r.New.ID())
	}
	for _, n := range adoption.Added() {
		merged = append(merged, rawOf(n.ID()))
	}
	content, err := json.MarshalIndent(struct {
		NFRs []json.RawMessage `json:"nfrs"`
	}{NFRs: merged}, "", "  ")
	if err != nil {
		return NFRAdoptionPlan{}, fmt.Errorf("write NFRs: %w", err)
	}
	if _, _, err := parseNFRs(content, NFRsPath); err != nil {
		return NFRAdoptionPlan{}, err
	}
	return NFRAdoptionPlan{Adoption: adoption, content: append(content, '\n')}, nil
}

// Write writes the adopted NFRs to NFRsPath, creating it if missing. A
// crash leaves the old file or the new one.
func (p NFRAdoptionPlan) Write(root string) error {
	path := filepath.Join(root, filepath.FromSlash(NFRsPath))
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nfrs-*.json")
	if err != nil {
		return fmt.Errorf("write NFRs: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(p.content); err != nil {
		tmp.Close()
		return fmt.Errorf("write NFRs: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write NFRs: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("write NFRs: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write NFRs: %w", err)
	}
	return nil
}
