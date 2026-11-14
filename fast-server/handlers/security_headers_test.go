package handlers

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestApplySecurityHeadersFillsOnlyWhatIsUnset(t *testing.T) {
	h := http.Header{}
	h.Set("Referrer-Policy", "same-origin")
	ApplySecurityHeaders(h, true)
	if got := h.Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("upstream Referrer-Policy replaced: %q", got)
	}
	if got := h.Values("Referrer-Policy"); len(got) != 1 {
		t.Fatalf("duplicate Referrer-Policy: %v", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := h.Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("Strict-Transport-Security = %q", got)
	}
}

func TestNoHSTSOverPlainHTTP(t *testing.T) {
	h := http.Header{}
	ApplySecurityHeaders(h, false)
	if got := h.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS sent over plain HTTP: %q", got)
	}
}

func TestForwardedProtoDoesNotCountAsHTTPS(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if RequestIsHTTPS(e.NewContext(req, httptest.NewRecorder())) {
		t.Fatal("a client-written X-Forwarded-Proto was trusted")
	}
	req.TLS = &tls.ConnectionState{}
	if !RequestIsHTTPS(e.NewContext(req, httptest.NewRecorder())) {
		t.Fatal("a TLS connection was not seen as HTTPS")
	}
}

func TestStaticResponsesCarryTheBaseHeaders(t *testing.T) {
	root := staticSite(t)
	for _, path := range []string{"/", "/about", "/assets/app.3f9c.js", "/no-such-route"} {
		rec := serveStatic(t, root, path)
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := rec.Header().Get("Referrer-Policy"); got == "" {
			t.Fatalf("%s: no Referrer-Policy", path)
		}
	}
}

// The address handed to upstreams is the connection's, whatever the request
// claims about itself.
func TestClientAddressIgnoresForwardingHeaders(t *testing.T) {
	e := echo.New()
	e.IPExtractor = echo.ExtractIPDirect()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:4444"
	req.Header.Set("X-Forwarded-For", "10.9.8.7")
	req.Header.Set("X-Real-IP", "10.9.8.6")
	if got := e.NewContext(req, httptest.NewRecorder()).RealIP(); got != "203.0.113.7" {
		t.Fatalf("RealIP = %q", got)
	}
}
