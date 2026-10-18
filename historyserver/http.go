package historyserver

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Config is the server's configuration file.
type Config struct {
	// Listen is the address to listen on, e.g. ":8080". Put TLS in front.
	Listen string `json:"listen"`
	// DB is the SQLite database's path.
	DB string `json:"db"`
	// Projects maps each project's id to the SHA-256 of its token, so the
	// file holds no token.
	Projects map[string]Project `json:"projects"`
}

// Project is a project's tokens, by their SHA-256: the sync token reads
// and writes; each read token only reads (the viewer, auditors).
type Project struct {
	TokenSHA256      string   `json:"tokenSha256"`
	ReadTokenSHA256s []string `json:"readTokenSha256s,omitempty"`
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LoadConfig reads the configuration at path.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if c.Listen == "" || c.DB == "" || len(c.Projects) == 0 {
		return Config{}, fmt.Errorf("%s: set listen, db and at least one project", path)
	}
	for id, p := range c.Projects {
		if !sha256Hex.MatchString(p.TokenSHA256) {
			return Config{}, fmt.Errorf("%s: project %s: tokenSha256 must be the token's SHA-256 in lowercase hex (printf %%s \"$TOKEN\" | shasum -a 256)", path, id)
		}
		for _, r := range p.ReadTokenSHA256s {
			if !sha256Hex.MatchString(r) {
				return Config{}, fmt.Errorf("%s: project %s: each of readTokenSha256s must be a token's SHA-256 in lowercase hex", path, id)
			}
		}
	}
	return c, nil
}

// Limits on what a request may carry.
const (
	maxBatch = 32 << 20
	maxBlob  = 64 << 20
)

// Handler serves the protocol for the configured projects.
func Handler(store *Store, projects map[string]Project, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &handler{store: store, projects: projects, logger: logger}
	mux := http.NewServeMux()
	p := wire.Version + "/projects/{project}"
	mux.HandleFunc("GET "+p+"/streams", h.auth(read, h.streams))
	mux.HandleFunc("GET "+p+"/blobs/{sha256}", h.auth(read, h.getBlob)) // HEAD too
	mux.HandleFunc("PUT "+p+"/blobs/{sha256}", h.auth(write, h.putBlob))
	mux.HandleFunc("POST "+p+"/files", h.auth(write, h.recordFiles))
	mux.HandleFunc("POST "+p+"/events", h.auth(write, h.appendEvents))
	mux.HandleFunc("GET "+p+"/works", h.auth(read, h.works))
	mux.HandleFunc("GET "+p+"/works/{id}", h.auth(read, h.timeline))
	return mux
}

type handler struct {
	store    *Store
	projects map[string]Project
	logger   *slog.Logger
}

// access is what a request needs of its token.
type access int

const (
	read access = iota
	write
)

// auth lets a request through only with a token of its project that may
// do what it needs: the sync token anything, a read token only reads. An
// unknown project is refused like a wrong token, so ids can't be probed.
func (h *handler) auth(need access, next func(w http.ResponseWriter, r *http.Request, project string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project := r.PathValue("project")
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		p, known := h.projects[project]
		sum := sha256.Sum256([]byte(token))
		given := []byte(hex.EncodeToString(sum[:]))
		matches := func(want string) bool { return subtle.ConstantTimeCompare(given, []byte(want)) == 1 }
		writer := ok && known && matches(p.TokenSHA256)
		reader := false
		for _, rt := range p.ReadTokenSHA256s {
			reader = reader || (ok && known && matches(rt))
		}
		switch {
		case !writer && !reader:
			h.problem(w, r, &problemError{status: http.StatusUnauthorized, problem: wire.Problem{Error: wire.ProblemUnauthorized, Message: "unknown project or wrong token"}})
		case need == write && !writer:
			h.problem(w, r, &problemError{status: http.StatusForbidden, problem: wire.Problem{Error: wire.ProblemForbidden, Message: "a read token can't write: gf sync uses the project's sync token"}})
		default:
			next(w, r, project)
		}
	}
}

func (h *handler) streams(w http.ResponseWriter, r *http.Request, project string) {
	heads, err := h.store.Streams(r.Context(), project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, wire.Streams{Streams: heads})
}

func (h *handler) getBlob(w http.ResponseWriter, r *http.Request, project string) {
	sha := r.PathValue("sha256")
	if r.Method == http.MethodHead {
		held, err := h.store.HasBlob(r.Context(), project, sha)
		switch {
		case err != nil:
			h.problem(w, r, err)
		case held:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	content, ok, err := h.store.Blob(r.Context(), project, sha)
	switch {
	case err != nil:
		h.problem(w, r, err)
	case !ok:
		h.problem(w, r, refuse(http.StatusNotFound, wire.ProblemNotFound, "no blob %s", sha))
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(content)
	}
}

func (h *handler) putBlob(w http.ResponseWriter, r *http.Request, project string) {
	sha := r.PathValue("sha256")
	if !sha256Hex.MatchString(sha) {
		h.problem(w, r, refuse(http.StatusBadRequest, wire.ProblemInvalid, "%q is not a SHA-256 in lowercase hex", sha))
		return
	}
	content, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBlob))
	if err != nil {
		h.problem(w, r, refuse(http.StatusRequestEntityTooLarge, wire.ProblemInvalid, "a blob holds at most %d bytes", maxBlob))
		return
	}
	if err := h.store.PutBlob(r.Context(), project, sha, content); err != nil {
		h.problem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) recordFiles(w http.ResponseWriter, r *http.Request, project string) {
	var files wire.Files
	if !h.decode(w, r, &files) {
		return
	}
	out, err := h.store.RecordFiles(r.Context(), project, files.Files)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, out)
}

func (h *handler) appendEvents(w http.ResponseWriter, r *http.Request, project string) {
	var batch wire.Events
	if !h.decode(w, r, &batch) {
		return
	}
	out, err := h.store.AppendEvents(r.Context(), project, batch)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	h.logger.Info("events appended", "project", project, "accepted", out.Accepted, "duplicates", out.Duplicates, "client", batch.Client)
	reply(w, http.StatusOK, out)
}

func (h *handler) works(w http.ResponseWriter, r *http.Request, project string) {
	works, err := h.store.Works(r.Context(), project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, works)
}

func (h *handler) timeline(w http.ResponseWriter, r *http.Request, project string) {
	t, ok, err := h.store.Timeline(r.Context(), project, r.PathValue("id"))
	switch {
	case err != nil:
		h.problem(w, r, err)
	case !ok:
		h.problem(w, r, refuse(http.StatusNotFound, wire.ProblemNotFound, "no work %s", r.PathValue("id")))
	default:
		reply(w, http.StatusOK, t)
	}
}

func (h *handler) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBatch))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		h.problem(w, r, refuse(http.StatusBadRequest, wire.ProblemInvalid, "not a request of the protocol: %v", err))
		return false
	}
	return true
}

func (h *handler) problem(w http.ResponseWriter, r *http.Request, err error) {
	var p *problemError
	if !errors.As(err, &p) {
		h.logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err.Error())
		p = refuse(http.StatusInternalServerError, "internal", "the server failed; its log says why")
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(p.status)
		return
	}
	reply(w, p.status, p.problem)
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
