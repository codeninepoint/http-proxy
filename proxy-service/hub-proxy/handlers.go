package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins - can be restricted in production
		return true
	},
}

// ProxyHandler handles incoming requests
type ProxyHandler struct {
	config *Config
	client *http.Client
}

// NewProxyHandler creates a new proxy handler
func NewProxyHandler(config *Config) *ProxyHandler {
	return &ProxyHandler{
		config: config,
		client: &http.Client{
			Timeout: config.ReadTimeout,
		},
	}
}

// checkEndpointMapping checks if the request's Host header matches any configured endpoint mapping
// and returns the mapped target (IP:Port) if found, empty string otherwise
func (h *ProxyHandler) checkEndpointMapping(r *http.Request) string {
	if len(h.config.EndpointMappings) == 0 {
		log.Printf("checkEndpointMapping: No endpoint mappings configured")
		return ""
	}

	host := r.Host
	if host == "" {
		log.Printf("checkEndpointMapping: Host header is empty")
		return ""
	}

	// Extract hostname from Host header (may include port like "myendpoint:8085")
	// Split on colon to separate hostname from port
	hostname := host
	if colonIdx := strings.Index(host, ":"); colonIdx > 0 {
		hostname = host[:colonIdx]
	}

	// Normalize hostname to lowercase for case-insensitive lookup
	hostnameLower := strings.ToLower(hostname)

	log.Printf("checkEndpointMapping: Checking - Host: %s, Hostname: %s (normalized: %s), Mappings available: %d", 
		host, hostname, hostnameLower, len(h.config.EndpointMappings))

	// Look up in endpoint mappings (case-insensitive)
	// First try exact match, then try lowercase match
	var target string
	var found bool
	if target, found = h.config.EndpointMappings[hostname]; found {
		log.Printf("checkEndpointMapping: Found endpoint mapping (exact match) - Host: %s, Hostname: %s, Target: %s", host, hostname, target)
		return target
	}
	
	// Try case-insensitive lookup by checking all mappings
	for key, value := range h.config.EndpointMappings {
		if strings.ToLower(key) == hostnameLower {
			target = value
			found = true
			log.Printf("checkEndpointMapping: Found endpoint mapping (case-insensitive match) - Host: %s, Hostname: %s, Mapping key: %s, Target: %s", 
				host, hostname, key, target)
			return target
		}
	}

	log.Printf("checkEndpointMapping: No mapping found for hostname: %s (normalized: %s, available mappings: %v)", 
		hostname, hostnameLower, h.config.EndpointMappings)
	return ""
}

// ServeHTTP is the main request handler
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handle auth_token from BFF redirect (query parameter)
	authToken := r.URL.Query().Get("auth_token")
	if authToken != "" {
		// Set Authorization header from query parameter
		r.Header.Set("Authorization", "Bearer "+authToken)
		// Remove auth_token from query to avoid passing it downstream
		q := r.URL.Query()
		q.Del("auth_token")
		r.URL.RawQuery = q.Encode()
	}

	// Check endpoint mapping early - if request matches a configured endpoint,
	// set X-Forward-Target header automatically
	if mappedTarget := h.checkEndpointMapping(r); mappedTarget != "" {
		// Only set if X-Forward-Target is not already set (allow manual override)
		if r.Header.Get("X-Forward-Target") == "" {
			r.Header.Set("X-Forward-Target", mappedTarget)
			log.Printf("ServeHTTP: Set X-Forward-Target from endpoint mapping - Host: %s, Target: %s", r.Host, mappedTarget)
			
			// Verify the header was set correctly
			actualHeader := r.Header.Get("X-Forward-Target")
			if actualHeader == mappedTarget {
				log.Printf("ServeHTTP: Verified X-Forward-Target header set correctly - Value: %s", actualHeader)
			} else {
				log.Printf("ServeHTTP: WARNING - X-Forward-Target header mismatch! Expected: %s, Actual: %s", mappedTarget, actualHeader)
			}
		} else {
			existingHeader := r.Header.Get("X-Forward-Target")
			log.Printf("ServeHTTP: X-Forward-Target already set to: %s (endpoint mapping target was: %s, not overriding)", 
				existingHeader, mappedTarget)
		}
	}

	// Handle URL-based proxy routing (/proxy endpoint)
	if r.URL.Path == "/proxy" || strings.HasPrefix(r.URL.Path, "/proxy/") {
		h.handleProxyEndpoint(w, r)
		return
	}

	// For non-/proxy requests, redirect to /proxy?target=...&path=... format
	// This ensures all requests always have the target in the URL
	if cookie, err := r.Cookie("proxy-target"); err == nil && cookie.Value != "" {
		// We have a target from cookie, redirect to /proxy format
		target := cookie.Value
		path := r.URL.Path
		if path == "" {
			path = "/"
		}
		
		// Build redirect URL using url.Values for proper encoding
		query := url.Values{}
		query.Set("target", target)
		query.Set("path", path)
		
		// Preserve existing query parameters if any
		if r.URL.RawQuery != "" {
			existingQuery, err := url.ParseQuery(r.URL.RawQuery)
			if err == nil {
				for key, values := range existingQuery {
					// Don't overwrite target/path if they exist
					if key != "target" && key != "path" {
						for _, value := range values {
							query.Add(key, value)
						}
					}
				}
			}
		}
		
		redirectURL := "/proxy?" + query.Encode()
		log.Printf("Redirecting to /proxy format: %s (target: %s, path: %s)", redirectURL, target, path)
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}

	// Check if this is a WebSocket upgrade request
	if isWebSocket(r) {
		h.handleWebSocket(w, r)
		return
	}

	// Check if authorization is needed
	if !h.isAuthorized(r) {
		h.redirectToBFF(w, r)
		return
	}

	// Forward to PIKO
	h.forwardToPIKO(w, r)
}

// isAuthorized checks if the request is already authorized
// In a real implementation, this would validate tokens, check session, etc.
func (h *ProxyHandler) isAuthorized(r *http.Request) bool {
	// If BFF is stubbed, allow all requests
	if h.config.StubBFF {
		log.Printf("BFF validation stubbed - allowing request")
		return true
	}

	// Check for authorization header or token
	authHeader := r.Header.Get("Authorization")
	tokenHeader := r.Header.Get("X-Auth-Token")

	// For now, if either is present, consider it authorized
	// In production, this should validate the token
	return authHeader != "" || tokenHeader != ""
}

// redirectToBFF redirects the client to BFF for authorization
func (h *ProxyHandler) redirectToBFF(w http.ResponseWriter, r *http.Request) {
	// If BFF is stubbed, log and allow request to proceed
	if h.config.StubBFF {
		log.Printf("BFF redirect stubbed - allowing request without redirect")
		// Allow request to proceed by not redirecting
		h.forwardToPIKO(w, r)
		return
	}

	// Build redirect URL with return path
	redirectURL, err := url.Parse(h.config.BFFAuthURL)
	if err != nil {
		log.Printf("Error parsing BFF URL: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Add return URL as query parameter
	q := redirectURL.Query()
	q.Set("return_url", r.URL.String())
	redirectURL.RawQuery = q.Encode()

	log.Printf("Redirecting to BFF: %s", redirectURL.String())
	http.Redirect(w, r, redirectURL.String(), http.StatusTemporaryRedirect)
}

// handleProxyEndpoint handles the /proxy endpoint for URL-based routing
func (h *ProxyHandler) handleProxyEndpoint(w http.ResponseWriter, r *http.Request) {
	// Extract target and path from query parameters or URL path
	target := r.URL.Query().Get("target")
	requestPath := r.URL.Query().Get("path")

	// If target is in query but path is not, try to extract from URL path
	if target != "" && requestPath == "" {
		// Format: /proxy/192.168.1.100:8080/api/endpoint
		if strings.HasPrefix(r.URL.Path, "/proxy/") {
			pathAfterProxy := r.URL.Path[7:] // Skip "/proxy/"
			parts := strings.SplitN(pathAfterProxy, "/", 2)
			if len(parts) == 2 && parts[0] != "" {
				target = parts[0]
				requestPath = "/" + parts[1]
			} else if len(parts) == 1 && parts[0] != "" {
				// Only target, no path
				target = parts[0]
				requestPath = "/"
			}
		}
	}

	// If still no target, try to extract from headers (fallback)
	if target == "" {
		routeInfo, err := ExtractRouteInfo(r, h.config.EndpointMappings)
		if err == nil {
			target = routeInfo.Target
		}
	}

	if target == "" {
		http.Error(w, "Bad Request: target parameter is required (format: ?target=IP:Port&path=/path)", http.StatusBadRequest)
		return
	}

	// Set default path if not provided
	if requestPath == "" {
		requestPath = "/"
	}

	// Ensure path starts with /
	if !strings.HasPrefix(requestPath, "/") {
		requestPath = "/" + requestPath
	}

	// Clean target to extract just IP:Port format (remove any path for target identification)
	// But preserve the path portion if it exists in the target for forwarding
	cleanTarget := strings.TrimSpace(target)
	extractedPath := ""
	if slashIdx := strings.Index(cleanTarget, "/"); slashIdx > 0 {
		// Extract path from target if present
		extractedPath = cleanTarget[slashIdx:]
		cleanTarget = cleanTarget[:slashIdx]
		log.Printf("Warning: Target contained path, extracting IP:Port: %s, Path: %s", cleanTarget, extractedPath)
		
		// If we extracted a path from target and requestPath is default, use extracted path
		if requestPath == "" || requestPath == "/" {
			requestPath = extractedPath
		}
	}

	// Set X-Forward-Target header for downstream processing (IP:Port only)
	r.Header.Set("X-Forward-Target", cleanTarget)
	log.Printf("handleProxyEndpoint: Set X-Forward-Target header to: %s (cleaned from: %s)", cleanTarget, target)

	// Store target in cookie for subsequent requests (browser proxy scenario)
	// Cookie expires in 1 hour - store only IP:Port, not path
	cookie := &http.Cookie{
		Name:     "proxy-target",
		Value:    cleanTarget, // Store cleaned target (IP:Port only)
		Path:     "/",
		MaxAge:   3600, // 1 hour
		HttpOnly: true,
		Secure:   r.TLS != nil, // Secure in HTTPS
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)

	// Update request path - preserve the original or extracted path, don't force to /
	if requestPath == "" {
		requestPath = "/"
	}
	log.Printf("handleProxyEndpoint: Updating request path from %s to %s", r.URL.Path, requestPath)
	r.URL.Path = requestPath
	// Remove proxy-related query parameters
	q := r.URL.Query()
	q.Del("target")
	q.Del("path")
	r.URL.RawQuery = q.Encode()
	log.Printf("handleProxyEndpoint: After modification - Path: %s, Query: %s, X-Forward-Target: %s", 
		r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Forward-Target"))

	// Check if authorization is needed
	if !h.isAuthorized(r) {
		h.redirectToBFF(w, r)
		return
	}

	// Forward to PIKO
	h.forwardToPIKO(w, r)
}

// forwardToPIKO forwards the request to PIKO Relay Server
func (h *ProxyHandler) forwardToPIKO(w http.ResponseWriter, r *http.Request) {
	log.Printf("forwardToPIKO: Starting - Method: %s, Path: %s, Query: %s, X-Forward-Target header: %s", 
		r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Forward-Target"))
	
	// Extract routing information
	routeInfo, err := ExtractRouteInfo(r, h.config.EndpointMappings)
	if err != nil {
		log.Printf("forwardToPIKO: ERROR - Failed to extract route info - Method: %s, Path: %s, Query: %s, Error: %v", 
			r.Method, r.URL.Path, r.URL.RawQuery, err)
		http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		return
	}

	// Log request path - for CONNECT requests, path is intentionally empty
	requestPathLog := r.URL.Path
	if r.Method == "CONNECT" && requestPathLog == "" {
		requestPathLog = "(empty - CONNECT tunnel)"
	} else if requestPathLog == "" {
		requestPathLog = "/"
	}
	log.Printf("forwardToPIKO: Route info extracted - Target: %s, PIKO Endpoint: %s, Request Path: %s", 
		routeInfo.Target, routeInfo.PIKOEndpoint, requestPathLog)

	// Build PIKO URL (base URL only - endpoint is in header, not path)
	pikoURL, err := BuildPIKOURL(h.config.PIKORelayURL, routeInfo)
	if err != nil {
		log.Printf("forwardToPIKO: ERROR - Failed to build PIKO URL - Target: %s, PIKO Endpoint: %s, Path: %s, Base URL: %s, Error: %v", 
			routeInfo.Target, routeInfo.PIKOEndpoint, r.URL.Path, h.config.PIKORelayURL, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Always append the request path to PIKO URL
	// PIKO uses header-based routing (X-Piko-Endpoint), not path-based
	// The path is forwarded to the target service
	pikoURLParsed, err := url.Parse(pikoURL)
	if err != nil {
		log.Printf("forwardToPIKO: ERROR - Failed to parse PIKO URL - Target: %s, PIKO Endpoint: %s, Path: %s, URL: %s, Error: %v", 
			routeInfo.Target, routeInfo.PIKOEndpoint, r.URL.Path, pikoURL, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Set the path
	// For CONNECT requests, don't set a path (CONNECT is for tunneling, no path)
	// For other requests, use the request path (default to "/" if empty)
	requestPath := r.URL.Path
	if r.Method == "CONNECT" {
		// CONNECT requests don't use paths - they establish a tunnel
		// Leave path empty for CONNECT requests
		requestPath = ""
		pikoURLParsed.Path = ""
	} else {
		// For non-CONNECT requests, always include the path (default to "/" if empty)
		if requestPath == "" {
			requestPath = "/"
			pikoURLParsed.Path = "/"
		} else {
			pikoURLParsed.Path = requestPath
		}
	}

	// Preserve query parameters (CONNECT requests typically don't have query params)
	if r.URL.RawQuery != "" {
		pikoURLParsed.RawQuery = r.URL.RawQuery
	}

	pikoURL = pikoURLParsed.String()

	// Log path clearly - show "(empty)" for CONNECT requests
	pathLog := requestPath
	if r.Method == "CONNECT" && pathLog == "" {
		pathLog = "(empty - CONNECT tunnel)"
	} else if pathLog == "" {
		pathLog = "/"
	}
	
	log.Printf("forwardToPIKO: Forwarding to PIKO - URL: %s, Endpoint: %s, Target: %s, Path: %s, Query: %s", 
		pikoURL, routeInfo.PIKOEndpoint, routeInfo.Target, pathLog, r.URL.RawQuery)

	// Create new request to PIKO
	pikoReq, err := http.NewRequest(r.Method, pikoURL, r.Body)
	if err != nil {
		log.Printf("forwardToPIKO: ERROR - Failed to create PIKO request - Method: %s, URL: %s, Target: %s, Path: %s, Error: %v", 
			r.Method, pikoURL, routeInfo.Target, requestPath, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Copy and set headers
	h.setHeadersForPIKO(pikoReq, r, routeInfo)

	// Forward the request
	resp, err := h.client.Do(pikoReq)
	if err != nil {
		log.Printf("forwardToPIKO: ERROR - Failed to forward request to PIKO - Method: %s, URL: %s, Target: %s, Path: %s, Error: %v", 
			r.Method, pikoURL, routeInfo.Target, requestPath, err)
		http.Error(w, "Gateway error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Log response received with status code categorization
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("forwardToPIKO: SUCCESS - Response received from PIKO - Status: %d, Method: %s, Target: %s, Path: %s", 
			resp.StatusCode, r.Method, routeInfo.Target, requestPath)
	} else if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		log.Printf("forwardToPIKO: WARNING - Client error from PIKO - Status: %d, Method: %s, Target: %s, Path: %s", 
			resp.StatusCode, r.Method, routeInfo.Target, requestPath)
	} else if resp.StatusCode >= 500 {
		log.Printf("forwardToPIKO: WARNING - Server error from PIKO - Status: %d, Method: %s, Target: %s, Path: %s", 
			resp.StatusCode, r.Method, routeInfo.Target, requestPath)
	}

	// Copy and rewrite response headers
	// Rewrite Location header to include target if it's a redirect
	// Pass original request to maintain endpoint in redirects
	h.copyAndRewriteResponseHeaders(w, resp, r, routeInfo)

	// Set status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	bytesCopied, err := io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("forwardToPIKO: ERROR - Failed to copy response body - Method: %s, Target: %s, Path: %s, Status: %d, Error: %v", 
			r.Method, routeInfo.Target, requestPath, resp.StatusCode, err)
		return
	}

	// Log successful response body copy
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("forwardToPIKO: SUCCESS - Response proxied successfully - Status: %d, Method: %s, Target: %s, Path: %s, Bytes: %d", 
			resp.StatusCode, r.Method, routeInfo.Target, requestPath, bytesCopied)
	} else {
		log.Printf("forwardToPIKO: Response proxied - Status: %d, Method: %s, Target: %s, Path: %s, Bytes: %d", 
			resp.StatusCode, r.Method, routeInfo.Target, requestPath, bytesCopied)
	}
}

// copyAndRewriteResponseHeaders copies response headers and rewrites Location headers to include target
// Passes original request to maintain endpoint in redirects
func (h *ProxyHandler) copyAndRewriteResponseHeaders(w http.ResponseWriter, resp *http.Response, originalReq *http.Request, routeInfo *RouteInfo) {
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		
		// Rewrite Location header to include target
		if lowerKey == "location" {
			for _, value := range values {
				rewrittenLocation := h.rewriteLocationHeader(value, originalReq, routeInfo)
				w.Header().Add(key, rewrittenLocation)
			}
			continue
		}
		
		// Copy all other headers as-is
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

// rewriteLocationHeader rewrites a Location header to maintain the current endpoint
// If redirect is to a different domain, it's rewritten to use the current endpoint (original request's Host)
// This ensures redirects stay within the same endpoint (e.g., esociety.point9.co.in stays on esociety)
func (h *ProxyHandler) rewriteLocationHeader(location string, originalReq *http.Request, routeInfo *RouteInfo) string {
	// Parse the location URL
	locURL, err := url.Parse(location)
	if err != nil {
		// If we can't parse, return original
		log.Printf("rewriteLocationHeader: Warning - Could not parse Location header: %s", location)
		return location
	}

	// Extract path from location
	path := locURL.Path
	if path == "" {
		path = "/"
	}

	// Check if this is an absolute URL to a different domain
	if locURL.IsAbs() {
		redirectHost := locURL.Host
		// Remove port if present for comparison
		if colonIdx := strings.Index(redirectHost, ":"); colonIdx > 0 {
			redirectHost = redirectHost[:colonIdx]
		}
		
		originalHost := originalReq.Host
		// Remove port if present for comparison
		if colonIdx := strings.Index(originalHost, ":"); colonIdx > 0 {
			originalHost = originalHost[:colonIdx]
		}
		
		// If redirect is to a different domain, rewrite it to maintain the current endpoint
		// This ensures redirects stay within the same endpoint (e.g., esociety.point9.co.in)
		// When target redirects to identity.point9.co.in, we rewrite it to esociety.point9.co.in
		if !strings.EqualFold(redirectHost, originalHost) {
			// Always rewrite to maintain current endpoint
			// Use the original request's Host (current endpoint) with the redirect path
			scheme := "http"
			if originalReq.TLS != nil || strings.ToLower(originalReq.Header.Get("X-Forwarded-Proto")) == "https" {
				scheme = "https"
			}
			
			// Build URL using current endpoint with redirect path
			rewrittenURL := fmt.Sprintf("%s://%s%s", scheme, originalReq.Host, path)
			if locURL.RawQuery != "" {
				rewrittenURL += "?" + locURL.RawQuery
			}
			
			log.Printf("rewriteLocationHeader: Rewriting redirect to maintain endpoint - Original: %s, Rewritten: %s (maintaining endpoint: %s)", 
				location, rewrittenURL, originalReq.Host)
			return rewrittenURL
		}
		
		// Same domain redirect - extract path and use endpoint mapping format
		// Fall through to build /proxy?target=... format
	}

	// For relative URLs or same-domain absolute URLs, use /proxy?target=... format
	// This works with endpoint mapping - the endpoint mapping will handle the routing
	// Build new URL in /proxy format using url.Values for proper encoding
	query := url.Values{}
	query.Set("target", routeInfo.Target)
	query.Set("path", path)
	
	// Preserve query parameters from original Location header
	if locURL.RawQuery != "" {
		existingQuery, err := url.ParseQuery(locURL.RawQuery)
		if err == nil {
			for key, values := range existingQuery {
				for _, value := range values {
					query.Add(key, value)
				}
			}
		}
	}
	
	proxyURL := "/proxy?" + query.Encode()
	log.Printf("rewriteLocationHeader: Rewriting redirect to /proxy format - Original: %s, Rewritten: %s", location, proxyURL)
	return proxyURL
}

// setHeadersForPIKO sets appropriate headers for PIKO request
func (h *ProxyHandler) setHeadersForPIKO(pikoReq *http.Request, originalReq *http.Request, routeInfo *RouteInfo) {
	// Copy all headers from original request
	for key, values := range originalReq.Header {
		// Skip certain headers that should be set explicitly
		lowerKey := strings.ToLower(key)
		if lowerKey == "host" || lowerKey == "x-piko-endpoint" {
			continue
		}
		for _, value := range values {
			pikoReq.Header.Add(key, value)
		}
	}

	// Set Host header
	pikoReq.Host = originalReq.Host

	// Set X-Piko-Endpoint header for PIKO routing (header-based, not path-based)
	pikoReq.Header.Set("X-Piko-Endpoint", routeInfo.PIKOEndpoint)

	// Set X-Forward-Target header with target IP:Port
	pikoReq.Header.Set("X-Forward-Target", routeInfo.Target)

	// Set authentication headers (pass through)
	h.setAuthHeaders(pikoReq, originalReq)

	// Set X-Forwarded-* headers
	pikoReq.Header.Set("X-Forwarded-For", originalReq.RemoteAddr)
	pikoReq.Header.Set("X-Forwarded-Proto", getScheme(originalReq))
	pikoReq.Header.Set("X-Forwarded-Host", originalReq.Host)
}

// setAuthHeaders sets authentication parameters in headers
func (h *ProxyHandler) setAuthHeaders(pikoReq *http.Request, originalReq *http.Request) {
	// Pass through Authorization header
	if auth := originalReq.Header.Get("Authorization"); auth != "" {
		pikoReq.Header.Set("Authorization", auth)
	}

	// Pass through X-Auth-Token header
	if token := originalReq.Header.Get("X-Auth-Token"); token != "" {
		pikoReq.Header.Set("X-Auth-Token", token)
	}

	// Pass through any other auth-related headers
	for key, values := range originalReq.Header {
		lowerKey := strings.ToLower(key)
		if strings.HasPrefix(lowerKey, "x-auth-") || strings.HasPrefix(lowerKey, "x-token-") {
			for _, value := range values {
				pikoReq.Header.Add(key, value)
			}
		}
	}
}

// handleWebSocket handles WebSocket connections
func (h *ProxyHandler) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Check authorization
	if !h.isAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract routing information
	routeInfo, err := ExtractRouteInfo(r, h.config.EndpointMappings)
	if err != nil {
		log.Printf("Error extracting route info: %v", err)
		http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		return
	}

	// Build PIKO WebSocket URL
	pikoURL, err := BuildPIKOURL(h.config.PIKORelayURL, routeInfo)
	if err != nil {
		log.Printf("Error building PIKO URL: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Convert http:// to ws:// or https:// to wss://
	wsURL := strings.Replace(pikoURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)

	log.Printf("Forwarding WebSocket to PIKO: %s (target: %s)", wsURL, routeInfo.Target)

	// Upgrade client connection
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading client connection: %v", err)
		return
	}
	defer clientConn.Close()

	// Connect to PIKO WebSocket
	headers := make(http.Header)
	h.setHeadersForPIKO(&http.Request{Header: headers}, r, routeInfo)

	pikoConn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		log.Printf("Error connecting to PIKO WebSocket: %v", err)
		return
	}
	defer pikoConn.Close()

	// Proxy WebSocket messages
	go h.proxyWebSocketMessages(clientConn, pikoConn)
	h.proxyWebSocketMessages(pikoConn, clientConn)
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

// Helper functions

func isWebSocket(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket" &&
		strings.ToLower(r.Header.Get("Connection")) == "upgrade"
}

func getScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if scheme := r.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	return "http"
}
