package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds the configuration for the Remote Forward Proxy service
type Config struct {
	Port             string
	ConnectionTimeout time.Duration
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	IdleTimeout      time.Duration
	LogLevel         string
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *Config {
	port := getEnv("REMOTE_PROXY_PORT", "8081")
	logLevel := getEnv("LOG_LEVEL", "info")

	connectionTimeout := getDurationEnv("CONNECTION_TIMEOUT", 30*time.Second)
	readTimeout := getDurationEnv("READ_TIMEOUT", 30*time.Second)
	writeTimeout := getDurationEnv("WRITE_TIMEOUT", 30*time.Second)
	idleTimeout := getDurationEnv("IDLE_TIMEOUT", 5*time.Minute)

	return &Config{
		Port:             port,
		ConnectionTimeout: connectionTimeout,
		ReadTimeout:      readTimeout,
		WriteTimeout:     writeTimeout,
		IdleTimeout:      idleTimeout,
		LogLevel:         logLevel,
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Port == "" {
		return fmt.Errorf("REMOTE_PROXY_PORT is required")
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
