# FAST
HTTP/HTTPS Static Site Server and Reverse Proxy

[![FAST CI/CD](https://github.com/ciign/fast/actions/workflows/go.yml/badge.svg)](https://github.com/ciign/fast/actions/workflows/go.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/ciign/fast)](https://goreportcard.com/report/github.com/ciign/fast)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/ciign/fast)](https://github.com/ciign/fast/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/github/go-mod/go-version/ciign/fast)](https://golang.org/)

FAST (File Access Speedy Transfer) is a lightweight...
FAST (File Access Speedy Transfer) is a lightweight,
high-performance web server designed for serving static content and acting as a reverse proxy.
It prioritizes speed and efficiency, making it ideal for quickly delivering static assets and proxying requests to
backend services.

## Features

- Multi-domain support
- SSL/TLS encryption
- HTTP to HTTPS redirection
- Domain-specific public directories
- Reverse proxy support
- Easy configuration via YAML
- Systemd service integration
- Minimal configuration required, allowing for quick setup and deployment
- Optimized for serving static files and proxying requests
- Optimized for serving file directories with resume on disconnect support for downloads
- High concurrency, able to handle multiple simultaneous connections efficiently
- Low memory footprint, making it suitable for various hosting environments
- Built-in caching mechanisms to further enhance performance
- Support for common web technologies like HTTP/2, SSL/TLS, and compression

## Deployment Options

FAST can be deployed in two ways:
1. System Service (Traditional) - For direct system installation
2. Docker Compose (Containerized) - For containerized deployment

## 1. System Service Deployment

### Prerequisites
- Go 1.16 or higher
- Make
- Systemd (for service installation)
- Root access (for installation)

### Installation

1. Clone the repository:
    ```bash
    git clone https://github.com/ciign/fast.git
    cd fast-server
    ```

2. Modify the `config.yaml.example` file to suit your needs and rename it to `config.yaml`.

3. Build and install the server:
    ```bash
    sudo make install
    ```

   This will:
   - Build the server for Linux
   - Copy the binary to `/usr/local/bin/fast_server`
   - Copy the configuration to `/etc/fast/config.yaml`
   - Set up a systemd service

4. Start the server:
    ```bash
    sudo systemctl start fast
    ```

### System Service Management

The server runs as a systemd service:
- Start: `sudo systemctl start fast`
- Stop: `sudo systemctl stop fast`
- Restart: `sudo systemctl restart fast`
- Check status: `sudo systemctl status fast`

### Uninstallation
To uninstall the system service:
   ```bash
   sudo make uninstall
   ```

## 2. Docker Compose Deployment

### Prerequisites

* Docker
* Docker Compose
* Make (optional)

### Quick Start

1. Initialize development environment:
   ```bash
      make init-dev
   ```
   This will:
   * Create necessary directories
   * Generate test SSL certificates
   * Copy example configuration files

2. Start the server:
   ```bash
   make docker-compose-up
   ```
   Or directly with Docker Compose:
   ```bash
   docker-compose up -d
   ```

### Docker Configuration
The server can be configured through environment variables in .env file:
   ```env
   # Host Directory Paths
   CONFIG_PATH=./config.yaml
   SSL_PATH=./ssl
   WWW_PATH=./www
   LOG_PATH=./logs

   # Server Ports
   HTTP_PORT=80
   HTTPS_PORT=443
   ```

### Docker Commands
Using Make:

* Build: `make docker-compose-build`
* Start: `make docker-compose-up`
* Stop: `make docker-compose-down`
* View logs: `make docker-compose-logs`

Using Docker Compose directly:

* Build: `docker-compose build`
* Start: `docker-compose up -d`
* Stop: `docker-compose down`
* View logs: `docker-compose logs -f`

### Docker Directory Structure
```
├── config.yaml         # Server configuration
├── docker-compose.yaml # Docker Compose configuration
├── .env               # Environment variables
├── ssl/               # SSL certificates
│   ├── domain1.lan/
│   ├── domain2.lan/
│   └── global/
├── www/               # Web content
└── logs/              # Server logs
```

## Configuration

The server is configured via the `config.yaml` file. The file location depends on your deployment method:
- System Service: `/etc/fast/config.yaml`
- Docker: `./config.yaml` (mounted to `/etc/fast/config.yaml` in container)

Here's an example configuration:

```yaml
server:
   port: 443
   http_port: 80  # for HTTP to HTTPS redirect

domains:
   - name: static.example.com
     type: static
     public_dir: /var/www/fast/static.example.com
     ssl:
        cert_file: /etc/fast/ssl/static.example.com/fullchain.pem
        key_file: /etc/fast/ssl/static.example.com/privkey.pem

   - name: files.example.com
     type: file_directory
     public_dir: /var/www/fast/files.example.com
     ssl:
        cert_file: /etc/fast/ssl/files.example.com/fullchain.pem
        key_file: /etc/fast/ssl/files.example.com/privkey.pem

   - name: api.example.com
     type: proxy
     proxy:
        host: 127.0.0.1
        port: 8000
     ssl:
        cert_file: /etc/fast/ssl/api.example.com/fullchain.pem
        key_file: /etc/fast/ssl/api.example.com/privkey.pem

global_ssl:
   cert_file: /etc/fast/ssl/global/fullchain.pem
   key_file: /etc/fast/ssl/global/privkey.pem

log:
   file: /var/log/fast/server.log
   level: info  # Options: debug, info, warn, error

settings:
   read_timeout: 5s
   write_timeout: 10s
   graceful_shutdown_timeout: 30s
```

### Common Configuration

Domain Types

1. **Static Sites** `(type: static)`
   * Serves static files from a specified directory
   * Perfect for HTML, CSS, JS, and other static assets
   ```yaml
   type: static
   public_dir: /var/www/fast/example.com
   ```
2. **File Directory** `(type: file_directory)`
   * Serves directory listings with download capabilities
   * Supports resume on disconnect for large files
   ```yaml
   type: file_directory
   public_dir: /var/www/fast/files
   ```
3. **Reverse Proxy** `(type: proxy)`
   * Forwards requests to backend services
   * Supports HTTP/HTTPS backends
   ```yaml
   type: proxy
   proxy:
   host: 127.0.0.1
   port: 8000
   ```

### SSL Configuration
SSL certificates can be configured per domain or globally:

1. **Per Domain** 
   ```yaml
      ssl:
         cert_file: /etc/fast/ssl/domain.com/fullchain.pem
         key_file: /etc/fast/ssl/domain.com/privkey.pem
   ```
2. **Global Fallback**
   ```yaml
      global_ssl:
      cert_file: /etc/fast/ssl/global/fullchain.pem
      key_file: /etc/fast/ssl/global/privkey.pem
   ```

### Logging

Configure logging behavior:
```yaml
log:
   file: /var/log/fast/server.log
   level: info  # Options: debug, info, warn, error
```

### Performance Settings
Fine-tune server performance:
```yaml
settings:
   read_timeout: 5s
   write_timeout: 10s
   graceful_shutdown_timeout: 30s
```

## Development

### Prerequisites

- Go 1.16 or higher
- gops tool (optional, for improved debug detection)

### Directory Structure

```
fast-server/
   ├── config/
   │   └── config.go          # Configuration handling
   ├── handlers/
   │   ├── handlers.go        # Common handler functions
   │   ├── static_handler.go  # Static file handling
   │   ├── proxy_handler.go   # Reverse proxy handling
   │   └── file_directory_handler.go # Directory listing
   ├── server/
   │   ├── server.go          # Main server implementation
   │   ├── embed.go           # Embedded templates
   │   └── templates/         # HTML templates
   ├── main.go                # Application entry point
   └── test/                  # Test files and fixtures
```

### Generate Test Certs

```bash
openssl req -x509 -newkey rsa:4096 -keyout fast-server/test/ssl/domain1.lan/privkey.pem -out fast-server/test/ssl/domain1.lan/fullchain.pem -days 365 -nodes -subj "/CN=domain1.lan"
openssl req -x509 -newkey rsa:4096 -keyout fast-server/test/ssl/domain2.lan/privkey.pem -out fast-server/test/ssl/domain2.lan/fullchain.pem -days 365 -nodes -subj "/CN=domain2.lan"
openssl req -x509 -newkey rsa:4096 -keyout fast-server/test/ssl/global/privkey.pem -out fast-server/test/ssl/global/fullchain.pem -days 365 -nodes -subj "/CN=localhost"

```

### Debug Tools
Install gops for runtime debugging:
```bash
go install github.com/google/gops@latest
```

### Local Development
1. Create development configuration:
   ```bash
   cp config.yaml.example config.yaml
   ```
2. Set up test directories:
   ```bash
   mkdir -p www/domain1.lan www/domain2.lan ssl/domain1.lan ssl/domain2.lan ssl/global logs
   ```
3. Generate test certificates (see above)
4. Build and run:
   ```bash
   make linux
   ./builds/fast_server
   ```
### Testing
Test different domain configurations:

1. Add test domains to /etc/hosts:
   ```
   127.0.0.1 domain1.lan domain2.lan
   ```
2. Access test sites:
   ```
   https://domain1.lan
   https://domain2.lan
   ```

## Acronym

FAST stands for:

- **F**ile
- **A**ccess
- **S**peedy
- **T**ransfer

Emphasizing its core functionality of rapidly serving static files.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Support

If you encounter any problems or have any questions, please open an issue on the GitHub repository.
