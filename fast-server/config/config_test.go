package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func validSSL() SSLConfig { return SSLConfig{CertFile: "/etc/ssl/a.pem", KeyFile: "/etc/ssl/a.key"} }

func TestProxyDomainDefaults(t *testing.T) {
	d := Domain{Name: "api.example.com", Type: "proxy", Proxy: ProxyConfig{Host: "backend", Port: 12000}, SSL: validSSL()}
	d.setDefaults()
	if d.Proxy.Protocol != "http" {
		t.Fatalf("protocol default: %q", d.Proxy.Protocol)
	}
	if len(d.Locations) != 1 || d.Locations[0].Path != "/" || d.Locations[0].Proxy.Host != "backend" {
		t.Fatalf("a proxy domain without locations must get a '/' location with the domain proxy: %+v", d.Locations)
	}
	if err := d.validate(); err != nil {
		t.Fatalf("valid proxy domain rejected: %v", err)
	}
}

func TestProxyLocationsKeepTheirOwnUpstreams(t *testing.T) {
	d := Domain{Name: "x", Type: "proxy", SSL: validSSL(), Locations: []Location{
		{Path: "/", Proxy: ProxyConfig{Host: "web", Port: 80}},
		{Path: "/api", Proxy: ProxyConfig{Host: "api", Port: 8080, Protocol: "https"}},
	}}
	d.setDefaults()
	if d.Locations[0].Proxy.Protocol != "http" || d.Locations[1].Proxy.Protocol != "https" {
		t.Fatalf("per-location protocol defaults wrong: %+v", d.Locations)
	}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDomainValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		d    Domain
		want string
	}{
		{"empty name", Domain{Type: "static", PublicDir: "/www", SSL: validSSL()}, "domain name cannot be empty"},
		{"bad type", Domain{Name: "a", Type: "ftp", PublicDir: "/www", SSL: validSSL()}, "invalid domain type"},
		{"static without public_dir", Domain{Name: "a", Type: "static", SSL: validSSL()}, "public_dir is required"},
		{"proxy bad port", Domain{Name: "a", Type: "proxy", SSL: validSSL(), Locations: []Location{{Path: "/", Proxy: ProxyConfig{Host: "b", Port: 70000, Protocol: "http"}}}}, "invalid proxy port"},
		{"proxy empty host", Domain{Name: "a", Type: "proxy", SSL: validSSL(), Locations: []Location{{Path: "/", Proxy: ProxyConfig{Port: 80, Protocol: "http"}}}}, "proxy host cannot be empty"},
		{"proxy bad protocol", Domain{Name: "a", Type: "proxy", SSL: validSSL(), Locations: []Location{{Path: "/", Proxy: ProxyConfig{Host: "b", Port: 80, Protocol: "ftp"}}}}, "invalid proxy protocol"},
		{"location empty path", Domain{Name: "a", Type: "proxy", SSL: validSSL(), Locations: []Location{{Proxy: ProxyConfig{Host: "b", Port: 80, Protocol: "http"}}}}, "location path cannot be empty"},
		{"auth without key", Domain{Name: "a", Type: "static", PublicDir: "/www", SSL: validSSL(), Auth: AuthConfig{Enabled: true, Type: "ec", TokenLifetime: 60}}, "public_key is required"},
		{"auth bad type", Domain{Name: "a", Type: "static", PublicDir: "/www", SSL: validSSL(), Auth: AuthConfig{Enabled: true, Type: "basic", PublicKey: "k", TokenLifetime: 60}}, "invalid auth type"},
		{"missing ssl", Domain{Name: "a", Type: "static", PublicDir: "/www"}, "SSL cert_file and key_file"},
	}
	for _, tc := range cases {
		err := tc.d.validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestAuthDefaults(t *testing.T) {
	d := Domain{Name: "a", Type: "static", PublicDir: "/www", SSL: validSSL(), Auth: AuthConfig{Enabled: true, PublicKey: "k"}}
	d.setDefaults()
	if d.Auth.Type != "ec" || d.Auth.TokenLifetime != 3600 {
		t.Fatalf("auth defaults: %+v", d.Auth)
	}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	off := Domain{Name: "a", Type: "static", PublicDir: "/www", SSL: validSSL()}
	off.setDefaults()
	if off.Auth.Type != "" || off.Auth.TokenLifetime != 0 {
		t.Fatalf("auth defaults must not apply when auth is disabled: %+v", off.Auth)
	}
}

func TestServerAndLogDefaults(t *testing.T) {
	var c Config
	c.Domains = []Domain{{Name: "a", Type: "static", PublicDir: "/www", SSL: validSSL()}}
	c.setDefaults()
	if c.Server.Port != 443 || c.Server.HTTPPort != 80 || !c.Server.IsHTTPS {
		t.Fatalf("server defaults: %+v", c.Server)
	}
	if c.Log.Level != "info" {
		t.Fatalf("log level default: %q", c.Log.Level)
	}
	if c.Settings.ReadTimeout != "0s" || c.Settings.WriteTimeout != "0s" || c.Settings.GracefulShutdownTimeout != "30s" {
		t.Fatalf("settings defaults: %+v", c.Settings)
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.Log.Level = "verbose"
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "invalid log level") {
		t.Fatalf("bad log level accepted: %v", err)
	}
}

func TestNonHTTPSServerSkipsSSLValidation(t *testing.T) {
	c := Config{Server: ServerConfig{Port: 8080}, Domains: []Domain{{Name: "a", Type: "static", PublicDir: "/www"}}}
	c.setDefaults()
	if c.Server.IsHTTPS {
		t.Fatal("port 8080 must not be treated as HTTPS")
	}
	if err := c.validate(); err != nil {
		t.Fatalf("non-HTTPS server must not require SSL files: %v", err)
	}
	c.Server.Port = 443
	c.setDefaults()
	if err := c.validate(); err == nil {
		t.Fatal("HTTPS server must require SSL files")
	}
}

func TestConfigNeedsAtLeastOneDomain(t *testing.T) {
	var c Config
	c.setDefaults()
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "at least one domain") {
		t.Fatalf("got %v", err)
	}
}

// The YAML shape users actually write (config.yaml.example) must round-trip
// through yaml -> setDefaults -> validate.
func TestExampleStyleYAML(t *testing.T) {
	src := `
server:
  port: 443
  http_port: 80
admin_token: "t"
log:
  level: debug
domains:
  - name: www.example.com
    type: static
    public_dir: /var/www/example
    ssl:
      cert_file: /etc/ssl/example/fullchain.pem
      key_file: /etc/ssl/example/privkey.pem
  - name: api.example.com
    type: proxy
    proxy:
      host: backend
      port: 12000
    ssl:
      cert_file: /etc/ssl/api/fullchain.pem
      key_file: /etc/ssl/api/privkey.pem
  - name: app.example.com
    type: proxy
    locations:
      - path: /
        proxy: {host: web, port: 3000}
      - path: /api
        proxy: {host: api, port: 8080, protocol: https, insecure_skip_verify: true}
    ssl:
      cert_file: /etc/ssl/app/fullchain.pem
      key_file: /etc/ssl/app/privkey.pem
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	c.setDefaults()
	if err := c.validate(); err != nil {
		t.Fatalf("example-style config rejected: %v", err)
	}
	if c.AdminToken != "t" || c.Log.Level != "debug" {
		t.Fatalf("top-level fields lost: %+v", c)
	}
	api := c.Domains[1]
	if len(api.Locations) != 1 || api.Locations[0].Proxy.Host != "backend" || api.Locations[0].Proxy.Protocol != "http" {
		t.Fatalf("proxy domain default location wrong: %+v", api.Locations)
	}
	app := c.Domains[2]
	if !app.Locations[1].Proxy.InsecureSkipVerify || app.Locations[1].Proxy.Protocol != "https" {
		t.Fatalf("nested location proxy fields lost: %+v", app.Locations[1].Proxy)
	}
}

// A domain's `headers:` map is sent as-is, so it must hold things that can be
// sent: a token for the name, one line for the value.
func TestDomainHeadersValidation(t *testing.T) {
	ok := Domain{Name: "a", Type: "static", PublicDir: "/www", SSL: validSSL(), Headers: map[string]string{
		"Content-Security-Policy-Report-Only": "default-src 'self'; frame-ancestors 'none'",
		"Permissions-Policy":                  "camera=()",
		"X-Tab-And-Latin1":                    "a\tb \xe9", // a tab and a Latin-1 byte are legal in a value
	}}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid headers rejected: %v", err)
	}
	if err := ok.validateWithoutSSL(); err != nil {
		t.Fatalf("valid headers rejected without SSL: %v", err)
	}
	bad := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"line break in value", map[string]string{"X-A": "one\r\nSet-Cookie: b"}, "one line"},
		{"newline in value", map[string]string{"X-A": "one\ntwo"}, "one line"},
		{"NUL in value", map[string]string{"X-A": "one\x00two"}, "one line"},
		{"DEL in value", map[string]string{"X-A": "one\x7ftwo"}, "one line"},
		{"space in name", map[string]string{"X A": "v"}, "invalid header name"},
		{"colon in name", map[string]string{"X-A:": "v"}, "invalid header name"},
		{"empty name", map[string]string{"": "v"}, "invalid header name"},
		{"non-ASCII name", map[string]string{"X-Ä": "v"}, "invalid header name"},
		{"control byte in name", map[string]string{"X-A\x01": "v"}, "invalid header name"},
		{"same header twice", map[string]string{"Content-Security-Policy": "a", "content-security-policy": "b"}, "same header"},
	}
	for _, tc := range bad {
		d := Domain{Name: "a", Type: "static", PublicDir: "/www", SSL: validSSL(), Headers: tc.headers}
		for _, check := range []func() error{d.validate, d.validateWithoutSSL} {
			if err := check(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: got %v, want error containing %q", tc.name, err, tc.want)
			}
		}
	}
}

// Two static domains sharing one headers block through a YAML anchor, the way
// a site served under two names is written.
func TestHeadersYAML(t *testing.T) {
	src := `
server:
  port: 3443
domains:
  - name: example.com
    type: static
    public_dir: /www
    headers: &site_headers
      Content-Security-Policy-Report-Only: "default-src 'self'; img-src 'self' data:"
  - name: www.example.com
    type: static
    public_dir: /www
    headers: *site_headers
  - name: files.example.com
    type: file_directory
    public_dir: /files
  - name: long.example.com
    type: static
    public_dir: /www
    headers:
      Content-Security-Policy-Report-Only: >-
        default-src 'self';
        img-src 'self' data:
`
	var c Config
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	c.setDefaults()
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	want := "default-src 'self'; img-src 'self' data:"
	for _, d := range c.Domains[:2] {
		if got := d.Headers["Content-Security-Policy-Report-Only"]; got != want {
			t.Fatalf("%s: header %q", d.Name, got)
		}
	}
	if len(c.Domains[2].Headers) != 0 {
		t.Fatalf("a domain without headers must have none: %v", c.Domains[2].Headers)
	}
	// A long policy written as a folded scalar, one directive per line at one
	// indent, arrives as a single line.
	if got := c.Domains[3].Headers["Content-Security-Policy-Report-Only"]; got != want {
		t.Fatalf("folded header = %q", got)
	}
}
