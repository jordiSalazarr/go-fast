package historyserver_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/historyserver"
)

func fetch(t *testing.T, u, bearer string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	require.NoError(t, err)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

func TestViewer_IsServedWithStrictSecurityHeaders(t *testing.T) {
	_, _, base := serve(t, token)
	for path, contentType := range map[string]string{
		"/":                          "text/html",
		"/viewer/app.js":             "text/javascript",
		"/viewer/app.css":            "text/css",
		"/v1/projects/wallets/works": "application/json",
	} {
		resp, body := fetch(t, base+path, "r3ad")
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.Contains(t, resp.Header.Get("Content-Type"), contentType, path)
		assert.Equal(t, "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
			resp.Header.Get("Content-Security-Policy"), path)
		assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"), path)
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), path)
		assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"), path)
		if path == "/" {
			assert.Contains(t, body, `<script src="/viewer/app.js" defer></script>`)
			assert.NotContains(t, body, "<script>", "no inline script: the CSP would block it")
		}
	}
	resp, _ := fetch(t, base+"/viewer/missing.js", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestViewer_RendersMarkdown_WithoutRawHTMLOrDangerousLinks(t *testing.T) {
	client, _, base := serve(t, token)
	doc := "# Decision\n\n<script>alert(1)</script>\n\n[ok](https://example.com) [bad](javascript:alert(1)) <img src=x onerror=alert(1)>\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	require.NoError(t, client.PutBlob(context.Background(), hash(doc), []byte(doc)))

	var rendered historyserver.Rendered
	get(t, base+"/v1/projects/wallets/blobs/"+hash(doc)+"/html", &rendered)
	assert.Contains(t, rendered.HTML, "<h1>Decision</h1>")
	assert.Contains(t, rendered.HTML, `<a href="https://example.com">ok</a>`)
	assert.Contains(t, rendered.HTML, "<table>")
	for _, unsafe := range []string{"<script", "javascript:", "onerror", "<img"} {
		assert.NotContains(t, rendered.HTML, unsafe)
	}

	resp, body := fetch(t, base+"/v1/projects/wallets/blobs/"+hash(doc), token)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"), "a blob is never rendered by the browser")
	assert.True(t, strings.HasPrefix(body, "# Decision"))
}
