// Command fakek6 is a test double for `k6 run`, for machines without k6. It
// can't run JavaScript: a script says what to do in comments.
//
//	// fake-k6: POST /wallets/1/transactions   one request per iteration (several allowed)
//	// fake-k6: exit 107                       fail like a script error, sending nothing
//
// It takes the flags gf passes (--vus, --duration, --env BASE_URL=…,
// --out json=<file>[.gz]); each of --vus workers sends the script's
// requests in turn for --duration, timing them, and every request is
// written as k6 writes it: an http_req_duration Point tagged with method,
// url, name, status and scenario "default". The arguments go to
// <out>.args, one per line, followed by any K6_* variable it was given.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "fakek6: only `run` is supported")
		os.Exit(2)
	}
	vus, duration, base, out := 1, time.Second, "", ""
	for i := 1; i < len(args)-1; i++ {
		switch args[i] {
		case "--vus":
			vus, _ = strconv.Atoi(args[i+1])
		case "--duration":
			duration, _ = time.ParseDuration(args[i+1])
		case "--env":
			if v, ok := strings.CutPrefix(args[i+1], "BASE_URL="); ok {
				base = v
			}
		case "--out":
			out = strings.TrimPrefix(args[i+1], "json=")
		}
	}
	script, err := os.ReadFile(args[len(args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakek6:", err)
		os.Exit(107)
	}
	if out != "" {
		recorded := args
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "K6_") {
				recorded = append(recorded, "env "+kv)
			}
		}
		_ = os.WriteFile(out+".args", []byte(strings.Join(recorded, "\n")+"\n"), 0o644)
	}
	type request struct{ method, path string }
	var requests []request
	for _, line := range strings.Split(string(script), "\n") {
		directive, ok := strings.CutPrefix(strings.TrimSpace(line), "// fake-k6: ")
		if !ok {
			continue
		}
		if code, ok := strings.CutPrefix(directive, "exit "); ok {
			n, _ := strconv.Atoi(code)
			fmt.Fprintln(os.Stderr, `level=error msg="Uncaught (in promise) ReferenceError: thisIsNotDefined is not defined"`)
			os.Exit(n)
		}
		method, path, _ := strings.Cut(directive, " ")
		requests = append(requests, request{method, path})
	}

	var w io.Writer = io.Discard
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakek6:", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
		if strings.HasSuffix(out, ".gz") {
			gz := gzip.NewWriter(f)
			defer gz.Close()
			w = gz
		}
	}
	buf := bufio.NewWriter(w)
	defer buf.Flush()
	var mu sync.Mutex
	write := func(method, url string, status int, took time.Duration) {
		line, _ := json.Marshal(map[string]any{
			"metric": "http_req_duration", "type": "Point",
			"data": map[string]any{
				"time": time.Now().Format(time.RFC3339Nano), "value": float64(took) / float64(time.Millisecond),
				"tags": map[string]string{"method": method, "url": url, "name": url, "status": strconv.Itoa(status), "scenario": "default"},
			},
		})
		mu.Lock()
		buf.Write(append(line, '\n'))
		mu.Unlock()
	}
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup
	for range vus {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) && len(requests) > 0 {
				for _, r := range requests {
					url := base + r.path
					req, _ := http.NewRequest(r.method, url, strings.NewReader("{}"))
					start := time.Now()
					resp, err := client.Do(req)
					status := 0
					if err == nil {
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
						status = resp.StatusCode
					}
					write(r.method, url, status, time.Since(start))
				}
			}
		}()
	}
	wg.Wait()
}
