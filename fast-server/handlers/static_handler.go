package handlers

import (
	"fast/config"
	"github.com/labstack/echo/v4"
	"os"
	"path/filepath"
	"strings"
)

// setCacheControl picks a caching policy by file kind so SPA redeploys are
// picked up promptly while content-hashed assets stay cached:
//   - index.html: the only unhashed entry point (it names the current asset
//     hashes), so it must not be cached indefinitely. A 1h max-age means a
//     redeploy propagates within the hour without revalidating every request.
//   - /assets/*: content-hashed build output — the filename changes whenever
//     the bytes change, so it's safe to cache for a year, immutable.
func setCacheControl(c echo.Context, path string) {
	// Every static response passes through here, so this is also where it
	// gets the domain's own headers and then the base security headers.
	if domain, ok := c.Get("domain").(config.Domain); ok {
		ApplyDomainHeaders(c.Response().Header(), domain.Headers)
	}
	ApplySecurityHeaders(c.Response().Header(), RequestIsHTTPS(c))
	switch {
	case strings.HasSuffix(path, "index.html"):
		c.Response().Header().Set("Cache-Control", "public, max-age=3600")
	case strings.Contains(filepath.ToSlash(path), "/assets/"):
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
}

// fileExtensions are the extensions a static site's own files have. A
// client-side route may carry a dot of its own (a version, a tag), so only
// these mark a path as naming a file.
var fileExtensions = map[string]bool{
	".json": true, ".txt": true, ".xml": true, ".md": true, ".map": true,
	".js": true, ".mjs": true, ".css": true, ".wasm": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".ico": true, ".webp": true, ".avif": true, ".bmp": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".mp4": true, ".webm": true, ".mp3": true, ".pdf": true, ".zip": true,
	".webmanifest": true,
}

// namesAFile reports whether a request path asks for a file rather than a
// page: it is under /.well-known/, or its last segment ends in one of
// fileExtensions.
func namesAFile(requestPath string) bool {
	clean := filepath.ToSlash(filepath.Clean("/" + requestPath))
	if strings.HasPrefix(clean, "/.well-known/") {
		return true
	}
	return fileExtensions[strings.ToLower(filepath.Ext(clean))]
}

func ServeIndexOrFile(c echo.Context, publicDir, requestPath string) error {
	// Convert publicDir to absolute path
	absPublicDir, err := filepath.Abs(publicDir)
	if err != nil {
		c.Logger().Errorf("Failed to get absolute path for public dir: %v", err)
		return echo.ErrInternalServerError
	}

	// Clean and join the paths using absolute path
	fullPath := filepath.Join(absPublicDir, filepath.Clean(requestPath))

	// Double-check for directory traversal using absolute path
	if !strings.HasPrefix(fullPath, absPublicDir) {
		c.Logger().Warnf("Attempted directory traversal detected: %s", fullPath)
		return echo.ErrNotFound
	}

	if stat, err := os.Stat(fullPath); err == nil && !stat.IsDir() {
		c.Logger().Infof("Serving file: %s", fullPath)
		setCacheControl(c, fullPath)
		return c.File(fullPath)
	}

	// Pre-rendered sites (Docusaurus docs/learn): a directory URL serves its
	// own index.html, and /foo may be /foo.html — so crawlers and fetchers get
	// the page's real SSR HTML instead of the SPA root. Only then fall back.
	if stat, err := os.Stat(fullPath); err == nil && stat.IsDir() {
		dirIndex := filepath.Join(fullPath, "index.html")
		if st, err := os.Stat(dirIndex); err == nil && !st.IsDir() {
			c.Logger().Infof("Serving directory index: %s", dirIndex)
			setCacheControl(c, dirIndex)
			return c.File(dirIndex)
		}
	}
	if htmlPath := fullPath + ".html"; strings.HasPrefix(htmlPath, absPublicDir) {
		if st, err := os.Stat(htmlPath); err == nil && !st.IsDir() {
			c.Logger().Infof("Serving html file: %s", htmlPath)
			setCacheControl(c, htmlPath)
			return c.File(htmlPath)
		}
	}

	// A path that names a file (by its extension, or anything under
	// /.well-known/) and has none is not a client-side route: answer 404
	// rather than the app's page with a 200, which a crawler or a tool
	// fetching ard.json or security.txt would take for the file itself.
	if namesAFile(requestPath) {
		return echo.ErrNotFound
	}

	// Any other path with no file is a client-side route: serve the root index.html
	indexPath := filepath.Join(absPublicDir, "index.html")

	// Verify index.html exists
	if _, err := os.Stat(indexPath); err != nil {
		c.Logger().Errorf("index.html not found at: %s", indexPath)
		return echo.ErrNotFound
	}

	c.Logger().Infof("Serving index.html: %s", indexPath)
	setCacheControl(c, indexPath)
	return c.File(indexPath)
}
