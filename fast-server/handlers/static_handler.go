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
