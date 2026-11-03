// Command gofast-server is the reference gofast history server: gf sync
// sends it each project's work history, and it serves it to the members of
// the project's organisation, who assess it there, as JSON and as a viewer
// at /.
//
//	gofast-server -config server.json            serve
//	gofast-server -config server.json -backup f  write a consistent copy of the database to f, and exit
//
// With $GOFAST_SERVER_CONFIG set, it reads the configuration from it, as
// JSON, instead of from the file: for hosts that set environment variables
// but mount no files, like Railway. Without "listen" in the configuration
// it listens on $PORT, or on :8080.
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
	config, err := loadConfig(*configPath, os.Getenv)
	if err == nil {
		err = run(config, logger)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gofast-server:", err)
		os.Exit(1)
	}
}

// configEnv holds the whole configuration, as JSON, when set.
const configEnv = "GOFAST_SERVER_CONFIG"

func loadConfig(path string, getenv func(string) string) (historyserver.Config, error) {
	var config historyserver.Config
	var err error
	if data := getenv(configEnv); data != "" {
		config, err = historyserver.ParseConfig([]byte(data), "$"+configEnv)
	} else {
		config, err = historyserver.LoadConfig(path)
	}
	if err == nil && config.Listen == "" {
		config.Listen = ":8080"
		if port := getenv("PORT"); port != "" {
			config.Listen = ":" + port
		}
	}
	return config, err
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
	handler, err := historyserver.Handler(store, config.Organisations, logger)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              config.Listen,
		Handler:           handler,
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
	logger.Info("listening", "addr", config.Listen, "db", config.DB, "organisations", len(config.Organisations))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
