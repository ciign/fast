package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func adminCtx(remote, token string) echo.Context {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/admin/api/generate-keys", nil)
	req.RemoteAddr = remote
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	return e.NewContext(req, httptest.NewRecorder())
}

func TestAdminAuthorization(t *testing.T) {
	t.Setenv("FAST_ADMIN_TOKEN", "s3cret")
	if !isAdminAuthorized(adminCtx("127.0.0.1:5555", "")) {
		t.Fatal("localhost IPv4 must be allowed")
	}
	if !isAdminAuthorized(adminCtx("[::1]:5555", "")) {
		t.Fatal("localhost IPv6 must be allowed")
	}
	if !isAdminAuthorized(adminCtx("203.0.113.9:5555", "s3cret")) {
		t.Fatal("correct admin token must be allowed")
	}
	if isAdminAuthorized(adminCtx("203.0.113.9:5555", "wrong")) {
		t.Fatal("wrong admin token accepted")
	}
	if isAdminAuthorized(adminCtx("203.0.113.9:5555", "")) {
		t.Fatal("remote without token accepted")
	}
	t.Setenv("FAST_ADMIN_TOKEN", "")
	if isAdminAuthorized(adminCtx("203.0.113.9:5555", "")) {
		t.Fatal("empty configured token must not match an empty header")
	}
}

func TestAdminEndpointsRejectUnauthorized(t *testing.T) {
	t.Setenv("FAST_ADMIN_TOKEN", "s3cret")
	for name, h := range map[string]echo.HandlerFunc{
		"generate-keys": HandleGenerateKeys,
		"domain-auth":   HandleUpdateDomainAuth,
		"add-domain":    HandleAddDomain,
		"test-auth":     HandleTestAuthentication,
	} {
		c := adminCtx("203.0.113.9:5555", "")
		err := h(c)
		he, ok := err.(*echo.HTTPError)
		if !ok || he.Code != http.StatusForbidden {
			t.Errorf("%s: unauthorized request got %v, want 403", name, err)
		}
	}
}
