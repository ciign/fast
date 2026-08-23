package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"fast/config"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

func TestGetMatchingLocationPrefersLongestPrefix(t *testing.T) {
	locs := []config.Location{
		{Path: "/", Proxy: config.ProxyConfig{Host: "web", Port: 80}},
		{Path: "/api", Proxy: config.ProxyConfig{Host: "api", Port: 80}},
		{Path: "/api/v2", Proxy: config.ProxyConfig{Host: "api2", Port: 80}},
	}
	for path, want := range map[string]string{
		"/":           "web",
		"/index.html": "web",
		"/api":        "api",
		"/api/users":  "api",
		"/api/v2/x":   "api2",
		"/apiary":     "api", // prefix match, as documented by the implementation
	} {
		got := getMatchingLocation(path, locs)
		if got == nil || got.Proxy.Host != want {
			t.Errorf("%s -> %v, want %s", path, got, want)
		}
	}
	if getMatchingLocation("/x", []config.Location{{Path: "/api"}}) != nil {
		t.Error("unmatched path must return nil")
	}
}

func TestIsWebSocketRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	if isWebSocketRequest(r) {
		t.Fatal("plain GET is not a websocket request")
	}
	r.Header.Set("Upgrade", "WebSocket")
	if !isWebSocketRequest(r) {
		t.Fatal("Upgrade: websocket must be detected case-insensitively")
	}
}

func upstreamDomain(t *testing.T, rawurl string, path string) config.Domain {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	return config.Domain{Name: "test.local", Type: "proxy", Locations: []config.Location{{
		Path: path, Proxy: config.ProxyConfig{Host: u.Hostname(), Port: port, Protocol: "http"},
	}}}
}

func TestProxyForwardsHeadersAndPath(t *testing.T) {
	var seen http.Header
	var seenPath, seenHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		seenPath = r.URL.RequestURI()
		seenHost = r.Host
		w.Header().Set("Content-Length", "2")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/items?x=1", strings.NewReader(`{"a":1}`))
	req.Host = "app.example.com"
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.7:40000"
	rec := httptest.NewRecorder()
	if err := HandleProxy(e.NewContext(req, rec), upstreamDomain(t, upstream.URL, "/")); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusCreated || rec.Body.String() != "ok" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if seenPath != "/api/items?x=1" {
		t.Fatalf("upstream path %q", seenPath)
	}
	if seenHost != "app.example.com" {
		t.Fatalf("upstream Host must be the original host, got %q", seenHost)
	}
	for k, want := range map[string]string{
		"X-Forwarded-Host":  "app.example.com",
		"X-Forwarded-Proto": "http",
		"X-Real-Ip":         "198.51.100.7",
		"X-Original-Uri":    "/api/items",
		"Content-Type":      "application/json",
	} {
		if got := seen.Get(k); got != want {
			t.Errorf("upstream header %s = %q, want %q", k, got, want)
		}
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("Content-Length must be stripped (streaming responses)")
	}
	if got := rec.Header().Get("Cross-Origin-Opener-Policy"); got != "same-origin-allow-popups" {
		t.Errorf("COOP = %q", got)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Error("X-Accel-Buffering: no expected")
	}
}

func TestProxyUnmatchedLocationIs404(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/elsewhere", nil)
	rec := httptest.NewRecorder()
	err := HandleProxy(e.NewContext(req, rec), config.Domain{Name: "t", Type: "proxy",
		Locations: []config.Location{{Path: "/api", Proxy: config.ProxyConfig{Host: "127.0.0.1", Port: 1, Protocol: "http"}}}})
	if err != echo.ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestProxyStreamsSSEWithoutBuffering(t *testing.T) {
	// The upstream flushes two events a moment apart; the proxy must deliver
	// the first before the second exists (no buffering of the whole body).
	firstFlushed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: one\n\n")
		fl.Flush()
		<-firstFlushed
		fmt.Fprint(w, "data: two\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	e := echo.New()
	dom := upstreamDomain(t, upstream.URL, "/")
	e.Any("/*", func(c echo.Context) error { return HandleProxy(c, dom) })
	front := httptest.NewServer(e)
	defer front.Close()

	resp, err := http.Get(front.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	done := make(chan string, 1)
	go func() { n, _ := resp.Body.Read(buf); done <- string(buf[:n]) }()
	select {
	case got := <-done:
		if !strings.Contains(got, "data: one") {
			t.Fatalf("first chunk %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first SSE event was not delivered before the upstream finished: proxy is buffering")
	}
	close(firstFlushed)
}

func TestWebSocketProxyEndToEnd(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var sawHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost = r.Host
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.WriteMessage(mt, append([]byte("echo:"), msg...))
		}
	}))
	defer upstream.Close()

	e := echo.New()
	dom := upstreamDomain(t, upstream.URL, "/")
	e.Any("/*", func(c echo.Context) error { return HandleProxy(c, dom) })
	front := httptest.NewServer(e)
	defer front.Close()

	wsURL := "ws" + strings.TrimPrefix(front.URL, "http") + "/voice/stream?token=abc"
	hdr := http.Header{"Host": []string{"voice.example.com"}}
	client, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial through proxy: %v (resp %v)", err, resp)
	}
	defer client.Close()
	for i := 0; i < 3; i++ {
		msg := fmt.Sprintf("hello %d", i)
		if err := client.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		client.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, got, err := client.ReadMessage()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if string(got) != "echo:"+msg {
			t.Fatalf("got %q", got)
		}
	}
	if sawHost == "" {
		t.Fatal("upstream never saw the websocket request")
	}
}
