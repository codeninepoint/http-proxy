package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins - can be restricted in production
		return true
	},
}

// isConnectionError checks if an error is a connection/TLS error (not an HTTP error)
// Connection errors indicate protocol mismatch (e.g., trying HTTPS on HTTP server)
// HTTP errors (4xx/5xx) mean the target is reachable, just returned an error
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	
	// Check for net.OpError (connection errors)
	if opErr, ok := err.(*net.OpError); ok {
		// Connection errors: dial, read, write operations
		if opErr.Op == "dial" || opErr.Op == "read" || opErr.Op == "write" {
			return true
		}
	}
	
	// Check for TLS handshake errors (common when trying HTTPS on HTTP server)
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "tls") || 
	   strings.Contains(errStr, "handshake") ||
	   strings.Contains(errStr, "connection refused") ||
	   strings.Contains(errStr, "no route to host") ||
	   strings.Contains(errStr, "timeout") {
		return true
	}
	
	return false
}

// forwardToTarget forwards HTTP request to target IP:Port
// Auto-detects target protocol: tries HTTPS first if client came via HTTPS, falls back to HTTP if connection fails
// Preserves X-Forwarded-Proto header so target knows original client protocol
func (h *ProxyHandler) forwardToTarget(w http.ResponseWriter, r *http.Request, targetIP, targetPort string) {
	// Preserve original X-Forwarded-Proto from request (tells target the original client protocol)
	originalProto := r.Header.Get("X-Forwarded-Proto")
	if originalProto == "" {
		if r.TLS != nil {
			originalProto = "https"
		} else {
			originalProto = "http"
		}
	}
	log.Printf("forwardToTarget: Original client protocol: %s (X-Forwarded-Proto: %s)", originalProto, r.Header.Get("X-Forwarded-Proto"))

	// Determine initial scheme to try (auto-detect: try HTTPS first if client came via HTTPS)
	initialScheme := "http"
	if strings.ToLower(originalProto) == "https" || r.TLS != nil {
		initialScheme = "https"
		log.Printf("forwardToTarget: Client came via HTTPS, will try HTTPS first for target %s:%s", targetIP, targetPort)
	}

	// Build target URL with initial scheme
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	targetURL := fmt.Sprintf("%s://%s:%s%s", initialScheme, targetIP, targetPort, path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	log.Printf("forwardToTarget: Attempting connection to target: %s %s", r.Method, targetURL)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: h.config.ConnectionTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   h.config.ConnectionTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       h.config.IdleTimeout,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	// Buffer request body if present (needed for retry with different protocol)
	// Body can only be read once, so we need to buffer it for potential retry
	var bodyBytes []byte
	var bodyReader io.Reader = r.Body
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			log.Printf("forwardToTarget: ERROR - Failed to read request body - Error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		r.Body.Close()
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Try to forward the request
	var resp *http.Response
	var err error
	var finalScheme string

	// Create request to target
	targetReq, err := http.NewRequest(r.Method, targetURL, bodyReader)
	if err != nil {
		log.Printf("forwardToTarget: ERROR - Failed to create target request - Method: %s, Target: %s:%s, Path: %s, URL: %s, Error: %v", 
			r.Method, targetIP, targetPort, path, targetURL, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Copy headers and preserve X-Forwarded-Proto
	h.copyHeaders(targetReq, r)
	
	// Ensure X-Forwarded-Proto is set to original client protocol (preserve it)
	targetReq.Header.Set("X-Forwarded-Proto", originalProto)

	// Set Host header to target
	targetReq.Host = fmt.Sprintf("%s:%s", targetIP, targetPort)

	// Forward the request
	resp, err = client.Do(targetReq)
	finalScheme = initialScheme

	// If HTTPS failed with connection/TLS error, retry with HTTP
	if err != nil && initialScheme == "https" {
		// Check if error is a connection/TLS error (not HTTP error)
		// Connection errors indicate protocol mismatch, HTTP errors mean target is reachable
		if isConnectionError(err) {
			log.Printf("forwardToTarget: HTTPS connection failed (likely target is HTTP), retrying with HTTP - Error: %v", err)
			
			// Retry with HTTP
			httpURL := fmt.Sprintf("http://%s:%s%s", targetIP, targetPort, path)
			if r.URL.RawQuery != "" {
				httpURL += "?" + r.URL.RawQuery
			}
			
			// Reset body reader for retry
			var retryBodyReader io.Reader
			if bodyBytes != nil {
				retryBodyReader = bytes.NewReader(bodyBytes)
			}
			
			targetReqHTTP, err2 := http.NewRequest(r.Method, httpURL, retryBodyReader)
			if err2 != nil {
				log.Printf("forwardToTarget: ERROR - Failed to create HTTP retry request - Error: %v", err2)
				http.Error(w, fmt.Sprintf("Gateway error: %v", err), http.StatusBadGateway)
				return
			}
			
			// Copy headers again and preserve X-Forwarded-Proto
			h.copyHeaders(targetReqHTTP, r)
			targetReqHTTP.Header.Set("X-Forwarded-Proto", originalProto)
			targetReqHTTP.Host = fmt.Sprintf("%s:%s", targetIP, targetPort)
			
			// Retry with HTTP
			resp, err = client.Do(targetReqHTTP)
			finalScheme = "http"
			
			if err == nil {
				log.Printf("forwardToTarget: Successfully connected to target using HTTP (HTTPS failed, auto-detected HTTP)")
			}
		}
	}

	if err != nil {
		log.Printf("forwardToTarget: ERROR - Failed to forward request to target - Method: %s, Target: %s:%s, Path: %s, Final Scheme: %s, Error: %v", 
			r.Method, targetIP, targetPort, path, finalScheme, err)
		http.Error(w, fmt.Sprintf("Gateway error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Printf("forwardToTarget: Connected to target using %s (original client protocol: %s, X-Forwarded-Proto preserved: %s)", 
		finalScheme, originalProto, targetReq.Header.Get("X-Forwarded-Proto"))

	// Log response received with status code categorization
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("forwardToTarget: SUCCESS - Response received from target - Status: %d, Method: %s, Target: %s:%s, Path: %s", 
			resp.StatusCode, r.Method, targetIP, targetPort, r.URL.Path)
	} else if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		log.Printf("forwardToTarget: WARNING - Client error from target - Status: %d, Method: %s, Target: %s:%s, Path: %s", 
			resp.StatusCode, r.Method, targetIP, targetPort, r.URL.Path)
	} else if resp.StatusCode >= 500 {
		log.Printf("forwardToTarget: WARNING - Server error from target - Status: %d, Method: %s, Target: %s:%s, Path: %s", 
			resp.StatusCode, r.Method, targetIP, targetPort, r.URL.Path)
	}

	// Copy response headers
	for key, values := range resp.Header {
		// Skip certain headers
		if strings.ToLower(key) == "connection" || strings.ToLower(key) == "upgrade" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Set status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	bytesCopied, err := io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("forwardToTarget: ERROR - Failed to copy response body - Method: %s, Target: %s:%s, Path: %s, Status: %d, Error: %v", 
			r.Method, targetIP, targetPort, r.URL.Path, resp.StatusCode, err)
		return
	}

	// Log successful response body copy
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("forwardToTarget: SUCCESS - Response proxied successfully - Status: %d, Method: %s, Target: %s:%s, Path: %s, Bytes: %d", 
			resp.StatusCode, r.Method, targetIP, targetPort, r.URL.Path, bytesCopied)
	} else {
		log.Printf("forwardToTarget: Response proxied - Status: %d, Method: %s, Target: %s:%s, Path: %s, Bytes: %d", 
			resp.StatusCode, r.Method, targetIP, targetPort, r.URL.Path, bytesCopied)
	}
}

// handleWebSocket handles WebSocket connections
func (h *ProxyHandler) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract target from headers
	targetIP, targetPort, err := h.extractTarget(r)
	if err != nil {
		log.Printf("Error extracting target: %v", err)
		http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		return
	}

	// Determine scheme
	scheme := "ws"
	if r.TLS != nil || strings.ToLower(r.Header.Get("X-Forwarded-Proto")) == "https" {
		scheme = "wss"
	}

	// Build target WebSocket URL
	targetURL := fmt.Sprintf("%s://%s:%s%s", scheme, targetIP, targetPort, r.URL.Path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	log.Printf("Forwarding WebSocket to target: %s", targetURL)

	// Upgrade client connection
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading client connection: %v", err)
		return
	}
	defer clientConn.Close()

	// Prepare headers for target connection
	headers := make(http.Header)
	h.copyHeaders(&http.Request{Header: headers}, r)

	// Connect to target WebSocket
	targetConn, _, err := websocket.DefaultDialer.Dial(targetURL, headers)
	if err != nil {
		log.Printf("Error connecting to target WebSocket: %v", err)
		return
	}
	defer targetConn.Close()

	// Proxy WebSocket messages
	go h.proxyWebSocketMessages(clientConn, targetConn)
	h.proxyWebSocketMessages(targetConn, clientConn)
}

// proxyWebSocketMessages proxies messages between two WebSocket connections
func (h *ProxyHandler) proxyWebSocketMessages(dst, src *websocket.Conn) {
	for {
		messageType, message, err := src.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		if err := dst.WriteMessage(messageType, message); err != nil {
			log.Printf("Error writing WebSocket message: %v", err)
			break
		}
	}
}
