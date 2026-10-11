package exitchecks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// NFRsPath is the repository's non-functional requirements, relative to the
// repository root. Like ConfigPath, the owner writes and commits it, and it
// sits outside every artifact directory, so agents can read it but not
// change it.
const NFRsPath = ".gofast/nfrs.json"

// NFRsError says what is wrong with NFRsPath.
type NFRsError struct{ Problem string }

func (e *NFRsError) Error() string { return NFRsPath + ": " + e.Problem }

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
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(NFRsPath)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read NFRs: %w", err)
	}
	var file nfrsFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, &NFRsError{Problem: "not valid JSON for gf: " + err.Error()}
	}
	if dec.More() {
		return nil, &NFRsError{Problem: "not valid JSON for gf: more than one JSON value"}
	}
	var nfrs []domain.NFR
	seen := map[string]bool{}
	for i, n := range file.NFRs {
		nfr, err := n.toDomain()
		if err != nil {
			return nil, &NFRsError{Problem: fmt.Sprintf("nfrs[%d] (%q): %s", i, n.ID, err)}
		}
		if seen[n.ID] {
			return nil, &NFRsError{Problem: fmt.Sprintf("nfrs[%d]: the id %q is used twice", i, n.ID)}
		}
		seen[n.ID] = true
		nfrs = append(nfrs, nfr)
	}
	return nfrs, nil
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
