package common

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// GetEnv returns the value of an environment variable or a default value
func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// ParseTarget parses the X-Forward-Target header value (format: "IP:Port")
// Returns IP and Port as separate strings
func ParseTarget(target string) (ip, port string, err error) {
	if target == "" {
		return "", "", fmt.Errorf("target is empty")
	}

	parts := strings.Split(target, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid target format, expected IP:Port, got: %s", target)
	}

	ip = strings.TrimSpace(parts[0])
	port = strings.TrimSpace(parts[1])

	if ip == "" || port == "" {
		return "", "", fmt.Errorf("invalid target format, IP or Port is empty")
	}

	return ip, port, nil
}

// BuildTarget constructs a target string from IP and Port (format: "IP:Port")
func BuildTarget(ip, port string) string {
	return fmt.Sprintf("%s:%s", ip, port)
}

// SetupLogger configures logging based on log level
func SetupLogger(logLevel string) {
	// Simple logger setup - can be enhanced with structured logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

// LogInfo logs an info message
func LogInfo(format string, v ...interface{}) {
	log.Printf("[INFO] "+format, v...)
}

// LogError logs an error message
func LogError(format string, v ...interface{}) {
	log.Printf("[ERROR] "+format, v...)
}

// LogDebug logs a debug message
func LogDebug(format string, v ...interface{}) {
	log.Printf("[DEBUG] "+format, v...)
}
