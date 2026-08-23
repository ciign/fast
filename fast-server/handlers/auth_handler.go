package handlers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fast/config"
	"fmt"
	"github.com/labstack/echo/v4"
	"math/big"
	"strings"
	"time"
)

type ECAuthConfig struct {
	PublicKey      string   `yaml:"public_key"`      // Base64 encoded public key
	AllowedHosts   []string `yaml:"allowed_hosts"`   // Domains that require auth
	TokenLifetime  int      `yaml:"token_lifetime"`  // Token lifetime in seconds
	RequireBrowser bool     `yaml:"require_browser"` // Require Hob browser
}

type AuthToken struct {
	Timestamp int64  `json:"timestamp"`
	Nonce     string `json:"nonce"`
	Host      string `json:"host"`
	Path      string `json:"path"`
	Signature string `json:"signature"` // ECDSA signature
}

// VerifyECDSASignature verifies an ECDSA signature
func VerifyECDSASignature(publicKeyPEM string, message []byte, signature string) (bool, error) {
	// Decode PEM public key
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return false, fmt.Errorf("failed to parse PEM block")
	}

	// Parse the public key
	genericKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("failed to parse public key: %v", err)
	}

	publicKey, ok := genericKey.(*ecdsa.PublicKey)
	if !ok {
		return false, fmt.Errorf("not an ECDSA public key")
	}

	// Decode signature from hex
	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false, fmt.Errorf("failed to decode signature: %v", err)
	}

	// Split signature into r and s components
	if len(sigBytes) != 64 {
		return false, fmt.Errorf("invalid signature length")
	}

	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:])

	// Hash the message
	hash := sha256.Sum256(message)

	// Verify the signature
	return ecdsa.Verify(publicKey, hash[:], r, s), nil
}

// VerifyECToken verifies an EC authentication token
func VerifyECToken(authConfig config.AuthConfig, tokenStr string, c echo.Context) (bool, error) {
	// Decode token from base64
	tokenBytes, err := base64.StdEncoding.DecodeString(tokenStr)
	if err != nil {
		c.Logger().Errorf("Failed to decode token: %v", err)
		return false, fmt.Errorf("invalid token format")
	}

	// Parse token structure
	// Format: timestamp:nonce:host:path:signature
	// The timestamp is RFC3339 and itself contains ':' (e.g. 2026-08-23T14:27:48Z),
	// so split from the RIGHT: the last four fields are nonce/host/path/signature
	// and everything before them is the timestamp. (Splitting into exactly five
	// fields rejected every real token — fixed 2026-08-23.)
	parts := strings.Split(string(tokenBytes), ":")
	if len(parts) < 5 {
		return false, fmt.Errorf("invalid token structure")
	}
	n := len(parts)
	signature := parts[n-1]
	tokenPath := parts[n-2]
	tokenHost := parts[n-3]
	nonce := parts[n-4]
	timestamp := strings.Join(parts[:n-4], ":")

	// Verify timestamp (prevent replay attacks)
	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return false, fmt.Errorf("invalid timestamp format")
	}

	if time.Since(ts) > time.Duration(authConfig.TokenLifetime)*time.Second {
		return false, fmt.Errorf("token expired")
	}

	// Verify host matches
	host := c.Request().Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}

	if tokenHost != host {
		return false, fmt.Errorf("token host mismatch")
	}

	// Construct message for signature verification
	message := fmt.Sprintf("%s:%s:%s:%s", timestamp, nonce, tokenHost, tokenPath)

	// Verify ECDSA signature
	valid, err := VerifyECDSASignature(authConfig.PublicKey, []byte(message), signature)
	if err != nil {
		c.Logger().Errorf("Signature verification error: %v", err)
		return false, fmt.Errorf("signature verification failed: %v", err)
	}

	return valid, nil
}

// AuthMiddleware checks for valid EC token
func AuthMiddleware(authConfig ECAuthConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			host := c.Request().Host
			if strings.Contains(host, ":") {
				host = strings.Split(host, ":")[0]
			}

			// Check if this host requires authentication
			requiresAuth := false
			for _, allowedHost := range authConfig.AllowedHosts {
				if strings.HasSuffix(host, allowedHost) || host == allowedHost {
					requiresAuth = true
					break
				}
			}

			// If no allowed hosts specified, auth is required for this domain
			if len(authConfig.AllowedHosts) == 0 {
				requiresAuth = true
			}

			if !requiresAuth {
				return next(c)
			}

			// Check browser identification if required
			if authConfig.RequireBrowser {
				userAgent := c.Request().Header.Get("User-Agent")
				if !strings.Contains(userAgent, "Hob/") {
					c.Logger().Warnf("Invalid browser for protected site: %s", userAgent)
					return c.JSON(403, map[string]string{
						"error":   "Access denied",
						"message": "Hob browser required to access this site",
					})
				}
			}

			// Get auth token from header or cookie
			tokenStr := c.Request().Header.Get("X-Hob-Token")
			if tokenStr == "" {
				// Try cookie as fallback
				cookie, err := c.Cookie("hob_token")
				if err != nil || cookie.Value == "" {
					c.Logger().Warnf("Missing auth token for protected site")
					return c.JSON(401, map[string]string{
						"error":   "Authentication required",
						"message": "Please authenticate with your EC token",
					})
				}
				tokenStr = cookie.Value
			}

			// Create a temporary auth config from EC auth config
			tempAuthConfig := config.AuthConfig{
				Enabled:        true,
				Type:           "ec",
				PublicKey:      authConfig.PublicKey,
				AllowedHosts:   authConfig.AllowedHosts,
				TokenLifetime:  authConfig.TokenLifetime,
				RequireBrowser: authConfig.RequireBrowser,
			}

			// Verify the EC token
			valid, err := VerifyECToken(tempAuthConfig, tokenStr, c)
			if err != nil || !valid {
				c.Logger().Warnf("Invalid EC token: %v", err)
				return c.JSON(401, map[string]string{
					"error":   "Invalid authentication",
					"message": "Token verification failed",
				})
			}

			// Set authenticated user context
			c.Set("authenticated", true)
			c.Set("auth_host", host)

			c.Logger().Infof("Authenticated access to %s%s", host, c.Request().URL.Path)
			return next(c)
		}
	}
}

// GenerateECKeyPair generates a new ECDSA key pair (for initial setup)
func GenerateECKeyPair() (privateKeyPEM, publicKeyPEM string, err error) {
	// Generate key pair using P-256 curve
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader) // was a fixed string reader: every generated key pair was predictable
	if err != nil {
		return "", "", err
	}

	// Encode private key
	privateKeyBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return "", "", err
	}

	privateKeyBlock := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM = string(pem.EncodeToMemory(privateKeyBlock))

	// Encode public key
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}

	publicKeyBlock := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}
	publicKeyPEM = string(pem.EncodeToMemory(publicKeyBlock))

	return privateKeyPEM, publicKeyPEM, nil
}
