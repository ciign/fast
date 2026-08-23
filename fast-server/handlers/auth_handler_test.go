package handlers

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fast/config"

	"github.com/labstack/echo/v4"
)

func mustKeyPair(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	privPEM, pubPEM, err := GenerateECKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		t.Fatal("private key PEM did not decode")
	}
	priv, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pubPEM
}

// Sign the way a client (the Hob browser) does: sha256 over
// "timestamp:nonce:host:path", r||s as 64 hex bytes, token = base64 of the
// five colon-separated fields.
func signToken(t *testing.T, priv *ecdsa.PrivateKey, ts time.Time, host, path string) string {
	t.Helper()
	stamp := ts.UTC().Format(time.RFC3339)
	nonce := "n0nce"
	msg := fmt.Sprintf("%s:%s:%s:%s", stamp, nonce, host, path)
	h := sha256.Sum256([]byte(msg))
	r, s, err := ecdsa.Sign(rand.Reader, priv, h[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	raw := fmt.Sprintf("%s:%s:%s:%s:%s", stamp, nonce, host, path, hex.EncodeToString(sig))
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func TestGenerateECKeyPairIsRandomAndParses(t *testing.T) {
	p1, pub1 := mustKeyPair(t)
	p2, pub2 := mustKeyPair(t)
	if pub1 == pub2 || p1.D.Cmp(p2.D) == 0 {
		t.Fatal("two generated key pairs are identical — key generation is not using real entropy")
	}
	if !strings.Contains(pub1, "BEGIN PUBLIC KEY") {
		t.Fatalf("public key PEM: %q", pub1[:40])
	}
}

func TestVerifyECToken(t *testing.T) {
	priv, pub := mustKeyPair(t)
	other, _ := mustKeyPair(t)
	cfg := config.AuthConfig{Enabled: true, Type: "ec", PublicKey: pub, TokenLifetime: 60}
	e := echo.New()
	ctx := func(host string) echo.Context {
		req := httptest.NewRequest(http.MethodGet, "/secure", nil)
		req.Host = host
		return e.NewContext(req, httptest.NewRecorder())
	}

	ok, err := VerifyECToken(cfg, signToken(t, priv, time.Now(), "protected.example.com", "/secure"), ctx("protected.example.com:443"))
	if err != nil || !ok {
		t.Fatalf("valid token rejected: ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyECToken(cfg, signToken(t, priv, time.Now().Add(-2*time.Minute), "protected.example.com", "/secure"), ctx("protected.example.com")); ok {
		t.Fatal("expired token accepted")
	}
	if ok, _ := VerifyECToken(cfg, signToken(t, priv, time.Now(), "other.example.com", "/secure"), ctx("protected.example.com")); ok {
		t.Fatal("token for another host accepted")
	}
	if ok, _ := VerifyECToken(cfg, signToken(t, other, time.Now(), "protected.example.com", "/secure"), ctx("protected.example.com")); ok {
		t.Fatal("token signed by a different key accepted")
	}
	if ok, err := VerifyECToken(cfg, "not base64!!", ctx("protected.example.com")); ok || err == nil {
		t.Fatal("garbage token accepted")
	}
	if ok, err := VerifyECToken(cfg, base64.StdEncoding.EncodeToString([]byte("a:b:c")), ctx("protected.example.com")); ok || err == nil {
		t.Fatal("malformed token accepted")
	}
}

func TestAuthMiddleware(t *testing.T) {
	priv, pub := mustKeyPair(t)
	mw := AuthMiddleware(ECAuthConfig{PublicKey: pub, TokenLifetime: 60, AllowedHosts: []string{"protected.example.com"}})
	okHandler := func(c echo.Context) error { return c.String(http.StatusOK, "secret content") }
	e := echo.New()
	do := func(host, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		if mutate != nil {
			mutate(req)
		}
		rec := httptest.NewRecorder()
		if err := mw(okHandler)(e.NewContext(req, rec)); err != nil {
			e.HTTPErrorHandler(err, e.NewContext(req, rec))
		}
		return rec
	}

	if rec := do("protected.example.com", "/x", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := do("protected.example.com", "/x", func(r *http.Request) {
		r.Header.Set("X-Hob-Token", signToken(t, priv, time.Now(), "protected.example.com", "/x"))
	}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "secret content") {
		t.Fatalf("header token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("protected.example.com", "/x", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "hob_token", Value: signToken(t, priv, time.Now(), "protected.example.com", "/x")})
	}); rec.Code != http.StatusOK {
		t.Fatalf("cookie token: %d", rec.Code)
	}
	if rec := do("protected.example.com", "/x", func(r *http.Request) {
		r.Header.Set("X-Hob-Token", signToken(t, priv, time.Now().Add(-5*time.Minute), "protected.example.com", "/x"))
	}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token: %d", rec.Code)
	}
	// a host outside allowed_hosts passes through without a token
	if rec := do("public.example.com", "/x", nil); rec.Code != http.StatusOK {
		t.Fatalf("host not in allowed_hosts must pass: %d", rec.Code)
	}

	browserMW := AuthMiddleware(ECAuthConfig{PublicKey: pub, TokenLifetime: 60, RequireBrowser: true})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "protected.example.com"
	req.Header.Set("X-Hob-Token", signToken(t, priv, time.Now(), "protected.example.com", "/x"))
	rec := httptest.NewRecorder()
	_ = browserMW(okHandler)(e.NewContext(req, rec))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("require_browser without Hob UA: %d", rec.Code)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 Hob/1.2")
	rec = httptest.NewRecorder()
	_ = browserMW(okHandler)(e.NewContext(req, rec))
	if rec.Code != http.StatusOK {
		t.Fatalf("require_browser with Hob UA: %d", rec.Code)
	}
}
