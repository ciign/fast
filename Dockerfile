# Start from a Go base image
FROM golang:1.21-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum first (from fast-server directory)
COPY fast-server/go.mod fast-server/go.sum ./

# Download dependencies
RUN go mod download

# Copy the entire fast-server directory
COPY fast-server/ .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o fast_server .

# Start a new stage from scratch
FROM alpine:latest

# Install necessary packages
RUN apk --no-cache add \
    ca-certificates \
    curl \
    wget \
    netcat-openbsd

WORKDIR /app

# Copy the pre-built binary
COPY --from=builder /app/fast_server .

# Copy the config file from root directory
COPY config.yaml.example /etc/fast/config.yaml

# Create necessary directories
RUN mkdir -p /var/www/fast /etc/fast/ssl /var/log/fast

# Expose ports
EXPOSE 80 443

# Command to run the executable
CMD ["./fast_server"]