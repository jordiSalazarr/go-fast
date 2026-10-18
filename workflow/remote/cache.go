package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// cachePath is where gf remembers its last sync, under .gofast/runtime/. It
// is a cache: losing it costs requests, never history, since the server's
// stream heads say what it holds.
func cachePath(root string) string {
	return filepath.Join(eventlog.Dir(root), eventlog.RuntimeDir, "sync.json")
}

// cache is what gf knows the server holds, as of its last sync to it.
type cache struct {
	Server   string         `json:"server"`
	Project  string         `json:"project"`
	SyncedAt time.Time      `json:"syncedAt"`
	Streams  map[string]int `json:"streams"`
	// Blobs the server holds, and Files (path → sha256) it was told about.
	Blobs map[string]bool   `json:"blobs"`
	Files map[string]string `json:"files"`
}

// loadCache reads the cache of the configured server and project; another
// server's or project's, or a broken one, is an empty cache.
func loadCache(root string, c Config) cache {
	empty := cache{Server: c.Server.String(), Project: c.Project, Streams: map[string]int{}, Blobs: map[string]bool{}, Files: map[string]string{}}
	data, err := os.ReadFile(cachePath(root))
	if err != nil {
		return empty
	}
	var got cache
	if json.Unmarshal(data, &got) != nil || got.Server != empty.Server || got.Project != empty.Project {
		return empty
	}
	if got.Streams == nil {
		got.Streams = map[string]int{}
	}
	if got.Blobs == nil {
		got.Blobs = map[string]bool{}
	}
	if got.Files == nil {
		got.Files = map[string]string{}
	}
	return got
}

// save writes the cache atomically.
func (k cache) save(root string) error {
	data, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	path := cachePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("save sync cache: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("save sync cache: %w", err)
	}
	return os.Rename(tmp, path)
}

// State is what gf status says about syncing, without asking the server.
type State struct {
	Configured bool
	// Synced is set once a sync to the configured server and project has
	// finished; SyncedAt is when.
	Synced   bool
	SyncedAt time.Time
	// Pending are the events the server didn't hold at the last sync, or
	// all of them before the first.
	Pending int
}

// StateOf is the sync state of the repository at root.
func StateOf(root string, records []eventlog.Recorded) (State, error) {
	c, err := LoadConfig(root)
	if err != nil || !c.Configured {
		return State{}, err
	}
	k := loadCache(root, c)
	state := State{Configured: true, Synced: !k.SyncedAt.IsZero(), SyncedAt: k.SyncedAt}
	for _, r := range records {
		if r.Version > k.Streams[string(r.Stream)] {
			state.Pending++
		}
	}
	return state, nil
}
