package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"fast/config"
)

// The server itself must be built with the direct extractor: a spoofed
// X-Forwarded-For must not become the client address upstreams see.
func TestServerDoesNotTrustClientForwardingHeaders(t *testing.T) {
	s := New(&config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:4444"
	req.Header.Set("X-Forwarded-For", "10.9.8.7")
	if got := s.echo.NewContext(req, httptest.NewRecorder()).RealIP(); got != "203.0.113.7" {
		t.Fatalf("RealIP = %q", got)
	}
}
