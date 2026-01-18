package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

// RouteInfo contains routing information extracted from the request
type RouteInfo struct {
	PIKOEndpoint string
	TargetIP     string
	TargetPort   string
	Target       string // IP:Port format
}

// ExtractRouteInfo extracts routing information from request headers or URL query parameters
// Priority order: Headers (X-Forward-Target) -> CONNECT: Endpoint mappings -> CONNECT: r.Host -> Query params (if path is /proxy) -> Cookies
// This order ensures that when handleProxyEndpoint sets X-Forward-Target before modifying the path,
// the target is found immediately.
func ExtractRouteInfo(r *http.Request, endpointMappings map[string]string) (*RouteInfo, error) {
	var target string
	var targetSource string

	// First, try to extract from headers (highest priority)
	// handleProxyEndpoint sets X-Forward-Target before modifying the path, so this should be checked first
	target = r.Header.Get("X-Forward-Target")
	if target != "" {
		targetSource = "header (X-Forward-Target)"
		log.Printf("ExtractRouteInfo: Found target in header: %s", target)
	}
	
	if target == "" {
		// Try alternative headers
		targetIP := r.Header.Get("X-Target-IP")
		targetPort := r.Header.Get("X-Target-Port")
		if targetIP != "" && targetPort != "" {
			target = fmt.Sprintf("%s:%s", targetIP, targetPort)
			targetSource = "headers (X-Target-IP + X-Target-Port)"
			log.Printf("ExtractRouteInfo: Found target in alternative headers: %s", target)
		}
	}

	// For CONNECT requests, check endpoint mappings first, then r.Host
	if target == "" && r.Method == "CONNECT" && r.Host != "" {
		log.Printf("ExtractRouteInfo: Processing CONNECT request - Host: %s", r.Host)
		
		// Extract hostname from r.Host (may have port like "myendpoint:443")
		hostname := r.Host
		if colonIdx := strings.Index(r.Host, ":"); colonIdx > 0 {
			hostname = r.Host[:colonIdx]
		}
		
		// Normalize hostname to lowercase for case-insensitive lookup
		hostnameLower := strings.ToLower(hostname)
		
		log.Printf("ExtractRouteInfo: Extracted hostname from CONNECT request - Host: %s, Hostname: %s (normalized: %s)", 
			r.Host, hostname, hostnameLower)
		
		// Check endpoint mappings first (case-insensitive)
		if endpointMappings != nil {
			log.Printf("ExtractRouteInfo: Checking endpoint mappings for CONNECT - Hostname: %s (normalized: %s), Mappings available: %d, Mappings: %v", 
				hostname, hostnameLower, len(endpointMappings), endpointMappings)
			
			// First try exact match
			if mappedTarget, found := endpointMappings[hostname]; found {
				target = mappedTarget
				targetSource = "endpoint mapping (CONNECT)"
				log.Printf("ExtractRouteInfo: Found CONNECT target in endpoint mapping (exact match) - Host: %s, Hostname: %s, Target: %s", 
					r.Host, hostname, target)
			} else {
				// Try case-insensitive lookup by checking all mappings
				for key, value := range endpointMappings {
					if strings.ToLower(key) == hostnameLower {
						target = value
						targetSource = "endpoint mapping (CONNECT)"
						log.Printf("ExtractRouteInfo: Found CONNECT target in endpoint mapping (case-insensitive match) - Host: %s, Hostname: %s, Mapping key: %s, Target: %s", 
							r.Host, hostname, key, target)
						break
					}
				}
				
				if target == "" {
					log.Printf("ExtractRouteInfo: No endpoint mapping found for CONNECT hostname: %s (normalized: %s, available mappings: %v)", 
						hostname, hostnameLower, endpointMappings)
				}
			}
		} else {
			log.Printf("ExtractRouteInfo: Endpoint mappings is nil for CONNECT request")
		}
		
		// If no mapping found, use r.Host directly
		if target == "" {
			target = r.Host
			targetSource = "r.Host (CONNECT method)"
			log.Printf("ExtractRouteInfo: Using r.Host for CONNECT (no mapping found) - Target: %s", target)
		}
	}

	// Second, try to extract from URL query parameters (only if path is /proxy)
	// This handles initial /proxy requests before handleProxyEndpoint sets the header
	if target == "" && (r.URL.Path == "/proxy" || strings.HasPrefix(r.URL.Path, "/proxy/")) {
		target = r.URL.Query().Get("target")
		if target != "" {
			targetSource = "query parameter (target)"
			log.Printf("ExtractRouteInfo: Found target in query parameter: %s (path: %s)", target, r.URL.Path)
		}
		
		if target == "" {
			// Try path-based format: /proxy/192.168.1.100:8080/path
			if strings.HasPrefix(r.URL.Path, "/proxy/") {
				pathParts := strings.SplitN(r.URL.Path[7:], "/", 2) // Skip "/proxy/"
				if len(pathParts) > 0 && pathParts[0] != "" {
					target = pathParts[0]
					targetSource = "path-based format"
					log.Printf("ExtractRouteInfo: Found target in path: %s", target)
				}
			}
		}
	}

	// Third, try to get from cookie (for browser proxy scenario)
	// This handles subsequent requests after initial /proxy request
	if target == "" {
		if cookie, err := r.Cookie("proxy-target"); err == nil && cookie.Value != "" {
			target = cookie.Value
			targetSource = "cookie (proxy-target)"
			log.Printf("ExtractRouteInfo: Found target in cookie: %s", target)
		}
	}

	if target == "" {
		log.Printf("ExtractRouteInfo: Target not found - path: %s, query: %s", r.URL.Path, r.URL.RawQuery)
		return nil, fmt.Errorf("target not specified in headers or URL parameters")
	}

	log.Printf("ExtractRouteInfo: Target extracted from %s: %s", targetSource, target)

	// Clean and validate target - ensure it's just IP:Port format
	// Remove any path that might be included (e.g., "localhost:8081/path" -> "localhost:8081")
	target = strings.TrimSpace(target)
	
	// Extract only the IP:Port portion (before first slash if any)
	if slashIdx := strings.Index(target, "/"); slashIdx > 0 {
		log.Printf("Warning: Target contains path, extracting IP:Port only. Original: %s", target)
		target = target[:slashIdx]
	}

	// Parse target IP:Port
	parts := strings.Split(target, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid target format, expected IP:Port, got: %s", target)
	}

	targetIP := strings.TrimSpace(parts[0])
	targetPort := strings.TrimSpace(parts[1])
	
	// Validate that port doesn't contain invalid characters (should be just numbers)
	if strings.Contains(targetPort, "/") || strings.Contains(targetPort, "\\") {
		return nil, fmt.Errorf("invalid target format, port contains invalid characters: %s", targetPort)
	}

	// Extract PIKO endpoint from standard header (X-Piko-Endpoint)
	// This will be injected by BFF, but hardcoded to "radware-proxy" for now
	pikoEndpoint := r.Header.Get("X-Piko-Endpoint")
	if pikoEndpoint == "" {
		// Try alternative case variations
		pikoEndpoint = r.Header.Get("x-piko-endpoint")
	}
	if pikoEndpoint == "" {
		pikoEndpoint = r.Header.Get("X-PIKO-Endpoint")
	}
	if pikoEndpoint == "" {
		// Allow override via query parameter for testing
		pikoEndpoint = r.URL.Query().Get("piko_endpoint")
	}
	if pikoEndpoint == "" {
		// Hardcoded default endpoint ID for remote network site
		// This will be injected by BFF in production
		pikoEndpoint = "radware-proxy"
	}

	return &RouteInfo{
		PIKOEndpoint: pikoEndpoint,
		TargetIP:     targetIP,
		TargetPort:   targetPort,
		Target:       target,
	}, nil
}

// BuildPIKOURL constructs the PIKO Relay Server URL based on routing info
// Note: PIKO uses header-based routing (X-Piko-Endpoint header), not path-based
// This function just returns the base URL - the endpoint ID is set in headers
func BuildPIKOURL(baseURL string, routeInfo *RouteInfo) (string, error) {
	// Just return the base URL - endpoint ID is set via X-Piko-Endpoint header
	// Path-based routing is not used with PIKO
	return baseURL, nil
}
