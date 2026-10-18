package historyserver

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// viewerFiles are the viewer's page, script and style: plain files, no
// build step.
//
//go:embed viewer
var viewerFiles embed.FS

// contentSecurityPolicy lets the page load only its own script, style and
// API; no inline script, no frames, no other origin.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// secured adds the security headers to every response.
func secured(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func viewerHandler() http.Handler {
	sub, err := fs.Sub(viewerFiles, "viewer")
	if err != nil {
		panic(err) // the files are embedded at build time
	}
	return http.FileServerFS(sub)
}

// markdown renders CommonMark with GitHub's tables and strikethrough. It
// leaves raw HTML out and drops dangerous links (javascript:, …): an
// artifact's author can't run script in the viewer.
var markdown = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))

// Rendered is a Markdown blob as HTML.
type Rendered struct {
	HTML string `json:"html"`
}

// renderBlob serves a Markdown blob rendered to HTML, for the viewer.
func (h *handler) renderBlob(w http.ResponseWriter, r *http.Request, project string) {
	content, ok, err := h.store.Blob(r.Context(), project, r.PathValue("sha256"))
	switch {
	case err != nil:
		h.problem(w, r, err)
		return
	case !ok:
		h.problem(w, r, refuse(http.StatusNotFound, wire.ProblemNotFound, "no blob %s", r.PathValue("sha256")))
		return
	case !utf8.Valid(content):
		h.problem(w, r, refuse(http.StatusUnprocessableEntity, wire.ProblemInvalid, "the blob isn't text"))
		return
	}
	var out bytes.Buffer
	if err := markdown.Convert(content, &out); err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, Rendered{HTML: out.String()})
}
