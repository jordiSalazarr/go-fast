// Command gofast-server is the reference gofast history server: gf sync
// sends it each project's work history, and it serves it back read-only,
// as JSON and as a viewer at /.
//
//	gofast-server -config server.json            serve
//	gofast-server -config server.json -backup f  write a consistent copy of the database to f, and exit
//
// It speaks plain HTTP; put TLS in front of it (deploy/ has an example).
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
	backup := flag.String("backup", "", "write a consistent copy of the database to this file, which must not exist, and exit")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	run := serve
	if *backup != "" {
		run = func(config historyserver.Config, logger *slog.Logger) error { return backupTo(config, *backup, logger) }
	}
	config, err := historyserver.LoadConfig(*configPath)
	if err == nil {
		err = run(config, logger)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gofast-server:", err)
		os.Exit(1)
	}
}

func backupTo(config historyserver.Config, path string, logger *slog.Logger) error {
	store, err := historyserver.Open(config.DB)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Backup(context.Background(), path); err != nil {
		return fmt.Errorf("back up %s to %s: %w", config.DB, path, err)
	}
	logger.Info("backed up", "db", config.DB, "to", path)
	return nil
}

func serve(config historyserver.Config, logger *slog.Logger) error {
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
