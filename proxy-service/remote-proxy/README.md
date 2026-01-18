# Remote-side Custom Forward Proxy

The Remote-side Custom Forward Proxy service receives requests from the PIKO agent (local connection) and forwards them to the target IP and Port specified in the request headers.

## Overview

This service is deployed at the remote site and:
- Receives HTTP/HTTPS and WebSocket requests from PIKO agent (local connection)
- Extracts target IP and Port from `X-Forward-Target` header
- Forwards requests to the target service
- Supports both HTTP and WebSocket protocols

## Architecture

```
PIKO Agent (local) → Remote Forward Proxy → Target IP:Port
```

## Configuration

The service is configured via environment variables:

### Optional Environment Variables

- `REMOTE_PROXY_PORT`: Port to listen on (default: `8081`)
- `LOG_LEVEL`: Logging level - `info`, `debug`, `warn`, `error` (default: `info`)
- `CONNECTION_TIMEOUT`: Connection timeout in seconds or duration (default: `30s`)
- `READ_TIMEOUT`: Request read timeout in seconds or duration (default: `30s`)
- `WRITE_TIMEOUT`: Response write timeout in seconds or duration (default: `30s`)
- `IDLE_TIMEOUT`: Idle connection timeout in seconds or duration (default: `5m`)

## Usage

### Building

```bash
cd remote-proxy
go mod tidy
go build -o remote-proxy
```

### Running

```bash
export REMOTE_PROXY_PORT="8081"
./remote-proxy
```

Or with all options:

```bash
REMOTE_PROXY_PORT="8081" \
LOG_LEVEL="info" \
CONNECTION_TIMEOUT="30s" \
./remote-proxy
```

## Request Flow

1. **PIKO Agent Request**: PIKO agent sends request to Remote Proxy (localhost)
2. **Header Extraction**: Extracts target IP:Port from `X-Forward-Target` header
3. **Target Forwarding**: Forwards request to target IP:Port
4. **Response**: Returns target service response back through the chain

## Headers

### Input Headers (from PIKO Agent)

- `X-Forward-Target`: Target IP and Port in format `IP:Port` (e.g., `192.168.1.100:8080`)
  - Alternative: `X-Target-IP` and `X-Target-Port` headers

### Headers Forwarded to Target

All headers from the incoming request are forwarded to the target, except:
- `Host`: Set to target IP:Port
- `Connection`: Managed by transport
- `Upgrade`: Managed by transport
- `X-Forward-Target`: Removed (internal use only)
- `X-Target-IP`: Removed (internal use only)
- `X-Target-Port`: Removed (internal use only)
- `Content-Length`: Managed by transport
- `Transfer-Encoding`: Managed by transport

## Target URL Construction

The target URL is constructed as:
- **HTTP**: `http://IP:Port/path?query`
- **HTTPS**: `https://IP:Port/path?query` (if TLS or X-Forwarded-Proto: https)

The scheme is determined by:
1. Original request TLS status
2. `X-Forwarded-Proto` header
3. Defaults to `http`

## WebSocket Support

The service supports WebSocket connections:
1. Upgrades HTTP connection to WebSocket
2. Connects to target WebSocket endpoint
3. Proxies messages bidirectionally between PIKO agent and target

## Error Handling

- **400 Bad Request**: Missing or invalid `X-Forward-Target` header
- **502 Bad Gateway**: Error connecting to target service
- **500 Internal Server Error**: Server configuration or processing errors

## Logging

The service logs:
- Target extraction from headers
- Forwarding requests to target
- Errors and warnings

Log format: `[LEVEL] message`

## Connection Management

- Connection pooling for efficient resource usage
- Configurable timeouts for all connection phases
- Keep-alive connections for better performance
- Graceful connection cleanup

## Graceful Shutdown

The service handles SIGINT and SIGTERM signals for graceful shutdown:
- Stops accepting new connections
- Waits for existing connections to complete (up to 30 seconds)
- Closes all connections cleanly

## Example

```bash
# Start the service
export REMOTE_PROXY_PORT="8081"
./remote-proxy

# PIKO agent will connect to localhost:8081 with:
# X-Forward-Target: 192.168.1.100:8080
# The proxy will forward to http://192.168.1.100:8080
```

## Integration with PIKO Agent

The PIKO agent should be configured to connect to this proxy locally:

```yaml
# Example PIKO agent configuration
endpoint: "remote-proxy"
upstream: "http://localhost:8081"
```

The PIKO agent will forward requests with the `X-Forward-Target` header set, and this proxy will handle routing to the actual target service.
