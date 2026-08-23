package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func staticSite(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "index.html"), "<html>root spa</html>")
	writeFile(t, filepath.Join(root, "about.html"), "<html>about page</html>")
	writeFile(t, filepath.Join(root, "docs", "index.html"), "<html>docs index</html>")
	writeFile(t, filepath.Join(root, "docs", "guide.html"), "<html>docs guide</html>")
	writeFile(t, filepath.Join(root, "assets", "app.3f9c.js"), "console.log(1)")
	writeFile(t, filepath.Join(root, "robots.txt"), "User-agent: *")
	return root
}

func serveStatic(t *testing.T, root, path string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := ServeIndexOrFile(c, root, path); err != nil {
		e.HTTPErrorHandler(err, c)
	}
	return rec
}

func TestStaticServing(t *testing.T) {
	root := staticSite(t)
	cases := []struct {
		path, wantBody, wantCache string
	}{
		{"/", "root spa", "public, max-age=3600"},
		{"/index.html", "root spa", "public, max-age=3600"},
		{"/robots.txt", "User-agent", ""},
		{"/assets/app.3f9c.js", "console.log", "public, max-age=31536000, immutable"},
		// pre-rendered deep links: directory index and <path>.html beat the SPA fallback
		{"/docs", "docs index", "public, max-age=3600"}, // a directory index.html is an entry point too
		{"/docs/", "docs index", "public, max-age=3600"},
		{"/docs/guide", "docs guide", ""},
		{"/about", "about page", ""},
		// SPA fallback for unknown client-side routes
		{"/settings/profile", "root spa", "public, max-age=3600"},
	}
	for _, tc := range cases {
		rec := serveStatic(t, root, tc.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", tc.path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s: body %q does not contain %q", tc.path, rec.Body.String(), tc.wantBody)
		}
		if got := rec.Header().Get("Cache-Control"); got != tc.wantCache {
			t.Errorf("%s: Cache-Control %q, want %q", tc.path, got, tc.wantCache)
		}
	}
}

func TestStaticTraversalStaysInsidePublicDir(t *testing.T) {
	root := staticSite(t)
	// A secret next to (not inside) the public dir must never be reachable.
	secret := filepath.Join(filepath.Dir(root), "secret-"+filepath.Base(root)+".txt")
	writeFile(t, secret, "TOP SECRET")
	defer os.Remove(secret)
	for _, p := range []string{"/../" + filepath.Base(secret), "/docs/../../" + filepath.Base(secret), "/%2e%2e/" + filepath.Base(secret)} {
		rec := serveStatic(t, root, p)
		if strings.Contains(rec.Body.String(), "TOP SECRET") {
			t.Fatalf("%s escaped the public dir", p)
		}
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("%s: 500", p)
		}
	}
}

func TestStaticWithoutIndexIs404(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "only.txt"), "x")
	if rec := serveStatic(t, root, "/only.txt"); rec.Code != http.StatusOK {
		t.Fatalf("existing file: %d", rec.Code)
	}
	if rec := serveStatic(t, root, "/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("no index.html must be 404, got %d", rec.Code)
	}
}
