package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the configuration for the Hub Proxy service
type Config struct {
	Port             string
	BFFAuthURL       string
	PIKORelayURL     string
	LogLevel         string
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	IdleTimeout      time.Duration
	StubBFF          bool              // If true, skip BFF validation and allow all requests
	EndpointMappings map[string]string // Maps endpoint hostname to target IP:Port (e.g., "myendpoint" -> "localhost:8081")
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *Config {
	port := getEnv("HUB_PROXY_PORT", "8080")
	bffAuthURL := getEnv("BFF_AUTH_URL", "")
	pikoRelayURL := getEnv("PIKO_RELAY_URL", "http://localhost:8000")
	logLevel := getEnv("LOG_LEVEL", "info")
	stubBFF := getEnv("STUB_BFF", "true") == "true" // Default to true for now

	readTimeout := getDurationEnv("READ_TIMEOUT", 30*time.Second)
	writeTimeout := getDurationEnv("WRITE_TIMEOUT", 30*time.Second)
	idleTimeout := getDurationEnv("IDLE_TIMEOUT", 5*time.Minute)

	endpointMappings := LoadEndpointMappings()

	return &Config{
		Port:             port,
		BFFAuthURL:       bffAuthURL,
		PIKORelayURL:     pikoRelayURL,
		LogLevel:         logLevel,
		ReadTimeout:      readTimeout,
		WriteTimeout:     writeTimeout,
		IdleTimeout:      idleTimeout,
		StubBFF:          stubBFF,
		EndpointMappings: endpointMappings,
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// BFF_AUTH_URL is only required if BFF is not stubbed
	if !c.StubBFF && c.BFFAuthURL == "" {
		return fmt.Errorf("BFF_AUTH_URL is required when STUB_BFF=false")
	}
	if c.PIKORelayURL == "" {
		return fmt.Errorf("PIKO_RELAY_URL is required")
	}
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
		// Try parsing as seconds
		if seconds, err := strconv.Atoi(value); err == nil {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultValue
}

// LoadEndpointMappings loads endpoint-to-target mappings from ENDPOINT_MAPPINGS environment variable
// Format: ENDPOINT_MAPPINGS=myendpoint:localhost:8081,other:192.168.1.100:8080
// Returns a map of endpoint hostname -> target IP:Port
func LoadEndpointMappings() map[string]string {
	mappings := make(map[string]string)

	envValue := getEnv("ENDPOINT_MAPPINGS", "")
	if envValue == "" {
		log.Printf("LoadEndpointMappings: ENDPOINT_MAPPINGS environment variable not set, no mappings loaded")
		return mappings
	}

	log.Printf("LoadEndpointMappings: Loading endpoint mappings from ENDPOINT_MAPPINGS: %s", envValue)

	// Split by comma to get individual mappings
	parts := strings.Split(envValue, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Split by colon - format: endpoint:target
		// Note: target is always "IP:Port" format, so it contains exactly one colon
		// The endpoint might contain a port (e.g., "endpoint:443"), making format: "endpoint:port:IP:Port"
		// Strategy: Split on all colons, then reconstruct:
		// - Last two parts are target (IP and Port)
		// - Everything before that is endpoint (may include port)
		// - Strip port from endpoint to normalize to hostname only

		allParts := strings.Split(part, ":")
		if len(allParts) < 3 {
			// Need at least "endpoint:IP:Port" (3 parts minimum)
			log.Printf("LoadEndpointMappings: WARNING - Invalid endpoint mapping format (need at least 'endpoint:IP:Port'): %s", part)
			continue
		}

		// Last two parts are target IP and Port
		targetIP := strings.TrimSpace(allParts[len(allParts)-2])
		targetPort := strings.TrimSpace(allParts[len(allParts)-1])
		target := targetIP + ":" + targetPort

		// Everything before the last two parts is the endpoint (may include port)
		endpointWithPort := strings.Join(allParts[:len(allParts)-2], ":")
		endpointWithPort = strings.TrimSpace(endpointWithPort)

		// Strip port from endpoint to normalize to hostname only
		// Endpoint mappings should use hostname only, not hostname:port
		endpoint := endpointWithPort
		if portColonIdx := strings.LastIndex(endpointWithPort, ":"); portColonIdx > 0 {
			// Check if the part after the last colon looks like a port (numbers only)
			possiblePort := strings.TrimSpace(endpointWithPort[portColonIdx+1:])
			isPort := true
			for _, r := range possiblePort {
				if r < '0' || r > '9' {
					isPort = false
					break
				}
			}
			if isPort {
				// Strip the port from endpoint
				endpoint = strings.TrimSpace(endpointWithPort[:portColonIdx])
				log.Printf("LoadEndpointMappings: Stripped port from endpoint - Original: %s, Normalized: %s", endpointWithPort, endpoint)
			}
		}

		if endpoint != "" && target != "" {
			mappings[endpoint] = target
			log.Printf("LoadEndpointMappings: Loaded endpoint mapping: %s -> %s", endpoint, target)
		} else {
			log.Printf("LoadEndpointMappings: WARNING - Empty endpoint or target after parsing: %s (endpoint: '%s', target: '%s')", part, endpoint, target)
		}
	}

	log.Printf("LoadEndpointMappings: Successfully loaded %d endpoint mapping(s)", len(mappings))
	if len(mappings) > 0 {
		log.Printf("LoadEndpointMappings: Mappings: %v", mappings)
	}

	return mappings
}
