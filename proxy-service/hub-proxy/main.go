package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Load configuration
	config := LoadConfig()

	// Validate configuration
	if err := config.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// Setup logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("Starting Hub Proxy on port %s", config.Port)
	if config.StubBFF {
		log.Printf("BFF validation is STUBBED - all requests will be allowed without BFF redirect")
	} else {
		log.Printf("BFF Auth URL: %s", config.BFFAuthURL)
	}
	log.Printf("PIKO Relay URL: %s", config.PIKORelayURL)
	
	// Log endpoint mappings status
	log.Printf("Endpoint mappings configured: %d", len(config.EndpointMappings))
	if len(config.EndpointMappings) > 0 {
		log.Printf("Endpoint mappings: %v", config.EndpointMappings)
	} else {
		log.Printf("No endpoint mappings configured. Set ENDPOINT_MAPPINGS environment variable to enable endpoint mapping.")
	}

	// Create proxy handler
	handler := NewProxyHandler(config)

	// Create HTTP server
	server := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      handler,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	log.Printf("Hub Proxy server started successfully on port %s", config.Port)

	// Wait for interrupt signal for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
