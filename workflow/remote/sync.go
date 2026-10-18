package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Server is what a sync needs from the history server.
type Server interface {
	Streams(ctx context.Context) (map[string]wire.StreamHead, error)
	HasBlob(ctx context.Context, sha256 string) (bool, error)
	PutBlob(ctx context.Context, sha256 string, content []byte) error
	RecordFiles(ctx context.Context, files []wire.File) (wire.FilesRecorded, error)
	SendEvents(ctx context.Context, batch wire.Events) (wire.EventsAccepted, error)
}

// Options tune a sync.
type Options struct {
	// DryRun asks the server what it holds, and sends nothing.
	DryRun bool
	// Client is gf's build, sent with the events.
	Client string
	// BatchSize is how many events or files go in one request; 500 when 0.
	BatchSize int
	Now       func() time.Time
}

// Report is what a sync did, or with DryRun would do.
type Report struct {
	DryRun bool
	// Events sent and accepted; Duplicates the server held already (an
	// interrupted sync's events).
	Events, Duplicates int
	// Files named; Uploaded blobs, and Held the server held already.
	Files, Uploaded, Held int
	Refused               []Refusal
	Skipped               []Skip
}

// RefusedError: some streams were not synced. The rest were.
type RefusedError struct{ Refused []Refusal }

func (e *RefusedError) Error() string {
	return fmt.Sprintf("%d streams were not synced", len(e.Refused))
}

// Sync sends what the server doesn't hold yet of the repository's history:
// the blobs first, then the files that name them, then the events, so the
// server never holds an event whose files are missing. A stream the server
// refuses is reported, and the others still sync. It returns a
// *RefusedError, with the report, when any stream was refused.
func Sync(ctx context.Context, root string, c Config, decisions domain.DecisionsDir, log Log, server Server, opts Options) (Report, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	report := Report{DryRun: opts.DryRun}
	heads, err := server.Streams(ctx)
	if err != nil {
		return report, err
	}
	events, refused := eventsToSend(log, heads)
	report.Refused = refused
	files, skipped, err := filesToSend(root, c, log, decisions)
	if err != nil {
		return report, err
	}
	report.Skipped = skipped
	k := loadCache(root, c)

	if err := sendFiles(ctx, server, files, &k, &report, opts); err != nil {
		var problem *ProblemError
		if !errors.As(err, &problem) || problem.Problem.Error != wire.ProblemMissingBlob {
			return report, err
		}
		// The server lost blobs the cache says it holds: ask it again.
		k.Blobs, k.Files = map[string]bool{}, map[string]string{}
		report.Files, report.Uploaded, report.Held = 0, 0, 0
		if err := sendFiles(ctx, server, files, &k, &report, opts); err != nil {
			return report, err
		}
	}
	if opts.DryRun {
		report.Events = len(events)
		return report, refusedErr(report)
	}

	streams := map[string]int{}
	for s, h := range heads {
		streams[s] = h.Version
	}
	for len(events) > 0 {
		n := min(opts.BatchSize, len(events))
		batch := wire.Events{Seal: log.Seal, Client: opts.Client}
		for _, e := range events[:n] {
			batch.Events = append(batch.Events, e.line)
		}
		accepted, err := server.SendEvents(ctx, batch)
		var problem *ProblemError
		if errors.As(err, &problem) && problem.Status == http.StatusConflict && problem.Problem.Stream != "" {
			// The server refused the whole batch because of one stream:
			// leave that stream out, and send the rest again.
			left := without(events, problem.Problem.Stream)
			if len(left) == len(events) {
				return report, err // a stream this sync didn't send: nothing to leave out
			}
			report.Refused = append(report.Refused, Refusal{Stream: problem.Problem.Stream, Version: problem.Problem.Version, Reason: problem.Problem.Message})
			events = left
			continue
		}
		if err != nil {
			return report, err
		}
		report.Events += accepted.Accepted
		report.Duplicates += accepted.Duplicates
		for _, e := range events[:n] {
			streams[e.stream] = max(streams[e.stream], e.version)
		}
		events = events[n:]
		k.Streams = streams
		if err := k.save(root); err != nil {
			return report, err
		}
	}
	k.Streams, k.SyncedAt = streams, opts.Now().UTC()
	if err := k.save(root); err != nil {
		return report, err
	}
	return report, refusedErr(report)
}

func refusedErr(r Report) error {
	if len(r.Refused) > 0 {
		return &RefusedError{Refused: r.Refused}
	}
	return nil
}

func without(events []pending, stream string) []pending {
	var out []pending
	for _, e := range events {
		if e.stream != stream {
			out = append(out, e)
		}
	}
	return out
}

// sendFiles uploads the blobs the server lacks, then names the files the
// server wasn't told about. With DryRun it only asks.
func sendFiles(ctx context.Context, server Server, files []localFile, k *cache, report *Report, opts Options) error {
	var named []wire.File
	for _, f := range files {
		if k.Files[f.Path] == f.SHA256 {
			continue
		}
		named = append(named, f.File)
		if k.Blobs[f.SHA256] {
			continue
		}
		held, err := server.HasBlob(ctx, f.SHA256)
		if err != nil {
			return err
		}
		if held {
			report.Held++
			k.Blobs[f.SHA256] = true
			continue
		}
		report.Uploaded++
		if opts.DryRun {
			continue
		}
		content, err := os.ReadFile(f.abs)
		if err != nil {
			return err
		}
		if err := server.PutBlob(ctx, f.SHA256, content); err != nil {
			return err
		}
		k.Blobs[f.SHA256] = true
	}
	report.Files = len(named)
	if opts.DryRun {
		return nil
	}
	for len(named) > 0 {
		n := min(opts.BatchSize, len(named))
		if _, err := server.RecordFiles(ctx, named[:n]); err != nil {
			return err
		}
		for _, f := range named[:n] {
			k.Files[f.Path] = f.SHA256
		}
		named = named[n:]
	}
	return nil
}
