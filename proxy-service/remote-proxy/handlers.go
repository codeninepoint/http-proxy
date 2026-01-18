package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

// ProxyHandler handles incoming requests from PIKO agent
type ProxyHandler struct {
	config *Config
}

// NewProxyHandler creates a new proxy handler
func NewProxyHandler(config *Config) *ProxyHandler {
	return &ProxyHandler{
		config: config,
	}
}

// ServeHTTP is the main request handler
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if this is a WebSocket upgrade request
	if isWebSocket(r) {
		h.handleWebSocket(w, r)
		return
	}

	// Extract target from headers
	targetIP, targetPort, err := h.extractTarget(r)
	if err != nil {
		log.Printf("ServeHTTP: ERROR - Failed to extract target - Method: %s, Path: %s, Query: %s, Error: %v", 
			r.Method, r.URL.Path, r.URL.RawQuery, err)
		http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		return
	}

	log.Printf("ServeHTTP: Forwarding request to target: %s:%s - Method: %s, Path: %s", targetIP, targetPort, r.Method, r.URL.Path)

	// Forward to target
	h.forwardToTarget(w, r, targetIP, targetPort)
}

// extractTarget extracts target IP and Port from X-Forward-Target header
func (h *ProxyHandler) extractTarget(r *http.Request) (ip, port string, err error) {
	// Try X-Forward-Target header first (format: "IP:Port")
	target := r.Header.Get("X-Forward-Target")
	if target != "" {
		parts := strings.Split(target, ":")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid X-Forward-Target format, expected IP:Port, got: %s", target)
		}
		ip = strings.TrimSpace(parts[0])
		port = strings.TrimSpace(parts[1])
		if ip != "" && port != "" {
			return ip, port, nil
		}
	}

	// Try alternative headers
	targetIP := r.Header.Get("X-Target-IP")
	targetPort := r.Header.Get("X-Target-Port")
	if targetIP != "" && targetPort != "" {
		return strings.TrimSpace(targetIP), strings.TrimSpace(targetPort), nil
	}

	return "", "", fmt.Errorf("target not specified in headers (X-Forward-Target or X-Target-IP/X-Target-Port)")
}

// copyHeaders copies relevant headers from source to destination request
func (h *ProxyHandler) copyHeaders(dst *http.Request, src *http.Request) {
	// Copy all headers except those that should be set by the transport
	skipHeaders := map[string]bool{
		"Host":              true,
		"Connection":        true,
		"Upgrade":           true,
		"X-Forward-Target":  true,
		"X-Target-IP":       true,
		"X-Target-Port":     true,
		"Content-Length":    true,
		"Transfer-Encoding": true,
	}

	for key, values := range src.Header {
		if skipHeaders[key] {
			continue
		}
		for _, value := range values {
			dst.Header.Add(key, value)
		}
	}
}

// Helper function
func isWebSocket(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket" &&
		strings.ToLower(r.Header.Get("Connection")) == "upgrade"
}
