package config

import (
	"fmt"
	"github.com/labstack/echo/v4"
	"golang.org/x/net/http/httpguts"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net/textproto"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

var ProductionConfigPath = "/etc/fast/config.yaml"

type ProxyConfig struct {
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"`
	Protocol           string `yaml:"protocol,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

func (p *ProxyConfig) setDefaults() {
	if p.Protocol == "" {
		p.Protocol = "http"
	}
	// InsecureSkipVerify defaults to false
}

func (p *ProxyConfig) validate() error {
	if p.Host == "" {
		return fmt.Errorf("proxy host cannot be empty")
	}
	if p.Port <= 0 || p.Port > 65535 {
		return fmt.Errorf("invalid proxy port: %d", p.Port)
	}
	if p.Protocol != "http" && p.Protocol != "https" {
		return fmt.Errorf("invalid proxy protocol: %s", p.Protocol)
	}
	return nil
}

type SSLConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

func (s *SSLConfig) validate() error {
	if s.CertFile == "" || s.KeyFile == "" {
		return fmt.Errorf("SSL cert_file and key_file must both be specified")
	}
	return nil
}

type Location struct {
	Path  string      `yaml:"path"`
	Proxy ProxyConfig `yaml:"proxy"`
}

// AuthConfig for EC authentication
type AuthConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Type           string   `yaml:"type"`            // "ec" for elliptic curve
	PublicKey      string   `yaml:"public_key"`      // PEM encoded EC public key
	AllowedHosts   []string `yaml:"allowed_hosts"`   // Subdomains requiring auth
	TokenLifetime  int      `yaml:"token_lifetime"`  // Seconds
	RequireBrowser bool     `yaml:"require_browser"` // Require Hob browser
}

type Domain struct {
	Name      string      `yaml:"name"`
	Type      string      `yaml:"type"`
	PublicDir string      `yaml:"public_dir"`
	Proxy     ProxyConfig `yaml:"proxy"`
	SSL       SSLConfig   `yaml:"ssl"`
	Locations []Location  `yaml:"locations,omitempty"`
	Auth      AuthConfig  `yaml:"auth,omitempty"` // Add authentication config
	// Headers are response headers this domain's answers carry (a
	// Content-Security-Policy for a static site, say). Each is set only where
	// the response has no value of its own: a header the upstream or the file
	// handler already chose stays.
	Headers map[string]string `yaml:"headers,omitempty"`
}

// validateHeaders refuses a header that net/http would drop or mangle rather
// than send: a name outside the token grammar, a value with a control byte
// or a line break (which would start a header of its own), and two names
// that are one header once canonicalised (map order would pick the winner).
func (d *Domain) validateHeaders() error {
	seen := make(map[string]string, len(d.Headers))
	for name, value := range d.Headers {
		if !httpguts.ValidHeaderFieldName(name) {
			return fmt.Errorf("invalid header name: %q", name)
		}
		if !httpguts.ValidHeaderFieldValue(value) {
			return fmt.Errorf("header %s: value must be one line of printable characters", name)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if other, dup := seen[canonical]; dup {
			return fmt.Errorf("headers %q and %q are the same header", other, name)
		}
		seen[canonical] = name
	}
	return nil
}

func (d *Domain) setDefaults() {
	if d.Type == "proxy" {
		// Set default for main proxy config
		d.Proxy.setDefaults()

		// If no locations provided, create default "/" location with main proxy config
		if len(d.Locations) == 0 {
			d.Locations = []Location{
				{
					Path:  "/",
					Proxy: d.Proxy,
				},
			}
		} else {
			// Set defaults for all provided locations
			for i := range d.Locations {
				d.Locations[i].Proxy.setDefaults()
			}
		}
	}

	// Set auth defaults
	if d.Auth.Type == "" && d.Auth.Enabled {
		d.Auth.Type = "ec"
	}
	if d.Auth.TokenLifetime == 0 && d.Auth.Enabled {
		d.Auth.TokenLifetime = 3600 // Default 1 hour
	}
}

func (d *Domain) validate() error {
	if d.Name == "" {
		return fmt.Errorf("domain name cannot be empty")
	}

	validTypes := map[string]bool{
		"static":         true,
		"proxy":          true,
		"file_directory": true,
	}
	if !validTypes[d.Type] {
		return fmt.Errorf("invalid domain type: %s", d.Type)
	}

	if d.Type == "proxy" {
		for _, loc := range d.Locations {
			if loc.Path == "" {
				return fmt.Errorf("location path cannot be empty")
			}
			if err := loc.Proxy.validate(); err != nil {
				return fmt.Errorf("proxy configuration error for location %s: %v", loc.Path, err)
			}
		}
	} else if d.PublicDir == "" {
		return fmt.Errorf("public_dir is required for type: %s", d.Type)
	}

	// Validate auth if enabled
	if d.Auth.Enabled {
		if d.Auth.Type != "ec" {
			return fmt.Errorf("invalid auth type: %s", d.Auth.Type)
		}
		if d.Auth.PublicKey == "" {
			return fmt.Errorf("public_key is required when auth is enabled")
		}
		if d.Auth.TokenLifetime <= 0 {
			return fmt.Errorf("invalid token_lifetime: %d", d.Auth.TokenLifetime)
		}
	}

	if err := d.validateHeaders(); err != nil {
		return err
	}

	// Always validate SSL for the regular validate method
	return d.SSL.validate()
}

func (c *Config) validate() error {
	if err := c.Server.validate(); err != nil {
		return fmt.Errorf("server configuration error: %v", err)
	}

	if err := c.Log.validate(); err != nil {
		return fmt.Errorf("log configuration error: %v", err)
	}

	if len(c.Domains) == 0 {
		return fmt.Errorf("at least one domain must be configured")
	}

	for _, domain := range c.Domains {
		// Only require SSL validation if we're running on HTTPS port
		if c.Server.IsHTTPS {
			if err := domain.validate(); err != nil {
				return fmt.Errorf("domain %s configuration error: %v", domain.Name, err)
			}
		} else {
			// Skip SSL validation for non-HTTPS servers
			if err := domain.validateWithoutSSL(); err != nil {
				return fmt.Errorf("domain %s configuration error: %v", domain.Name, err)
			}
		}
	}

	return nil
}

// Add this new method to Domain
func (d *Domain) validateWithoutSSL() error {
	if d.Name == "" {
		return fmt.Errorf("domain name cannot be empty")
	}

	validTypes := map[string]bool{
		"static":         true,
		"proxy":          true,
		"file_directory": true,
	}
	if !validTypes[d.Type] {
		return fmt.Errorf("invalid domain type: %s", d.Type)
	}

	if d.Type == "proxy" {
		for _, loc := range d.Locations {
			if loc.Path == "" {
				return fmt.Errorf("location path cannot be empty")
			}
			if err := loc.Proxy.validate(); err != nil {
				return fmt.Errorf("proxy configuration error for location %s: %v", loc.Path, err)
			}
		}
	} else if d.PublicDir == "" {
		return fmt.Errorf("public_dir is required for type: %s", d.Type)
	}

	// Validate auth if enabled
	if d.Auth.Enabled {
		if d.Auth.Type != "ec" {
			return fmt.Errorf("invalid auth type: %s", d.Auth.Type)
		}
		if d.Auth.PublicKey == "" {
			return fmt.Errorf("public_key is required when auth is enabled")
		}
		if d.Auth.TokenLifetime <= 0 {
			return fmt.Errorf("invalid token_lifetime: %d", d.Auth.TokenLifetime)
		}
	}

	return d.validateHeaders()
}

type ServerConfig struct {
	Port     int  `yaml:"port"`
	HTTPPort int  `yaml:"http_port"`
	IsHTTPS  bool `yaml:"-"`
}

func (s *ServerConfig) setDefaults() {
	if s.Port == 0 {
		s.Port = 443
	}
	if s.HTTPPort == 0 {
		s.HTTPPort = 80
	}
	s.IsHTTPS = s.Port == 443
}

func (s *ServerConfig) validate() error {
	if s.Port <= 0 || s.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", s.Port)
	}
	if s.HTTPPort <= 0 || s.HTTPPort > 65535 {
		return fmt.Errorf("invalid HTTP port: %d", s.HTTPPort)
	}
	return nil
}

type LogConfig struct {
	File  string `yaml:"file"`
	Level string `yaml:"level"`
}

func (l *LogConfig) setDefaults() {
	if l.Level == "" {
		l.Level = "info"
	}
}

func (l *LogConfig) validate() error {
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLevels[l.Level] {
		return fmt.Errorf("invalid log level: %s", l.Level)
	}
	return nil
}

type Config struct {
	Server    ServerConfig `yaml:"server"`
	Domains   []Domain     `yaml:"domains"`
	GlobalSSL SSLConfig    `yaml:"global_ssl"`
	Log       LogConfig    `yaml:"log"`
	Settings  struct {
		ReadTimeout             string `yaml:"read_timeout"`
		WriteTimeout            string `yaml:"write_timeout"`
		GracefulShutdownTimeout string `yaml:"graceful_shutdown_timeout"`
	} `yaml:"settings"`
	IsDevelopment bool   `yaml:"is_development"`
	AdminToken    string `yaml:"admin_token,omitempty"` // Optional admin token for remote access
}

func (c *Config) setDefaults() {
	c.Server.setDefaults()
	c.Log.setDefaults()

	for i := range c.Domains {
		c.Domains[i].setDefaults()
	}

	if c.Settings.ReadTimeout == "" {
		c.Settings.ReadTimeout = "0s" // Changed for streaming
	}
	if c.Settings.WriteTimeout == "" {
		c.Settings.WriteTimeout = "0s" // Changed for streaming
	}
	if c.Settings.GracefulShutdownTimeout == "" {
		c.Settings.GracefulShutdownTimeout = "30s"
	}
}

func isLaunchedByDebugger() bool {
	_, err := exec.LookPath("gops")
	if err != nil {
		return strings.Contains(os.Args[0], "debugger") || strings.Contains(os.Args[0], "___go_build_")
	}

	gopsOut, err := exec.Command("gops", strconv.Itoa(os.Getppid())).Output()
	if err != nil {
		echo.New().Logger.Warnf("Error running gops: %v", err)
		return false
	}

	gopsOutStr := string(gopsOut)

	switch runtime.GOOS {
	case "windows":
		return strings.Contains(gopsOutStr, "\\dlv.exe")
	case "darwin":
		return strings.Contains(gopsOutStr, "/dlv") ||
			strings.Contains(gopsOutStr, "/dlv-dap") ||
			strings.Contains(gopsOutStr, "debugserver")
	default:
		return strings.Contains(gopsOutStr, "/dlv")
	}
}

func LoadConfig() (*Config, error) {
	var configPath string
	if isLaunchedByDebugger() {
		configPath = "test/config.yaml"
		echo.New().Logger.Info("Debug mode detected. Using local config.yaml")
	} else {
		configPath = ProductionConfigPath
	}

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	config.setDefaults()

	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %v", err)
	}

	return &config, nil
}
