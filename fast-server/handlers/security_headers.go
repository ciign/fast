package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// baseSecurityHeaders are the response headers every site behind this server
// gets when its own response did not choose them. They are the ones that are
// right for any site: nothing here decides who may frame a page or which
// scripts it may load — a site's own upstream sets those.
var baseSecurityHeaders = [][2]string{
	{"X-Content-Type-Options", "nosniff"},
	{"Referrer-Policy", "strict-origin-when-cross-origin"},
}

// hstsValue pins HTTPS for a year. Deliberately without includeSubDomains:
// one server fronts many unrelated domains, and a subdomain served elsewhere
// over plain HTTP must not be broken by its parent's header.
const hstsValue = "max-age=31536000"

// RequestIsHTTPS reports whether the client reached this server over TLS.
// Only the connection itself counts: X-Forwarded-Proto is the client's to
// write when this server is the edge.
func RequestIsHTTPS(c echo.Context) bool {
	return c.Request().TLS != nil
}

// ApplySecurityHeaders sets the base security headers on h wherever h has no
// value of its own, plus HSTS when the request arrived over TLS.
func ApplySecurityHeaders(h http.Header, https bool) {
	for _, kv := range baseSecurityHeaders {
		if h.Get(kv[0]) == "" {
			h.Set(kv[0], kv[1])
		}
	}
	if https && h.Get("Strict-Transport-Security") == "" {
		h.Set("Strict-Transport-Security", hstsValue)
	}
}

// ApplyDomainHeaders sets the headers a domain's configuration asks for on h,
// each only where h has no value of its own. It runs before
// ApplySecurityHeaders so a domain's choice beats the base defaults, and after
// the upstream has answered so the upstream's choice beats the domain's.
func ApplyDomainHeaders(h http.Header, headers map[string]string) {
	for name, value := range headers {
		if h.Get(name) == "" {
			h.Set(name, value)
		}
	}
}
