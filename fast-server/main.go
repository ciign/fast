package main

import (
	"fast/config"
	"fast/server"
	"log"
)

func main() {
	log.Println("Starting FAST server...")

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create and start server
	s := server.New(cfg)
	if err := s.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
