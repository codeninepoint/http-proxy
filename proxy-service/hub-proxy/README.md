# Hub-side Custom Proxy

The Hub-side Custom Proxy service receives client requests over the internet, handles authorization through BFF, and routes requests to the PIKO Relay Server.

## Overview

This service acts as the entry point for client connections. It:
- Receives HTTP/HTTPS and WebSocket requests from clients
- Redirects unauthenticated requests to BFF for authorization
- Sets Host headers and authentication parameters
- Routes requests to PIKO Relay Server based on headers
- Uses `X-Forward-Target` header to specify target IP:Port

## Architecture

```
Client → Hub Proxy → BFF (authorization) → Hub Proxy → PIKO Relay Server
```

## Configuration

The service is configured via environment variables:

### Required Environment Variables

- `BFF_AUTH_URL`: URL of the BFF authorization endpoint (required)
- `PIKO_RELAY_URL`: Base URL of the PIKO Relay Server (default: `http://localhost:8000`)

### Optional Environment Variables

- `HUB_PROXY_PORT`: Port to listen on (default: `8080`)
- `LOG_LEVEL`: Logging level - `info`, `debug`, `warn`, `error` (default: `info`)
- `READ_TIMEOUT`: Request read timeout in seconds or duration (default: `30s`)
- `WRITE_TIMEOUT`: Response write timeout in seconds or duration (default: `30s`)
- `IDLE_TIMEOUT`: Idle connection timeout in seconds or duration (default: `5m`)

## Usage

### Building

```bash
cd hub-proxy
go mod tidy
go build -o hub-proxy
```

### Running

```bash
export BFF_AUTH_URL="https://bff.example.com/auth"
export PIKO_RELAY_URL="http://localhost:8000"
export HUB_PROXY_PORT="8080"
./hub-proxy
```

Or with all options:

```bash
BFF_AUTH_URL="https://bff.example.com/auth" \
PIKO_RELAY_URL="http://localhost:8000" \
HUB_PROXY_PORT="8080" \
LOG_LEVEL="info" \
./hub-proxy
```

## Request Flow

1. **Client Request**: Client sends HTTP/WebSocket request to Hub Proxy
2. **Authorization Check**: Hub Proxy checks for authentication headers
3. **BFF Redirect**: If not authorized, redirects to BFF for authorization
4. **Target Extraction**: Extracts target from `X-Forward-Target` header or URL query parameters
5. **PIKO Routing**: Routes to PIKO Relay Server using header-based routing
6. **Request Forwarding**: Forwards request with appropriate headers to PIKO

## Browser Proxy Configuration

The Hub Proxy supports URL-based routing for standard browser proxy configuration. This allows browsers to use the proxy without requiring custom headers.

### Browser Proxy Settings

Configure your browser to use the Hub Proxy:

**HTTP Proxy:** `hub-proxy.example.com:8080`  
**HTTPS Proxy:** `hub-proxy.example.com:8080`  
**No proxy for:** `localhost, 127.0.0.1`

### URL-Based Routing

Use the `/proxy` endpoint with query parameters:

**Format 1: Query Parameters**
```
http://hub-proxy:8080/proxy?target=192.168.1.100:8080&path=/api/endpoint
```

**Format 2: Path-Based**
```
http://hub-proxy:8080/proxy/192.168.1.100:8080/api/endpoint
```

**Parameters:**
- `target` (required): Target IP and Port in format `IP:Port` (e.g., `192.168.1.100:8080`)
- `path` (optional): The path to forward to the target service (default: `/`)
- `auth_token` (optional): Authentication token from BFF redirect (automatically converted to Authorization header)

### BFF Redirect Flow

When a user is not authenticated:
1. Hub Proxy redirects to: `BFF_AUTH_URL/api/proxyredirect?return_url=/proxy?target=...&path=...`
2. BFF validates authentication and redirects back with: `/proxy?target=...&path=...&auth_token=<JWT_TOKEN>`
3. Hub Proxy extracts `auth_token` and sets `Authorization: Bearer <token>` header
4. Request proceeds to PIKO

## Headers

### Input Headers (from Client)

- `X-Forward-Target`: Target IP and Port in format `IP:Port` (e.g., `192.168.1.100:8080`)
- `X-Piko-Endpoint`: Standard PIKO endpoint identifier (defaults to `radware-proxy` if not provided)
  - This header will be injected by BFF in production
  - Currently hardcoded to `radware-proxy` for the remote network site
  - The endpoint must be registered with PIKO agent prior to use
- `Authorization`: Authentication token (passed through)
- `X-Auth-Token`: Alternative authentication token (passed through)

### Output Headers (to PIKO)

- `X-Forward-Target`: Target IP:Port (set by Hub Proxy)
- `X-Piko-Endpoint`: PIKO endpoint identifier (from header or default `radware-proxy`)
- `Host`: Original host header
- `Authorization`: Passed through from client
- `X-Forwarded-For`: Client IP address
- `X-Forwarded-Proto`: Protocol (http/https)
- `X-Forwarded-Host`: Original host

## WebSocket Support

The service supports WebSocket connections for real-time communication. WebSocket requests are:
1. Upgraded from HTTP
2. Routed to PIKO Relay Server
3. Proxied bidirectionally between client and PIKO

## Error Handling

- **400 Bad Request**: Invalid target format or missing required headers
- **401 Unauthorized**: Missing authentication (redirects to BFF)
- **502 Bad Gateway**: Error connecting to PIKO Relay Server
- **500 Internal Server Error**: Server configuration or processing errors

## Logging

The service logs:
- Request routing information
- Target extraction
- PIKO forwarding
- Errors and warnings

Log format: `[LEVEL] message`

## Graceful Shutdown

The service handles SIGINT and SIGTERM signals for graceful shutdown:
- Stops accepting new connections
- Waits for existing connections to complete (up to 30 seconds)
- Closes all connections cleanly

## Examples

### Programmatic Usage (with Headers)

```bash
# Start the service
export BFF_AUTH_URL="https://api.example.com/auth"
export PIKO_RELAY_URL="http://piko.example.com:8000"
./hub-proxy

# Client request with headers
curl -H "X-Forward-Target: 192.168.1.100:8080" \
     -H "Authorization: Bearer token123" \
     http://localhost:8080/api/endpoint
```

### Browser Proxy Usage (URL-Based)

```bash
# Start the service
export BFF_AUTH_URL="https://bff.example.com/api/proxyredirect"
export PIKO_RELAY_URL="http://piko.example.com:8000"
./hub-proxy
```

**Browser Configuration:**
- HTTP Proxy: `hub-proxy.example.com:8080`
- HTTPS Proxy: `hub-proxy.example.com:8080`

**Access Target Service:**
```
http://hub-proxy:8080/proxy?target=192.168.1.100:8080&path=/api/endpoint
```

Or using path-based format:
```
http://hub-proxy:8080/proxy/192.168.1.100:8080/api/endpoint
```

### Complete Flow Example

1. **User accesses:** `http://hub-proxy:8080/proxy?target=192.168.1.100:8080&path=/dashboard`

2. **Hub Proxy redirects to BFF:** `https://bff.example.com/api/proxyredirect?return_url=/proxy?target=192.168.1.100:8080&path=/dashboard`

3. **BFF validates and redirects back:** `http://hub-proxy:8080/proxy?target=192.168.1.100:8080&path=/dashboard&auth_token=eyJhbGc...`

4. **Hub Proxy processes request:**
   - Extracts `auth_token` and sets `Authorization: Bearer eyJhbGc...`
   - Extracts `target=192.168.1.100:8080`
   - Uses PIKO endpoint ID from `X-Piko-Endpoint` header (or defaults to `radware-proxy`)
   - Forwards to PIKO at path `/radware-proxy` (or specified endpoint)
   - PIKO routes through tunnel to Remote Proxy (registered with same endpoint ID)
   - Remote Proxy forwards to `192.168.1.100:8080/dashboard`

**Note:** The PIKO endpoint ID (e.g., `radware-proxy`) must be registered with the PIKO agent at the remote site before use. The agent connects to PIKO server and registers this endpoint ID.
