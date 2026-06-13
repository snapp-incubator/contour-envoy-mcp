---
name: contour-envoy-mcp
description: Diagnose Contour/Envoy ingress in OKD4/OpenShift clusters using the contour-envoy MCP server tools. Use when investigating route errors, routing failures, TLS/cert problems, upstream health, or cross-referencing Contour config against live Envoy state. Triggers - "why is my route 404/503", "route invalid", "check ingress", "envoy clusters unhealthy", "cert expiring", "fqdn not resolving", "which ingress class / class / fleet / type serves this".
---

# Contour-Envoy Ingress Diagnostics

This MCP server exposes 24 read-only tools for querying **Contour HTTPProxy** CRDs and live **Envoy** admin-API state in clusters where Contour replaces the OpenShift router. Use it to map declared config (Contour) against runtime reality (Envoy).

## Terminology (our words → tool words)

Our team's terms differ from Contour/Envoy upstream naming. The MCP tool names use the upstream words; map them:

| We say | Tool/upstream word | Notes |
|--------|--------------------|-------|
| **route** | HTTPProxy | The CRD. `list_httpproxies` lists routes, `get_httpproxy` gets one route, `list_invalid_httpproxies` = invalid routes, etc. |
| **ingress class** (aka **class**, **ingress**, **fleet**, **type**) | fleet / ingress class | The Envoy ingress class. The `ingress_class` tool param takes an ingress class name (`public`, `private`, `inter-dc`, `inter-venture`, `ode-private`). `list_envoy_ingress_classes` lists ingress classes. |

When the user says "route", they mean an HTTPProxy — use the `*_httpproxy*` tools. When they say "ingress class" — or any of its aliases **class**, **ingress**, **fleet**, **type** — they mean the same thing; pass it as the `ingress_class` param. Prefer "ingress class" in your own replies.

## Tool map

**Routes (Contour HTTPProxy CRD reads):**
| Tool | Params | Use |
|------|--------|-----|
| `list_httpproxies` | `namespace?` | Inventory of routes: name, ns, FQDN, status |
| `get_httpproxy` | `name*`, `namespace*` | Full spec of one route |
| `get_httpproxy_status` | `name*`, `namespace*` | Route Valid? conditions + error detail |
| `get_httpproxy_routes` | `name*`, `namespace*` | Path matches, services, weights of a route |
| `get_httpproxy_tree` | `name*`, `namespace*` | Root + included child routes (delegation chain) |
| `search_httpproxy_by_fqdn` | `fqdn*` | Which route owns a domain (wildcard ok) |
| `search_httpproxy_by_backend` | `service_name*`, `namespace?` | Which routes point to a Service |
| `list_invalid_httpproxies` | — | All non-Valid routes (fast triage) |

**Contour TLS / extensions:**
| Tool | Params | Use |
|------|--------|-----|
| `list_tls_cert_delegations` | `namespace?` | Which TLS secrets delegated where |
| `list_extension_services` | `namespace?` | Rate-limit/tracing extensions |

**Ingress class discovery & Contour debug:**
| Tool | Params | Use |
|------|--------|-----|
| `list_envoy_ingress_classes` | — | Ingress classes + pod readiness + admin port. Call first. |
| `list_envoy_pods` | `ingress_class?` | Envoy pods: name, node, IP, ready |
| `get_contour_dag` | `ingress_class*`, `pod?` | Contour's computed routing DAG (DOT) — authoritative view of interpreted config |

**Envoy admin API** (live data plane). All take `ingress_class?`, `pod?`, `admin_port?`, `envoy_url?`:
| Tool | Extra params | Use |
|------|--------------|-----|
| `envoy_config_dump` | `resource_type?` (listener/route/cluster/endpoint/secret/scoped_route) | Full or filtered config |
| `envoy_listeners` | — | Listeners, filter chains |
| `envoy_routes` | — | Virtual hosts, match rules, cluster mappings |
| `envoy_clusters` | — | Upstreams, endpoints, circuit breakers |
| `envoy_endpoints` | — | Backend host addrs, health, LB weights |
| `envoy_stats` | `filter?` (regex), `format?` (text/json/prometheus) | Request/conn metrics |
| `envoy_clusters_health` | — | Membership, pressure, failover |
| `envoy_server_info` | — | Version, state, uptime |
| `envoy_certs` | — | Cert chains, expiry, serials |
| `envoy_ready` | — | Readiness check |
| `envoy_runtime` | — | Feature flags, runtime overrides |
| `envoy_memory` | — | Heap/allocated memory |

`*` = required. `?` = optional.

## Targeting an ingress class

Multiple Envoy ingress classes run per cluster (`public`, `private`, `inter-dc`, `inter-venture`, `ode-private`), one daemonset/deployment each in the ingress namespace. The `ingress_class` tool param is the ingress class name. Pass `ingress_class` to any `envoy_*` tool: the server picks a ready Envoy pod of that ingress class and tunnels (K8s port-forward) to the read-only admin listener Contour programs on `127.0.0.1:<adminPort>` inside the pod. Pass `pod` to inspect a specific node's daemonset instance. `envoy_url` bypasses targeting (local debugging only). Start with `list_envoy_ingress_classes` to see valid ingress class names and ports.

## Diagnostic playbooks

### Route returns 404 / not found
1. `search_httpproxy_by_fqdn` with the host → confirm a route claims it.
2. If none → no route for that domain. Done.
3. If found but unexpected → `get_httpproxy_routes` → check path match.
4. Confirm Envoy programmed it: `envoy_routes` (pass the ingress class as `ingress_class`), grep virtual host = FQDN.
   - Route exists but missing in Envoy → route likely Invalid. Go to status check.

### Route returns 503 / upstream failure
1. `search_httpproxy_by_fqdn` → get the owning route + namespace.
2. `get_httpproxy_routes` → identify backend service(s).
3. `envoy_clusters_health` (pass the ingress class as `ingress_class`) → is the target cluster's membership 0 / all unhealthy?
4. `envoy_endpoints` → are there any healthy backend hosts?
5. Empty endpoints = no ready pods / wrong service selector. Healthy but still 503 = check `envoy_stats` filter `cluster\.<name>\..*` for `upstream_rq_5xx`, circuit breaker, timeouts.

### Route shows Invalid
1. `list_invalid_httpproxies` → triage all broken routes at once.
2. `get_httpproxy_status` on the target → read conditions for exact error.
3. If error is delegation/include related → `get_httpproxy_tree` to inspect the chain.
4. Common: orphaned include, missing TLS secret, FQDN conflict with another root route.

### TLS / certificate problems
1. `envoy_certs` (pass the ingress class as `ingress_class`) → check expiry + which cert is served for the SNI.
2. Cert missing/wrong → `list_tls_cert_delegations` to verify the secret is delegated to the route's namespace.
3. `get_httpproxy` → confirm `spec.virtualhost.tls.secretName` matches.

### Config drift (Contour says X, traffic does Y)
Cross-reference is the core value: compare declared (route, `get_httpproxy*`) against live (`envoy_*`). If the route is Valid but Envoy lacks the route/cluster, suspect xDS sync lag or a Contour-internal reject — check `envoy_server_info` state and Contour pod logs. `get_contour_dag` (pass the ingress class as `ingress_class`) shows what Contour actually computed.

## Notes
- Envoy tools need a target: pass `ingress_class` (the ingress class, preferred) or `pod`. `envoy_url` / `-envoy-admin-url` is a direct-mode fallback. With no target, the server's default ingress class is used (if configured).
- For routing discrepancies (route Valid but Envoy disagrees), `get_contour_dag` shows what Contour actually computed — compare against `envoy_routes`.
- Ingress namespace comes from server flag `-contour-namespace` (snappcloud: `snappcloud-ingress`).
- All tools are read-only — safe to call freely. No mutation, no traffic impact.
- Start broad (`list_*`, `search_*`), then drill into one route. Avoid `envoy_config_dump` with no `resource_type` unless you truly need everything — it is large.
