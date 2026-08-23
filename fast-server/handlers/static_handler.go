package handlers

import (
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
	switch {
	case strings.HasSuffix(path, "index.html"):
		c.Response().Header().Set("Cache-Control", "public, max-age=3600")
	case strings.Contains(filepath.ToSlash(path), "/assets/"):
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
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

	// If file doesn't exist or is a directory, serve the root index.html
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
