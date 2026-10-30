package handlers

import (
	"crypto/tls"
	"fast/config"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

func getMatchingLocation(path string, locations []config.Location) *config.Location {
	// Sort locations by path length in descending order (longest first)
	sortedLocations := make([]config.Location, len(locations))
	copy(sortedLocations, locations)
	sort.Slice(sortedLocations, func(i, j int) bool {
		return len(sortedLocations[i].Path) > len(sortedLocations[j].Path)
	})

	// Find the first matching location
	for i, loc := range sortedLocations {
		if strings.HasPrefix(path, loc.Path) {
			return &sortedLocations[i]
		}
	}

	return nil
}

// isWebSocketRequest checks if the request is a WebSocket upgrade request
func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket"
}

// handleWebSocketProxy handles proxying of WebSocket connections using gorilla/websocket
func handleWebSocketProxy(c echo.Context, location *config.Location) error {
	proxyConfig := location.Proxy
	targetScheme := proxyConfig.Protocol
	if targetScheme == "" {
		targetScheme = "http"
	}

	// For WebSockets, if the target is https, we need to use wss instead
	wsScheme := "ws"
	if targetScheme == "https" {
		wsScheme = "wss"
	}

	// Build the target URL
	targetURL := fmt.Sprintf("%s://%s:%d%s",
		wsScheme,
		proxyConfig.Host,
		proxyConfig.Port,
		c.Request().URL.Path)

	if c.Request().URL.RawQuery != "" {
		targetURL += "?" + c.Request().URL.RawQuery
	}

	c.Logger().Infof("Proxying WebSocket connection to: %s", targetURL)

	// Create a dialer for connecting to the backend
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: proxyConfig.InsecureSkipVerify,
		},
		HandshakeTimeout: 10 * time.Second,
	}

	// Create new headers for the backend request
	requestHeader := make(http.Header)

	// Copy all non-WebSocket headers
	for k, v := range c.Request().Header {
		k = strings.ToLower(k)
		if k != "upgrade" && k != "connection" && k != "sec-websocket-key" &&
			k != "sec-websocket-version" && k != "sec-websocket-extensions" {
			for _, val := range v {
				requestHeader.Add(k, val)
			}
		}
	}

	// Copy WebSocket protocol if specified
	if proto := c.Request().Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		// Use the Subprotocols field
		for _, p := range strings.Split(proto, ",") {
			dialer.Subprotocols = append(dialer.Subprotocols, strings.TrimSpace(p))
		}
	}

	// CRITICAL: Set the Host header to the ORIGINAL host, not the backend host
	// This preserves the domain information that the backend needs for routing
	originalHost := c.Request().Host
	requestHeader.Set("Host", originalHost) // THIS IS THE KEY FIX

	// Add proxy headers
	requestHeader.Set("X-Forwarded-Host", originalHost)
	requestHeader.Set("X-Real-IP", c.RealIP())
	requestHeader.Set("X-Forwarded-For", c.RealIP())

	sourceScheme := "http"
	if c.Request().TLS != nil {
		sourceScheme = "https"
	}
	requestHeader.Set("X-Forwarded-Proto", sourceScheme)
	requestHeader.Set("X-Original-URI", c.Request().URL.Path)

	// Keep or set Origin header
	if origin := c.Request().Header.Get("Origin"); origin != "" {
		requestHeader.Set("Origin", origin)
	} else {
		requestHeader.Set("Origin", sourceScheme+"://"+originalHost)
	}

	// Upgrader for the client connection
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins
		},
	}

	// Upgrade the client connection
	clientConn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		c.Logger().Errorf("Failed to upgrade client connection: %v", err)
		return echo.ErrInternalServerError
	}
	defer clientConn.Close()

	// Connect to the backend
	c.Logger().Infof("Connecting to backend with host header: %s", requestHeader.Get("Host"))
	backendConn, resp, err := dialer.Dial(targetURL, requestHeader)
	if err != nil {
		c.Logger().Errorf("Failed to connect to backend WebSocket: %v", err)
		if resp != nil {
			errorMsg := fmt.Sprintf("Backend error: %d %s", resp.StatusCode, resp.Status)
			clientConn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, errorMsg))
		}
		return nil
	}
	defer backendConn.Close()

	c.Logger().Info("WebSocket connections established, beginning proxy")

	// Create channels for the proxy
	clientDone := make(chan struct{})
	backendDone := make(chan struct{})

	// Copy messages from client to backend
	go func() {
		defer close(clientDone)
		for {
			messageType, message, err := clientConn.ReadMessage()
			if err != nil {
				c.Logger().Debugf("Client closed connection: %v", err)
				break
			}

			err = backendConn.WriteMessage(messageType, message)
			if err != nil {
				c.Logger().Errorf("Error writing to backend: %v", err)
				break
			}
		}
		// Signal the backend to close with a normal closure
		backendConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}()

	// Copy messages from backend to client
	go func() {
		defer close(backendDone)
		for {
			messageType, message, err := backendConn.ReadMessage()
			if err != nil {
				c.Logger().Debugf("Backend closed connection: %v", err)
				break
			}

			err = clientConn.WriteMessage(messageType, message)
			if err != nil {
				c.Logger().Errorf("Error writing to client: %v", err)
				break
			}
		}
		// Signal the client to close with a normal closure
		clientConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}()

	// Wait for either connection to finish
	select {
	case <-clientDone:
		c.Logger().Debug("Client connection closed first")
	case <-backendDone:
		c.Logger().Debug("Backend connection closed first")
	}

	c.Logger().Info("WebSocket proxy connection terminated")
	return nil
}

// Upstream transports are SHARED per target, never per request.
//
// Until 2026-08-23 HandleProxy built a fresh *http.Transport for every request
// with IdleConnTimeout 0. After each response the upstream connection sat idle
// in a pool nobody would ever use again (the next request made a new
// Transport), so every proxied request leaked one ESTABLISHED connection and
// one ephemeral port on both ends. The public-facing instance exhausted its
// ~28k ephemeral ports after ~36k requests (~42 h) and answered 502 "cannot
// assign requested address" until restarted; the inner instance was holding
// 28,558 ESTABLISHED sockets. One transport per target, with an idle timeout,
// is what keep-alive was always meant to be.
var transportCache sync.Map // key: scheme|host|port|insecure -> *http.Transport

// transportKey identifies the upstream a transport may be reused for.
func transportKey(scheme string, p config.ProxyConfig) string {
	return fmt.Sprintf("%s|%s|%d|%t", scheme, p.Host, p.Port, p.InsecureSkipVerify)
}

// getTransport returns the shared transport for an upstream target, creating
// it on first use. Streaming-friendly settings are kept (no response-header
// timeout, keep-alives on, large buffers); idle connections now expire.
func getTransport(scheme string, p config.ProxyConfig) *http.Transport {
	key := transportKey(scheme, p)
	if t, ok := transportCache.Load(key); ok {
		return t.(*http.Transport)
	}
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS13,
			InsecureSkipVerify: p.InsecureSkipVerify, // Using the config setting
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,              // default is 2: anything above that was closed after use
		IdleConnTimeout:       90 * time.Second, // idle keep-alives die; streams in flight are unaffected
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 0, // streaming: the upstream may take long before the first byte
		DisableCompression:    false,
		DisableKeepAlives:     false,
		ReadBufferSize:        64 * 1024,
		WriteBufferSize:       64 * 1024,
	}
	actual, _ := transportCache.LoadOrStore(key, t)
	return actual.(*http.Transport)
}

func HandleProxy(c echo.Context, domain config.Domain) error {
	requestPath := c.Request().URL.Path
	c.Logger().Infof("Original request path: %s", requestPath)

	// Get matching location
	location := getMatchingLocation(requestPath, domain.Locations)
	if location == nil {
		c.Logger().Errorf("No matching location found for path: %s", requestPath)
		return echo.ErrNotFound
	}

	c.Logger().Infof("Matched location path: %s", location.Path)

	// Check if this is a WebSocket request and handle it separately.
	// Skip manual WebSocket proxying for mirror subdomains — the relay
	// handles WS upgrades itself via its own axum WebSocket support.
	host := c.Request().Host
	isMirror := strings.Contains(host, ".mirror.")
	if isWebSocketRequest(c.Request()) && !isMirror {
		c.Logger().Info("Detected WebSocket request, handling with WebSocket proxy")
		return handleWebSocketProxy(c, location)
	}

	// Use location's proxy config for normal HTTP requests
	proxyConfig := location.Proxy
	targetScheme := proxyConfig.Protocol
	if targetScheme == "" {
		targetScheme = "http"
	}

	target, err := url.Parse(fmt.Sprintf("%s://%s:%d", targetScheme, proxyConfig.Host, proxyConfig.Port))
	if err != nil {
		c.Logger().Errorf("Error parsing proxy URL: %v", err)
		return echo.ErrInternalServerError
	}

	originalHost := c.Request().Host

	// Create rewrite map based on location path
	rewriteMap := make(map[string]string)
	if location.Path == "/" {
		rewriteMap["/*"] = "/$1"
	} else {
		rewriteMap[location.Path+"/*"] = location.Path + "/$1"
	}

	c.Logger().Infof("Using rewrite map: %v", rewriteMap)
	c.Logger().Infof("Proxying %s request from %s%s to %s%s",
		c.Request().Method, originalHost, requestPath, target.String(), requestPath)

	transport := getTransport(targetScheme, proxyConfig)

	sourceScheme := "http"
	if c.Request().TLS != nil {
		sourceScheme = "https"
	}

	// Compression happens once, at this edge: the gzip middleware in server.go
	// wraps the proxied response, so the upstream must send an identity body.
	// When the upstream is another fast (eggrun.ai → frontend-eggrun) and the
	// client's Accept-Encoding were forwarded, it would gzip too and the edge
	// would gzip the gzip — browsers decode one layer and render the inner
	// gzip bytes as text (eggrun.ai, 2026-10-30). Go's transport adds its own
	// Accept-Encoding: gzip and transparently decodes, which is fine: the body
	// reaches the middleware plain, and Content-Encoding is dropped with it.
	c.Request().Header.Del("Accept-Encoding")

	proxyMiddleware := middleware.ProxyWithConfig(middleware.ProxyConfig{
		Balancer: middleware.NewRoundRobinBalancer([]*middleware.ProxyTarget{
			{
				URL: target,
			},
		}),
		Rewrite:   rewriteMap,
		Transport: transport,
		ModifyResponse: func(res *http.Response) error {
			c.Logger().Infof("Proxy response status: %d for path: %s", res.StatusCode, requestPath)

			// Always treat as streaming
			res.Header.Del("Content-Length")          // Remove content length to allow streaming
			res.Header.Set("X-Accel-Buffering", "no") // Disable nginx buffering

			// The edge gzip middleware adds its own Vary: Accept-Encoding; the
			// upstream's copy (an inner fast) would only duplicate it.
			res.Header.Del("Vary")

			// Set headers for CORS and COOP for Google OAuth and other services
			res.Header.Del("Cross-Origin-Opener-Policy")
			res.Header.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")

			return nil
		},
	})

	// Set proxy headers
	c.Request().Header.Set("X-Forwarded-Host", originalHost)
	c.Request().Header.Set("X-Real-IP", c.RealIP())
	c.Request().Header.Set("X-Forwarded-For", c.RealIP())
	c.Request().Header.Set("X-Forwarded-Proto", sourceScheme)
	c.Request().Header.Set("X-Original-URI", requestPath)
	c.Request().Host = originalHost

	return proxyMiddleware(func(c echo.Context) error {
		return nil
	})(c)
}
