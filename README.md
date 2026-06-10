# Contour-Envoy MCP Server

An MCP (Model Context Protocol) server for querying **Contour** and **Envoy** ingress controller state in OKD4/OpenShift clusters. Designed for integration with AI agents, chat bots, and LLM-powered tooling.

## Overview

In OKD4 clusters where the default OpenShift router has been replaced with **Project Contour** (using Envoy as the data plane), this MCP server provides AI agents with real-time visibility into:

- **Contour HTTPProxy** CRDs (status, routes, virtual hosts, delegation trees)
- **Envoy admin API** (config dump, listeners, routes, clusters, stats, certs, health)
- **Cross-referencing** between Contour configuration and Envoy runtime state

## Features

### Contour HTTPProxy Tools
| Tool | Description |
|------|-------------|
| `list_httpproxies` | List all HTTPProxies with name, namespace, FQDN, status |
| `get_httpproxy` | Get full details of a specific HTTPProxy |
| `get_httpproxy_status` | Get status and conditions of an HTTPProxy |
| `get_httpproxy_routes` | Get the routes defined in an HTTPProxy |
| `get_httpproxy_tree` | Get the full delegation tree (root + includes) |
| `search_httpproxy_by_fqdn` | Find HTTPProxies matching a domain name |
| `search_httpproxy_by_backend` | Find HTTPProxies routing to a specific service |
| `list_invalid_httpproxies` | List all HTTPProxies with non-Valid status |

### Contour TLS & Extension Tools
| Tool | Description |
|------|-------------|
| `list_tls_cert_delegations` | List TLSCertificateDelegation resources |
| `list_extension_services` | List ExtensionService resources |

### Envoy Admin API Tools
| Tool | Description |
|------|-------------|
| `envoy_config_dump` | Full or filtered Envoy configuration dump |
| `envoy_listeners` | Envoy listener configuration |
| `envoy_routes` | Envoy route configuration |
| `envoy_clusters` | Envoy cluster (upstream) configuration |
| `envoy_endpoints` | Envoy endpoint (backend host) details |
| `envoy_stats` | Envoy server statistics (with filtering) |
| `envoy_clusters_health` | Cluster health and membership status |
| `envoy_server_info` | Envoy version, state, uptime |
| `envoy_certs` | TLS certificate details and expiration |
| `envoy_ready` | Envoy readiness check |
| `envoy_runtime` | Envoy runtime configuration |
| `envoy_memory` | Envoy memory allocation details |

### Fleet Discovery & Contour Debug Tools
| Tool | Description |
|------|-------------|
| `list_envoy_fleets` | List Envoy fleets (ingress classes) with pod readiness and resolved admin port |
| `list_envoy_pods` | List Envoy pods, optionally filtered by fleet |
| `get_contour_dag` | Contour's computed routing DAG (DOT format) from the debug server |

## How Envoy admin access works

Contour binds the Envoy admin interface to a unix socket and programs a
read-only allowlist listener on `127.0.0.1:<adminPort>` inside each Envoy pod
(`ContourConfiguration spec.envoy.network.adminPort`, default `9001`). That
listener is intentionally not reachable over the pod network.

This server reaches it with a Kubernetes **port-forward** tunnel
(`pods/portforward`), which connects inside the pod's network namespace where
localhost-bound ports are reachable. Every `envoy_*` tool accepts:

- `fleet` — ingress class (e.g. `public`, `private`, `inter-dc`); a ready
  Envoy pod of that fleet is picked automatically. The admin port is resolved
  from the fleet's ContourConfiguration.
- `pod` — a specific Envoy pod name (daemonset state differs per node).
- `admin_port` — explicit admin port override.
- `envoy_url` — direct admin URL, bypassing pod targeting (local debugging or
  deployments that expose the admin endpoint on the network).

Only the read-only allowlist endpoints are reachable through this path
(`/config_dump`, `/clusters`, `/listeners`, `/certs`, `/memory`, `/ready`,
`/runtime`, `/server_info`, `/stats`); mutating admin endpoints are blocked by
Envoy itself.

The Contour debug server (`/debug/dag`, also localhost-bound, default port
`6060`) is reached the same way by `get_contour_dag`.

## Quick Start

### Build

```bash
make build
```

### Run with stdio transport (default)

```bash
# Uses in-cluster Kubernetes config
./bin/contour-envoy-mcp

# Or specify kubeconfig
./bin/contour-envoy-mcp -kubeconfig ~/.kube/config

# With Envoy admin access
./bin/contour-envoy-mcp -envoy-admin-url http://envoy.projectcontour:9001
```

### Run with HTTP transport

```bash
./bin/contour-envoy-mcp -transport streamable-http -addr :8080
```

### Docker

```bash
docker build -t contour-envoy-mcp .
docker run -i contour-envoy-mcp
```

## Configuration

### Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-transport` | `stdio` | Transport mode: `stdio` or `streamable-http` |
| `-addr` | `:8080` | Listen address for HTTP transport |
| `-kubeconfig` | (auto) | Path to kubeconfig file |
| `-context` | (current) | Kubernetes context to use |
| `-envoy-admin-url` | (none) | Envoy admin API base URL |
| `-contour-namespace` | `projectcontour` | Default namespace for Contour resources |
| `-version` | | Print version and exit |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECONFIG` | Path to kubeconfig file (alternative to `-kubeconfig` flag) |

## Integration with AI Agents

### Claude Code / Claude Desktop

Add to your MCP client configuration:

```json
{
  "mcpServers": {
    "contour-envoy": {
      "command": "contour-envoy-mcp",
      "args": ["-kubeconfig", "/path/to/kubeconfig", "-envoy-admin-url", "http://envoy.projectcontour:9001"]
    }
  }
}
```

### HTTP Mode (for remote agents)

```json
{
  "mcpServers": {
    "contour-envoy": {
      "url": "http://contour-envoy-mcp:8080/mcp"
    }
  }
}
```

### Example Queries for AI Agents

Once connected, the AI agent can answer questions like:

- "List all HTTPProxies in the production namespace"
- "Show me the full HTTPProxy tree for app.example.com"
- "Which HTTPProxies are invalid or have errors?"
- "What routes are defined in the admin-portal HTTPProxy?"
- "Find all proxies routing to the payment-service backend"
- "Show me the Envoy listener configuration"
- "Get Envoy cluster health status"
- "Check TLS certificate expiration dates"
- "What are the current Envoy stats for the checkout cluster?"
- "Is Envoy ready to accept traffic?"

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                   AI Agent / Chat Bot                │
│                    (MCP Client)                      │
└──────────────────────┬──────────────────────────────┘
                       │ MCP Protocol (stdio / HTTP)
┌──────────────────────┴──────────────────────────────┐
│              contour-envoy-mcp server                │
│  ┌─────────────┐  ┌──────────────┐                  │
│  │ Contour     │  │ Envoy Admin  │                  │
│  │ Tools (10)  │  │ Tools (11)   │                  │
│  └──────┬──────┘  └──────┬───────┘                  │
│         │                │                           │
│  ┌──────┴──────┐  ┌──────┴───────┐                  │
│  │ Contour     │  │ Envoy Admin  │                  │
│  │ Client      │  │ Client       │                  │
│  └──────┬──────┘  └──────┬───────┘                  │
└─────────┼────────────────┼──────────────────────────┘
          │                │
          ▼                ▼
   ┌──────────────┐ ┌──────────────┐
   │  Kubernetes   │ │  Envoy       │
   │  API Server   │ │  Admin API   │
   │  (CRDs)       │ │  (port 9001) │
   └──────────────┘ └──────────────┘
```

## Development

```bash
# Build
make build

# Run tests
make test

# Lint
make lint

# Run locally with stdio
make run

# Run locally with HTTP
make run-http

# Tidy dependencies
make tidy
```

## OKD4 Deployment

### As a Deployment with stdio (sidecar pattern)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: contour-envoy-mcp
  namespace: projectcontour
spec:
  replicas: 1
  selector:
    matchLabels:
      app: contour-envoy-mcp
  template:
    metadata:
      labels:
        app: contour-envoy-mcp
    spec:
      serviceAccountName: contour-envoy-mcp
      containers:
      - name: contour-envoy-mcp
        image: your-registry/contour-envoy-mcp:latest
        args:
        - -transport=stdio
        - -contour-namespace=projectcontour
        - -envoy-admin-url=http://envoy.projectcontour:9001
```

### As a Deployment with HTTP transport

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: contour-envoy-mcp
  namespace: projectcontour
spec:
  replicas: 1
  selector:
    matchLabels:
      app: contour-envoy-mcp
  template:
    metadata:
      labels:
        app: contour-envoy-mcp
    spec:
      serviceAccountName: contour-envoy-mcp
      containers:
      - name: contour-envoy-mcp
        image: your-registry/contour-envoy-mcp:latest
        args:
        - -transport=streamable-http
        - -addr=:8080
        - -contour-namespace=projectcontour
        ports:
        - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: contour-envoy-mcp
  namespace: projectcontour
spec:
  selector:
    app: contour-envoy-mcp
  ports:
  - port: 8080
    targetPort: 8080
```

### Required RBAC

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: contour-envoy-mcp
  namespace: projectcontour
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: contour-envoy-mcp
rules:
- apiGroups: ["projectcontour.io"]
  resources:
  - httpproxies
  - tlscertificatedelegations
  - extensionservices
  - contourconfigurations
  verbs: ["get", "list", "watch"]
- apiGroups: ["gateway.networking.k8s.io"]
  resources:
  - httproutes
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: contour-envoy-mcp
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: contour-envoy-mcp
subjects:
- kind: ServiceAccount
  name: contour-envoy-mcp
  namespace: projectcontour
---
# Namespaced: pod discovery + port-forward to the localhost-bound Envoy admin
# listener and Contour debug server, scoped to the ingress namespace.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: contour-envoy-mcp-portforward
  namespace: projectcontour
rules:
- apiGroups: [""]
  resources:
  - pods
  verbs: ["get", "list"]
- apiGroups: [""]
  resources:
  - pods/portforward
  verbs: ["create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: contour-envoy-mcp-portforward
  namespace: projectcontour
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: contour-envoy-mcp-portforward
subjects:
- kind: ServiceAccount
  name: contour-envoy-mcp
  namespace: projectcontour
```

## License

Apache License 2.0
