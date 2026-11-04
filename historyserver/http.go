package historyserver

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Limits on what a request may carry.
const (
	maxBatch = 32 << 20
	maxBlob  = 64 << 20
)

// Handler serves the protocol for the organisations' members, and for
// the projects they create.
func Handler(store *Store, organisations map[string]Organisation, logger *slog.Logger) (http.Handler, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	m, err := newMembers(organisations)
	if err != nil {
		return nil, err
	}
	h := &handler{store: store, members: m, logger: logger}
	mux := http.NewServeMux()
	p := wire.Version + "/projects/{project}"
	mux.HandleFunc("GET "+p+"/streams", h.syncs(h.streams))
	mux.HandleFunc("GET "+p+"/blobs/{sha256}", h.blob) // HEAD too
	mux.HandleFunc("PUT "+p+"/blobs/{sha256}", h.syncs(h.putBlob))
	mux.HandleFunc("POST "+p+"/files", h.syncs(h.recordFiles))
	mux.HandleFunc("POST "+p+"/events", h.syncs(h.appendEvents))
	mux.HandleFunc("GET "+p+"/works", h.inspects(h.works))
	mux.HandleFunc("GET "+p+"/works/{id}", h.inspects(h.timeline))
	mux.HandleFunc("PUT "+p+"/works/{id}/assessment", h.inspects(h.assess))
	mux.HandleFunc("PUT "+p+"/works/{id}/visits/{stage}/{visit}/assessment", h.inspects(h.assess))
	mux.HandleFunc("GET "+p+"/assessments", h.inspects(h.assessments))
	mux.HandleFunc("GET "+p+"/troubles", h.inspects(h.troubles))
	mux.HandleFunc("GET "+p+"/blobs/{sha256}/html", h.inspects(h.renderBlob))
	mux.HandleFunc("GET "+p+"/instructions", h.inspects(h.instructions))
	mux.HandleFunc("GET "+p+"/instructions/{from}/diff/{to}", h.inspects(h.diff))
	mux.HandleFunc("POST "+p+"/secret", h.inspects(h.rotateSecret))
	mux.HandleFunc("POST "+p+"/skill-improvements", h.inspects(h.proposeSkillImprovement))
	mux.HandleFunc("POST "+p+"/skill-improvements/{id}/revise", h.inspects(h.reviseSkillImprovement))
	mux.HandleFunc("POST "+p+"/skill-improvements/{id}/approve", h.inspects(h.approveSkillImprovement))
	mux.HandleFunc("POST "+p+"/skill-improvements/{id}/withdraw", h.inspects(h.withdrawSkillImprovement))
	mux.HandleFunc("GET "+p+"/skill-improvements", h.inspects(h.skillImprovements))
	mux.HandleFunc("GET "+p+"/skill-improvements/{id}", h.inspects(h.skillImprovement))
	// The one read the project secret makes: what its clones may apply.
	mux.HandleFunc("GET "+p+"/skill-improvements/approved", h.syncs(h.approvedSkillImprovements))
	mux.HandleFunc("POST "+wire.Version+"/projects", h.asMember(h.createProject))
	mux.HandleFunc("GET "+wire.Version+"/me", h.asMember(h.me))
	viewer := viewerHandler()
	mux.Handle("GET /{$}", viewer)
	mux.Handle("GET /viewer/", http.StripPrefix("/viewer", viewer))
	return secured(mux), nil
}

type handler struct {
	store   *Store
	members members
	logger  *slog.Logger
}

// credential is who a bearer token is: a member, by their token, or a
// project, by its secret; both empty for nobody.
type credential struct {
	project string
	member  memberOf
}

func (h *handler) credential(r *http.Request) (credential, error) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		return credential{}, nil
	}
	hash := sha256Of(token)
	if m, ok := h.members[hash]; ok {
		return credential{member: m}, nil
	}
	project, _, err := h.store.projectOfSecret(r.Context(), hash)
	return credential{project: project}, err
}

// syncs lets a request through only with its project's secret. Another
// project's secret, and an unknown project, are refused like a wrong
// secret, so ids can't be probed.
func (h *handler) syncs(next func(w http.ResponseWriter, r *http.Request, project string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project := r.PathValue("project")
		c, err := h.credential(r)
		switch {
		case err != nil:
			h.problem(w, r, err)
		case c.project != "" && c.project == project:
			next(w, r, project)
		case c.member.name != "":
			h.problem(w, r, refuse(http.StatusForbidden, wire.ProblemForbidden, "a member's token can't sync: gf sync uses the project's secret"))
		default:
			h.problem(w, r, refuse(http.StatusUnauthorized, wire.ProblemUnauthorized, "unknown project or wrong secret"))
		}
	}
}

// asMember lets a request through only with a member's token.
func (h *handler) asMember(next func(w http.ResponseWriter, r *http.Request, m memberOf)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := h.credential(r)
		switch {
		case err != nil:
			h.problem(w, r, err)
		case c.project != "":
			h.problem(w, r, refuse(http.StatusForbidden, wire.ProblemForbidden, "the project secret only syncs: read with a member's token"))
		case c.member.name == "":
			h.problem(w, r, refuse(http.StatusUnauthorized, wire.ProblemUnauthorized, "unknown token: sign in with your member token"))
		default:
			next(w, r, c.member)
		}
	}
}

// inspects lets a request through only with the token of a member of the
// project's organisation. To a member, another organisation's project
// looks like no project.
func (h *handler) inspects(next func(w http.ResponseWriter, r *http.Request, project, member string)) http.HandlerFunc {
	return h.asMember(func(w http.ResponseWriter, r *http.Request, m memberOf) {
		project := r.PathValue("project")
		org, err := h.store.organisationOf(r.Context(), project)
		switch {
		case err != nil:
			h.problem(w, r, err)
		case org != m.org:
			h.problem(w, r, refuse(http.StatusNotFound, wire.ProblemNotFound, "organisation %s has no project %s", m.org, project))
		default:
			next(w, r, project, m.name)
		}
	})
}

// blob is HEAD, which gf sync asks, and GET, which members read.
func (h *handler) blob(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		h.syncs(h.hasBlob)(w, r)
		return
	}
	h.inspects(h.getBlob)(w, r)
}

// me is who the member is, and their organisation's projects.
func (h *handler) me(w http.ResponseWriter, r *http.Request, m memberOf) {
	projects, err := h.store.Projects(r.Context(), m.org)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, wire.Me{Member: m.name, Organisation: m.org, Projects: projects})
}

// createProject makes a project in the member's organisation, and shows
// its secret, once.
func (h *handler) createProject(w http.ResponseWriter, r *http.Request, m memberOf) {
	var p wire.NewProject
	if !h.decode(w, r, &p) {
		return
	}
	out, err := h.store.CreateProject(r.Context(), m.org, p.ID, m.name)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	h.logger.Info("project created", "project", out.Project, "organisation", m.org, "member", m.name)
	reply(w, http.StatusCreated, out)
}

// rotateSecret gives the project a new secret, and shows it, once.
func (h *handler) rotateSecret(w http.ResponseWriter, r *http.Request, project, member string) {
	out, err := h.store.RotateSecret(r.Context(), project, member)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	h.logger.Info("project secret rotated", "project", project, "member", member)
	reply(w, http.StatusOK, out)
}

func (h *handler) streams(w http.ResponseWriter, r *http.Request, project string) {
	heads, err := h.store.Streams(r.Context(), project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, wire.Streams{Streams: heads})
}

func (h *handler) hasBlob(w http.ResponseWriter, r *http.Request, project string) {
	held, err := h.store.HasBlob(r.Context(), project, r.PathValue("sha256"))
	switch {
	case err != nil:
		h.problem(w, r, err)
	case held:
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *handler) getBlob(w http.ResponseWriter, r *http.Request, project, _ string) {
	sha := r.PathValue("sha256")
	content, ok, err := h.store.Blob(r.Context(), project, sha)
	switch {
	case err != nil:
		h.problem(w, r, err)
	case !ok:
		h.problem(w, r, refuse(http.StatusNotFound, wire.ProblemNotFound, "no blob %s", sha))
	default:
		// Never rendered by the browser: text as plain text, anything else
		// as bytes.
		contentType := "application/octet-stream"
		if utf8.Valid(content) {
			contentType = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", "inline")
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

func (h *handler) works(w http.ResponseWriter, r *http.Request, project, _ string) {
	works, err := h.store.Works(r.Context(), project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	q := r.URL.Query()
	reply(w, http.StatusOK, FilterWorks(works, WorkFilter{Status: q.Get("status"), Type: q.Get("type"), Branch: q.Get("branch"), Query: q.Get("q")}))
}

func (h *handler) timeline(w http.ResponseWriter, r *http.Request, project, _ string) {
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

// instructions lists the skills and agents the history names, or, with
// ?name=, one's versions and the stage visits each instructed.
func (h *handler) instructions(w http.ResponseWriter, r *http.Request, project, _ string) {
	name := r.URL.Query().Get("name")
	if name == "" {
		names, err := h.store.InstructionNames(r.Context(), project)
		if err != nil {
			h.problem(w, r, err)
			return
		}
		reply(w, http.StatusOK, names)
		return
	}
	out, ok, err := h.store.Instructions(r.Context(), project, name)
	switch {
	case err != nil:
		h.problem(w, r, err)
	case !ok:
		h.problem(w, r, refuse(http.StatusNotFound, wire.ProblemNotFound, "the history names no skill or agent %q", name))
	default:
		reply(w, http.StatusOK, out)
	}
}

func (h *handler) diff(w http.ResponseWriter, r *http.Request, project, _ string) {
	from, to := r.PathValue("from"), r.PathValue("to")
	if !sha256Hex.MatchString(from) || !sha256Hex.MatchString(to) {
		h.problem(w, r, refuse(http.StatusBadRequest, wire.ProblemInvalid, "versions are SHA-256s in lowercase hex"))
		return
	}
	out, ok, err := h.store.Diff(r.Context(), project, from, to)
	switch {
	case err != nil:
		h.problem(w, r, err)
	case !ok:
		h.problem(w, r, refuse(http.StatusNotFound, wire.ProblemNotFound, "the server holds the files of neither version"))
	default:
		reply(w, http.StatusOK, out)
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
