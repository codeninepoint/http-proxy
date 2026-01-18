package common

// Custom header constants used across both proxy services
const (
	// XForwardTarget is the header that contains the target IP:Port
	// Format: "IP:Port" (e.g., "192.168.1.100:8080")
	XForwardTarget = "X-Forward-Target"

	// XTargetIP is an alternative header for target IP (if needed)
	XTargetIP = "X-Target-IP"

	// XTargetPort is an alternative header for target port (if needed)
	XTargetPort = "X-Target-Port"

	// XPIKOEndpoint is the standard PIKO endpoint header (case-insensitive)
	// Standard header name: X-Piko-Endpoint
	XPIKOEndpoint = "X-Piko-Endpoint"

	// XAuthToken is the header for authentication token
	XAuthToken = "X-Auth-Token"

	// XAuthorization is the standard authorization header
	XAuthorization = "Authorization"
)
