// Command walletserver is a service for stress-run tests: it listens on
// -addr, answers /healthz, and posts wallet transactions taking -delay each.
// With -crash it exits at once.
package main

import (
	"flag"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	delay := flag.Duration("delay", 0, "time each transaction takes")
	crash := flag.Bool("crash", false, "exit at once")
	flag.Parse()
	if *crash {
		os.Exit(3)
	}
	http.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	http.HandleFunc("POST /wallets/{id}/transactions", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(*delay)
		w.WriteHeader(http.StatusCreated)
	})
	if err := http.ListenAndServe(*addr, nil); err != nil {
		os.Exit(1)
	}
}
