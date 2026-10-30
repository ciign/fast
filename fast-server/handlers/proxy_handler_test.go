package handlers

import (
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"fast/config"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// The upstream transport must be one object per target, not one per request:
// a per-request transport leaks its keep-alive connection (and an ephemeral
// port) on every call. This is the regression that took ai.onaccelerate.com
// down with 502 "cannot assign requested address" on 2026-08-23.
func TestGetTransportIsSharedPerTarget(t *testing.T) {
	a := config.ProxyConfig{Host: "127.0.0.1", Port: 18080, Protocol: "http"}
	b := config.ProxyConfig{Host: "127.0.0.1", Port: 18081, Protocol: "http"}

	t1 := getTransport("http", a)
	t2 := getTransport("http", a)
	t3 := getTransport("http", b)
	t4 := getTransport("https", a)

	if t1 != t2 {
		t.Fatalf("same target must share one transport")
	}
	if t1 == t3 || t1 == t4 {
		t.Fatalf("different targets must not share a transport")
	}
	if t1.IdleConnTimeout <= 0 {
		t.Fatalf("idle connections must expire (IdleConnTimeout=%v)", t1.IdleConnTimeout)
	}
	if t1.MaxIdleConnsPerHost < 10 {
		t.Fatalf("MaxIdleConnsPerHost too small to reuse under concurrency: %d", t1.MaxIdleConnsPerHost)
	}
	if t1.DisableKeepAlives {
		t.Fatalf("keep-alives must stay enabled")
	}
}

// Drive HandleProxy like production does and count how many TCP connections
// the upstream actually receives. With a shared keep-alive transport, 50
// sequential requests reuse a handful of connections; the old per-request
// transport opened one per request (50) and never closed them.
func TestProxyReusesUpstreamConnections(t *testing.T) {
	var newConns int32
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "ok %s", r.URL.Path)
	}))
	upstream.Config.ConnState = func(c net.Conn, st http.ConnState) {
		if st == http.StateNew {
			atomic.AddInt32(&newConns, 1)
		}
	}
	upstream.Start()
	defer upstream.Close()

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	domain := config.Domain{
		Name: "test.local",
		Locations: []config.Location{{
			Path:  "/",
			Proxy: config.ProxyConfig{Host: u.Hostname(), Port: port, Protocol: "http"},
		}},
	}

	e := echo.New()
	const n = 50
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/health?i=%d", i), nil)
		req.Host = "test.local"
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := HandleProxy(c, domain); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d body %q", i, rec.Code, rec.Body.String())
		}
	}

	got := atomic.LoadInt32(&newConns)
	if got > 5 {
		t.Fatalf("upstream saw %d new connections for %d sequential requests: connections are not being reused (leak)", got, n)
	}
	t.Logf("%d requests over %d upstream connection(s)", n, got)
}

// A gzip-capable upstream (another fast) must not compress: the edge gzip
// middleware does that once. Forwarding the client's Accept-Encoding made both
// layers gzip and browsers rendered the inner gzip bytes (eggrun.ai 2026-10-30).
func TestProxyUpstreamGetsIdentityBodyAndEdgeGzipsOnce(t *testing.T) {
	body := strings.Repeat("egg ", 2048)
	upstream := httptest.NewServer(gzipUpstream(body))
	defer upstream.Close()

	e := echo.New()
	e.Use(middleware.GzipWithConfig(middleware.GzipConfig{Level: 6, MinLength: 1024}))
	dom := upstreamDomain(t, upstream.URL, "/")
	e.Any("/*", func(c echo.Context) error { return HandleProxy(c, dom) })

	req := httptest.NewRequest(http.MethodGet, "http://eggrun.ai/", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Accept-Encoding" {
		t.Fatalf("expected a single Vary: Accept-Encoding, got %v", got)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("edge must gzip, got %q", rec.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("one gzip layer must yield the page, got %v", err)
	}
	if string(plain) != body {
		t.Fatalf("decoded body is not the page (double-gzipped?): %d bytes", len(plain))
	}
}

// gzipUpstream behaves like a fast static instance: it gzips when asked.
func gzipUpstream(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Add("Vary", "Accept-Encoding")
			zw := gzip.NewWriter(w)
			_, _ = zw.Write([]byte(body))
			_ = zw.Close()
			return
		}
		_, _ = w.Write([]byte(body))
	})
}
