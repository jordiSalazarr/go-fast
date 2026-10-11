package exitchecks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// gf measures a stress run from k6's raw output, never from anything the
// script defines. With `k6 run --out json=<file>` (checked with k6 v2.1.0;
// a name ending in .gz is gzipped), every request is a line
//
//	{"metric":"http_req_duration","type":"Point","data":{"time":"…","value":6.687,
//	 "tags":{"method":"POST","url":"http://…/wallets/7/transactions","status":"201","scenario":"default",…}}}
//
// with the duration in milliseconds. Only requests of the load count:
// gf runs k6 with --vus and --duration, whose scenario is "default";
// setup() and teardown() requests have no scenario tag. Only the NFR's
// operation counts, so a script can't pad the run with easy requests. A
// request grouped with a custom `name` tag, or with http.url, has its url tag
// replaced by that name: http.url keeps the path with "${}" for each value,
// which still matches; a custom name doesn't.
//
// A request failed if it got no response (status 0) or a status of 400 or
// more, whatever the script tells k6 to expect (http.setResponseCallback).

// k6Point is one line of k6's JSON output.
type k6Point struct {
	Type   string `json:"type"`
	Metric string `json:"metric"`
	Data   struct {
		Value float64           `json:"value"`
		Tags  map[string]string `json:"tags"`
	} `json:"data"`
}

// measure reads k6's JSON output and measures the operation's requests, sent
// to baseURL during a run of the given duration.
func measure(r io.Reader, op domain.Operation, baseURL string, duration time.Duration) (domain.Measurement, error) {
	var durations []float64 // milliseconds
	failed := 0
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), `"http_req_duration"`) {
			continue
		}
		var p k6Point
		if err := json.Unmarshal(line, &p); err != nil {
			return domain.Measurement{}, fmt.Errorf("read k6 output: %w", err)
		}
		if p.Type != "Point" || p.Metric != "http_req_duration" || p.Data.Tags["scenario"] != "default" {
			continue
		}
		path, ok := requestPath(p.Data.Tags["url"], baseURL)
		if !ok || !op.Matches(p.Data.Tags["method"], path) {
			continue
		}
		durations = append(durations, p.Data.Value)
		if status, err := strconv.Atoi(p.Data.Tags["status"]); err != nil || status == 0 || status >= 400 {
			failed++
		}
	}
	if err := scanner.Err(); err != nil {
		return domain.Measurement{}, fmt.Errorf("read k6 output: %w", err)
	}
	values := domain.MeasurementValues{Requests: len(durations)}
	if len(durations) > 0 {
		slices.Sort(durations)
		values.P95 = milliseconds(percentile(durations, 0.95))
		values.P99 = milliseconds(percentile(durations, 0.99))
		values.ErrorRate = float64(failed) / float64(len(durations))
		values.RPS = float64(len(durations)) / duration.Seconds()
	}
	return domain.NewMeasurement(values)
}

// percentile interpolates linearly between the two closest values, as k6's
// trend metrics do: p(95) of what k6 would report.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	i := p * float64(len(sorted)-1)
	lo, hi := sorted[int(math.Floor(i))], sorted[int(math.Ceil(i))]
	return lo + (hi-lo)*(i-math.Floor(i))
}

func milliseconds(ms float64) time.Duration {
	return time.Duration(math.Round(ms * float64(time.Millisecond)))
}

// requestPath is a request URL's path below baseURL, without its query. A
// URL grouped with http.url starts with "${}" for the base URL.
func requestPath(url, baseURL string) (string, bool) {
	var rest string
	switch base := strings.TrimSuffix(baseURL, "/"); {
	case strings.HasPrefix(url, base):
		rest = url[len(base):]
	case strings.HasPrefix(url, "${}"):
		rest = url[len("${}"):]
	default:
		return "", false
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		rest = "/"
	}
	if !strings.HasPrefix(rest, "/") {
		return "", false // another host whose name starts with the base's
	}
	return rest, true
}
