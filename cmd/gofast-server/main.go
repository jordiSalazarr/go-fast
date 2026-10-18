// Command gofast-server is the reference gofast history server: gf sync
// sends it each project's work history, and it serves it back read-only.
//
//	gofast-server -config server.json
//
// It speaks plain HTTP; put TLS in front of it (a reverse proxy).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jordiSalazarr/go-fast/historyserver"
)

func main() {
	configPath := flag.String("config", "server.json", "the server's configuration file")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*configPath, logger); err != nil {
		fmt.Fprintln(os.Stderr, "gofast-server:", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *slog.Logger) error {
	config, err := historyserver.LoadConfig(configPath)
	if err != nil {
		return err
	}
	store, err := historyserver.Open(config.DB)
	if err != nil {
		return err
	}
	defer store.Close()
	srv := &http.Server{
		Addr:              config.Listen,
		Handler:           historyserver.Handler(store, config.Projects, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	logger.Info("listening", "addr", config.Listen, "db", config.DB, "projects", len(config.Projects))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
