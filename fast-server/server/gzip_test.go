package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func gzipEcho() *echo.Echo {
	e := echo.New()
	e.Use(middleware.GzipWithConfig(gzipConfig()))
	body := strings.Repeat("egg ", 2048)
	e.GET("/app.js", func(c echo.Context) error { return c.Blob(http.StatusOK, "text/javascript", []byte(body)) })
	e.GET("/clip.mp4", func(c echo.Context) error { return c.Blob(http.StatusOK, "video/mp4", []byte(body)) })
	e.GET("/tiny.js", func(c echo.Context) error { return c.Blob(http.StatusOK, "text/javascript", []byte("ok")) })
	return e
}

func get(e *echo.Echo, path string, acceptGzip bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptGzip {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestGzipTextForClientsThatAcceptIt(t *testing.T) {
	rec := get(gzipEcho(), "/app.js", true)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip, got %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.Len() >= 8192 {
		t.Fatalf("body not compressed: %d bytes", rec.Body.Len())
	}
}

func TestGzipSkipsClientsWithoutAcceptEncoding(t *testing.T) {
	rec := get(gzipEcho(), "/app.js", false)
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.Len() != 8192 {
		t.Fatalf("expected identity body, got %q / %d bytes", rec.Header().Get("Content-Encoding"), rec.Body.Len())
	}
}

func TestGzipSkipsCompressedMedia(t *testing.T) {
	rec := get(gzipEcho(), "/clip.mp4", true)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("media must not be gzipped, got %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestGzipSkipsSmallBodies(t *testing.T) {
	rec := get(gzipEcho(), "/tiny.js", true)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("small body must not be gzipped, got %q", rec.Header().Get("Content-Encoding"))
	}
}
