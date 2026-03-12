package server

import (
	"crypto/tls"
	"fast/config"
	"fast/handlers"
	"fmt"
	"github.com/labstack/echo/v4/middleware"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

type Server struct {
	echo   *echo.Echo
	config *config.Config
}

// TemplateRenderer is a custom html/template renderer for Echo framework
type TemplateRenderer struct {
	templates *template.Template
}

// Render renders a template document
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	// Add global methods if data is a map
	if viewContext, isMap := data.(map[string]interface{}); isMap {
		viewContext["reverse"] = c.Echo().Reverse
	}
	return t.templates.ExecuteTemplate(w, name, data)
}

func New(cfg *config.Config) *Server {
	e := echo.New()

	// Initialize and set the renderer with custom functions
	tmpl := template.New("")

	// Add custom functions
	tmpl = tmpl.Funcs(template.FuncMap{
		"splitPath": func(path string) []string {
			path = strings.Trim(path, "/")
			if path == "" {
				return []string{}
			}
			return strings.Split(path, "/")
		},
		"joinPath": func(base, path string) string {
			if base == "" {
				return "/" + path
			}
			return base + "/" + path
		},
		"lastIndex": func(arr []string) int {
			return len(arr) - 1
		},
		"currentYear": func() int {
			return time.Now().Year()
		},
	})

	// Parse embedded templates
	templates, err := fs.ReadDir(templateFiles, "templates")
	if err != nil {
		log.Fatalf("Failed to read templates: %v", err)
	}

	for _, entry := range templates {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".html") {
			templateContent, err := templateFiles.ReadFile("templates/" + entry.Name())
			if err != nil {
				log.Fatalf("Failed to read template %s: %v", entry.Name(), err)
			}
			_, err = tmpl.New(entry.Name()).Parse(string(templateContent))
			if err != nil {
				log.Fatalf("Failed to parse template %s: %v", entry.Name(), err)
			}
		}
	}

	e.Renderer = &TemplateRenderer{
		templates: tmpl,
	}

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	return &Server{
		echo:   e,
		config: cfg,
	}
}

func (s *Server) setupRoutes() {
	// Add health check endpoint before domain middleware
	s.echo.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"status": "ok",
		})
	})

	// Admin routes - add before domain middleware
	// These routes are accessible only from localhost or with admin token
	admin := s.echo.Group("/admin")

	// Admin UI
	admin.GET("", handlers.HandleAdminPanel)
	admin.GET("/", handlers.HandleAdminPanel)

	// Admin API endpoints
	adminAPI := admin.Group("/api")
	adminAPI.POST("/generate-keys", handlers.HandleGenerateKeys)
	adminAPI.PUT("/domain-auth", handlers.HandleUpdateDomainAuth)
	adminAPI.POST("/domain", handlers.HandleAddDomain)
	adminAPI.DELETE("/domain/:domain", handlers.HandleDeleteDomain)
	adminAPI.GET("/test-auth", handlers.HandleTestAuthentication)

	// Create a map for quick domain lookup
	domainMap := make(map[string]config.Domain)
	for _, domain := range s.config.Domains {
		domainMap[domain.Name] = domain
	}

	// Single middleware to handle all domains
	s.echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Skip domain check for health and admin endpoints
			if c.Request().URL.Path == "/health" ||
				strings.HasPrefix(c.Request().URL.Path, "/admin") {
				return next(c)
			}

			host := c.Request().Host
			if strings.Contains(host, ":") {
				host = strings.Split(host, ":")[0]
			}
			log.Printf("Incoming request for host: %s", host)

			// First try exact matches
			for _, domain := range s.config.Domains {
				if !strings.HasPrefix(domain.Name, "*.") && host == domain.Name {
					log.Printf("Matched exact domain: %s", domain.Name)
					c.Set("domain", domain)

					// Check if authentication is required
					if domain.Auth.Enabled && domain.Auth.Type == "ec" {
						// Apply EC authentication middleware
						authConfig := handlers.ECAuthConfig{
							PublicKey:      domain.Auth.PublicKey,
							AllowedHosts:   domain.Auth.AllowedHosts,
							TokenLifetime:  domain.Auth.TokenLifetime,
							RequireBrowser: domain.Auth.RequireBrowser,
						}

						// Create a wrapped handler with auth
						authMiddleware := handlers.AuthMiddleware(authConfig)
						return authMiddleware(next)(c)
					}

					return next(c)
				}
			}

			// If no exact match found, try wildcard domains
			for _, domain := range s.config.Domains {
				if strings.HasPrefix(domain.Name, "*.") {
					suffix := domain.Name[1:] // Remove the *
					if strings.HasSuffix(host, suffix) {
						log.Printf("Matched wildcard domain: %s", domain.Name)
						c.Set("domain", domain)

						// Check if authentication is required
						if domain.Auth.Enabled && domain.Auth.Type == "ec" {
							// Apply EC authentication middleware
							authConfig := handlers.ECAuthConfig{
								PublicKey:      domain.Auth.PublicKey,
								AllowedHosts:   domain.Auth.AllowedHosts,
								TokenLifetime:  domain.Auth.TokenLifetime,
								RequireBrowser: domain.Auth.RequireBrowser,
							}

							// Create a wrapped handler with auth
							authMiddleware := handlers.AuthMiddleware(authConfig)
							return authMiddleware(next)(c)
						}

						return next(c)
					}
				}
			}

			log.Printf("No matching domain found for host: %s", host)
			return echo.ErrNotFound
		}
	})

	// Root handler
	s.echo.Any("/", func(c echo.Context) error {
		domainVal := c.Get("domain")
		if domainVal == nil {
			return echo.ErrNotFound
		}
		domain := domainVal.(config.Domain)

		switch domain.Type {
		case "proxy":
			return handlers.HandleProxy(c, domain)
		case "file_directory":
			return handlers.HandleFileDirectory(c, domain)
		default:
			return handlers.ServeIndexOrFile(c, domain.PublicDir, "index.html")
		}
	})

	// Catch-all handler
	s.echo.Any("/*", func(c echo.Context) error {
		domainVal := c.Get("domain")
		if domainVal == nil {
			return echo.ErrNotFound
		}
		domain := domainVal.(config.Domain)

		switch domain.Type {
		case "proxy":
			return handlers.HandleProxy(c, domain)
		case "file_directory":
			return handlers.HandleFileDirectory(c, domain)
		default:
			return handlers.ServeIndexOrFile(c, domain.PublicDir, c.Request().URL.Path)
		}
	})
}

func (s *Server) setupTLSConfig() (*tls.Config, error) {
	certificates := make(map[string]tls.Certificate)

	for _, domain := range s.config.Domains {
		cert, err := tls.LoadX509KeyPair(domain.SSL.CertFile, domain.SSL.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load SSL cert for %s: %v", domain.Name, err)
		}
		certificates[domain.Name] = cert
		s.echo.Logger.Infof("Loaded certificate for domain: %s", domain.Name)
	}

	// Load global SSL certificate if provided
	if s.config.GlobalSSL.CertFile != "" && s.config.GlobalSSL.KeyFile != "" {
		globalCert, err := tls.LoadX509KeyPair(s.config.GlobalSSL.CertFile, s.config.GlobalSSL.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load global SSL cert: %v", err)
		}
		certificates[""] = globalCert
		s.echo.Logger.Info("Loaded global SSL certificate")
	}

	return &tls.Config{
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			s.echo.Logger.Infof("Client requesting certificate for ServerName: %s", info.ServerName)

			if cert, ok := certificates[info.ServerName]; ok {
				return &cert, nil
			}

			// If no exact match, try to find a wildcard certificate
			for domainName, cert := range certificates {
				if strings.HasPrefix(domainName, "*.") && strings.HasSuffix(info.ServerName, domainName[1:]) {
					return &cert, nil
				}
			}

			// If still no match, return the global certificate if available
			if globalCert, ok := certificates[""]; ok {
				s.echo.Logger.Infof("Using global certificate for ServerName: %s", info.ServerName)
				return &globalCert, nil
			}

			// As a last resort, return the first certificate in the map
			for _, cert := range certificates {
				s.echo.Logger.Infof("Using first available certificate for ServerName: %s", info.ServerName)
				return &cert, nil
			}

			s.echo.Logger.Warnf("No suitable certificate found for ServerName: %s", info.ServerName)
			return nil, fmt.Errorf("no suitable certificate found")
		},
		MinVersion:               tls.VersionTLS12,
		MaxVersion:               tls.VersionTLS13,
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
	}, nil
}

func (s *Server) Start() error {
	s.echo.Logger.Info("Setting up routes for domains:")
	for _, domain := range s.config.Domains {
		s.echo.Logger.Infof("  - %s (Type: %s, Public Dir: %s)", domain.Name, domain.Type, domain.PublicDir)
		if domain.Auth.Enabled {
			s.echo.Logger.Infof("    Auth: Enabled (EC), Token Lifetime: %ds, Require Hob: %v",
				domain.Auth.TokenLifetime, domain.Auth.RequireBrowser)
		}
	}
	s.setupRoutes()

	tlsConfig, err := s.setupTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to setup TLS config: %v", err)
	}

	// Update HTTP server configuration to support long-lived connections
	server := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", s.config.Server.Port),
		TLSConfig:         tlsConfig,
		ReadTimeout:       0,                // No timeout for streaming
		WriteTimeout:      0,                // No timeout for streaming
		IdleTimeout:       0,                // No timeout for streaming
		ReadHeaderTimeout: 10 * time.Second, // Only timeout for reading headers
		// The lack of timeouts is intentional for supporting SSE and other streaming responses
	}

	// Start HTTP to HTTPS redirect
	go s.startHTTPRedirect()

	s.echo.Logger.Infof("Starting HTTPS server on port %d (IPv4)", s.config.Server.Port)
	s.echo.Logger.Infof("Admin panel available at: https://localhost:%d/admin", s.config.Server.Port)
	return s.echo.StartServer(server)
}

func (s *Server) startHTTPRedirect() {
	// Update HTTP redirect server to also support long-lived connections
	httpServer := &http.Server{
		Addr: fmt.Sprintf("0.0.0.0:%d", s.config.Server.HTTPPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+r.Host+r.URL.String(), http.StatusMovedPermanently)
		}),
		ReadTimeout:       10 * time.Second, // Can use timeout here as it's just redirecting
		WriteTimeout:      10 * time.Second, // Can use timeout here as it's just redirecting
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.echo.Logger.Infof("Starting HTTP redirect server on port %d (IPv4)", s.config.Server.HTTPPort)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		s.echo.Logger.Fatal(err)
	}
}
