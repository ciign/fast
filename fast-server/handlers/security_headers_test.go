package handlers

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"fast/config"

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

func TestApplyDomainHeadersFillsOnlyWhatIsUnset(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Security-Policy", "default-src 'none'")
	ApplyDomainHeaders(h, map[string]string{
		"Content-Security-Policy": "default-src 'self'",
		"Permissions-Policy":      "camera=()",
	})
	if got := h.Get("Content-Security-Policy"); got != "default-src 'none'" {
		t.Fatalf("a header the response already had was replaced: %q", got)
	}
	if got := h.Values("Content-Security-Policy"); len(got) != 1 {
		t.Fatalf("duplicate Content-Security-Policy: %v", got)
	}
	if got := h.Get("Permissions-Policy"); got != "camera=()" {
		t.Fatalf("Permissions-Policy = %q", got)
	}
	ApplyDomainHeaders(h, nil)
}

// A domain's headers beat the base defaults, and the base defaults still fill
// what the domain did not name.
func TestDomainHeadersBeatBaseDefaults(t *testing.T) {
	h := http.Header{}
	ApplyDomainHeaders(h, map[string]string{"Referrer-Policy": "no-referrer"})
	ApplySecurityHeaders(h, true)
	if got := h.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

// Every static answer of a domain — the SPA root, a prerendered page, a
// hashed asset, the fallback for an unknown route — carries its headers.
func TestStaticResponsesCarryDomainHeaders(t *testing.T) {
	root := staticSite(t)
	domain := config.Domain{Name: "example.com", Type: "static", PublicDir: root, Headers: map[string]string{
		"Content-Security-Policy-Report-Only": "default-src 'self'",
	}}
	e := echo.New()
	for _, path := range []string{"/", "/docs", "/assets/app.3f9c.js", "/no-such-route"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.Set("domain", domain)
		if err := ServeIndexOrFile(c, root, path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := rec.Header().Get("Content-Security-Policy-Report-Only"); got != "default-src 'self'" {
			t.Fatalf("%s: Content-Security-Policy-Report-Only = %q", path, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s: base headers lost: X-Content-Type-Options = %q", path, got)
		}
	}
	// A domain without headers is served as before.
	if rec := serveStatic(t, root, "/"); rec.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatal("a header appeared on a domain that configured none")
	}
}

// A proxied answer carries the domain's headers where the upstream set none,
// and keeps the upstream's own where it did.
func TestProxyResponsesCarryDomainHeadersUnlessUpstreamSet(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/own" {
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	domain := upstreamDomain(t, upstream.URL, "/")
	domain.Headers = map[string]string{"Content-Security-Policy": "default-src 'self'"}
	e := echo.New()
	e.Any("/*", func(c echo.Context) error { return HandleProxy(c, domain) })

	for path, want := range map[string]string{"/": "default-src 'self'", "/own": "default-src 'none'"} {
		req := httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		if got := rec.Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != want {
			t.Fatalf("%s: Content-Security-Policy = %v, want %q", path, got, want)
		}
	}
}
