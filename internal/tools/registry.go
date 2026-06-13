// Package tools implements the MCP tool registry and all Contour/Envoy tool handlers.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/snapp-incubator/contour-envoy-mcp/internal/contour"
	"github.com/snapp-incubator/contour-envoy-mcp/internal/envoy"
	"github.com/snapp-incubator/contour-envoy-mcp/internal/k8s"
)

// Pod label conventions of the Bitnami Contour chart: each ingress class (an
// class) is a chart alias, so the ingress class name lands in app.kubernetes.io/name
// and the role (envoy / contour) in app.kubernetes.io/component.
const (
	componentLabel    = "app.kubernetes.io/component"
	ingressClassLabel = "app.kubernetes.io/name"

	envoySelector   = componentLabel + "=envoy"
	contourSelector = componentLabel + "=contour"
)

// PodLister provides pod discovery for ingress-class-targeted tools.
type PodLister interface {
	ListPods(ctx context.Context, namespace, labelSelector string) ([]k8s.PodInfo, error)
}

// Registry holds all MCP tool definitions and their handlers.
type Registry struct {
	contourClient       *contour.Client
	envoyClient         *envoy.AdminClient
	pods                PodLister
	ingressNS           string
	defaultIngressClass string
	toolCount           int
}

// SetDefaultIngressClass sets the ingress class targeted when an envoy_* tool call passes no
// ingress class, pod, or envoy_url.
func (r *Registry) SetDefaultIngressClass(ingressClass string) {
	r.defaultIngressClass = ingressClass
}

// NewRegistry creates a new tool registry with the given clients.
// ingressNamespace is where the Contour and Envoy ingress classes run.
func NewRegistry(contourClient *contour.Client, envoyClient *envoy.AdminClient, pods PodLister, ingressNamespace string) *Registry {
	return &Registry{
		contourClient: contourClient,
		envoyClient:   envoyClient,
		pods:          pods,
		ingressNS:     ingressNamespace,
	}
}

// ToolCount returns the number of registered tools.
func (r *Registry) ToolCount() int {
	return r.toolCount
}

// RegisterAll registers all Contour and Envoy tools with the MCP server.
func (r *Registry) RegisterAll(s *server.MCPServer) error {
	// ─── Contour HTTPProxy Tools ───
	r.register(s, "list_httpproxies",
		mcp.NewTool("list_httpproxies",
			mcp.WithDescription("List all Contour HTTPProxy resources. Returns name, namespace, FQDN, and status for each proxy."),
			mcp.WithString("namespace",
				mcp.Description("Kubernetes namespace to filter by. Leave empty for all namespaces."),
			),
		),
		r.handleListHTTPProxies,
	)

	r.register(s, "get_httpproxy",
		mcp.NewTool("get_httpproxy",
			mcp.WithDescription("Get full details of a specific Contour HTTPProxy resource."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Name of the HTTPProxy resource."),
			),
			mcp.WithString("namespace",
				mcp.Required(),
				mcp.Description("Namespace of the HTTPProxy resource."),
			),
		),
		r.handleGetHTTPProxy,
	)

	r.register(s, "get_httpproxy_status",
		mcp.NewTool("get_httpproxy_status",
			mcp.WithDescription("Get the status and conditions of a Contour HTTPProxy. Shows if the proxy is Valid and describes any errors."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Name of the HTTPProxy resource."),
			),
			mcp.WithString("namespace",
				mcp.Required(),
				mcp.Description("Namespace of the HTTPProxy resource."),
			),
		),
		r.handleGetHTTPProxyStatus,
	)

	r.register(s, "get_httpproxy_routes",
		mcp.NewTool("get_httpproxy_routes",
			mcp.WithDescription("Get the routes defined in a Contour HTTPProxy. Shows path matching, services, and weight distributions."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Name of the HTTPProxy resource."),
			),
			mcp.WithString("namespace",
				mcp.Required(),
				mcp.Description("Namespace of the HTTPProxy resource."),
			),
		),
		r.handleGetHTTPProxyRoutes,
	)

	r.register(s, "get_httpproxy_tree",
		mcp.NewTool("get_httpproxy_tree",
			mcp.WithDescription("Get the full HTTPProxy tree including the root proxy and all included (child) proxies. Useful for understanding the delegation chain."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Name of the root HTTPProxy resource."),
			),
			mcp.WithString("namespace",
				mcp.Required(),
				mcp.Description("Namespace of the root HTTPProxy resource."),
			),
		),
		r.handleGetHTTPProxyTree,
	)

	r.register(s, "search_httpproxy_by_fqdn",
		mcp.NewTool("search_httpproxy_by_fqdn",
			mcp.WithDescription("Search for Contour HTTPProxies that match a given fully qualified domain name (FQDN). Supports wildcard matching."),
			mcp.WithString("fqdn",
				mcp.Required(),
				mcp.Description("Fully qualified domain name to search for (e.g. 'app.example.com')."),
			),
		),
		r.handleSearchByFQDN,
	)

	r.register(s, "search_httpproxy_by_backend",
		mcp.NewTool("search_httpproxy_by_backend",
			mcp.WithDescription("Search for Contour HTTPProxies that route to a specific backend service."),
			mcp.WithString("service_name",
				mcp.Required(),
				mcp.Description("Name of the backend Kubernetes Service."),
			),
			mcp.WithString("namespace",
				mcp.Description("Namespace of the backend Service. Leave empty to search all namespaces."),
			),
		),
		r.handleSearchByBackend,
	)

	r.register(s, "list_invalid_httpproxies",
		mcp.NewTool("list_invalid_httpproxies",
			mcp.WithDescription("List all Contour HTTPProxies with non-Valid status. Useful for quickly finding broken or misconfigured proxies."),
		),
		r.handleListInvalidProxies,
	)

	// ─── Contour TLSCertificateDelegation Tools ───
	r.register(s, "list_tls_cert_delegations",
		mcp.NewTool("list_tls_cert_delegations",
			mcp.WithDescription("List Contour TLSCertificateDelegation resources. Shows which TLS secrets are delegated to which namespaces."),
			mcp.WithString("namespace",
				mcp.Description("Kubernetes namespace to filter by. Leave empty for all namespaces."),
			),
		),
		r.handleListTLSCertDelegations,
	)

	// ─── Contour ExtensionService Tools ───
	r.register(s, "list_extension_services",
		mcp.NewTool("list_extension_services",
			mcp.WithDescription("List Contour ExtensionService resources. These configure global rate limiting, tracing, and other extensions."),
			mcp.WithString("namespace",
				mcp.Description("Kubernetes namespace to filter by. Defaults to the Contour namespace."),
			),
		),
		r.handleListExtensionServices,
	)

	// ─── Envoy Admin API Tools ───
	r.register(s, "envoy_config_dump",
		mcp.NewTool("envoy_config_dump",
			mcp.WithDescription("Get the full Envoy configuration dump via the admin API. Returns listeners, clusters, routes, endpoints, and secrets."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
			mcp.WithString("resource_type",
				mcp.Description("Filter by resource type: listener, route, cluster, endpoint, secret, scoped_route."),
			),
		),
		r.handleEnvoyConfigDump,
	)

	r.register(s, "envoy_listeners",
		mcp.NewTool("envoy_listeners",
			mcp.WithDescription("Get Envoy listener configuration. Shows all listeners, their filter chains, and associated routes."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyListeners,
	)

	r.register(s, "envoy_routes",
		mcp.NewTool("envoy_routes",
			mcp.WithDescription("Get Envoy route configuration. Shows virtual hosts, route matching rules, and cluster mappings."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyRoutes,
	)

	r.register(s, "envoy_clusters",
		mcp.NewTool("envoy_clusters",
			mcp.WithDescription("Get Envoy cluster configuration. Shows upstream clusters, endpoints, health status, and circuit breaker settings."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyClusters,
	)

	r.register(s, "envoy_endpoints",
		mcp.NewTool("envoy_endpoints",
			mcp.WithDescription("Get Envoy endpoint configuration. Shows upstream host addresses, health status, and load balancing weights."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyEndpoints,
	)

	r.register(s, "envoy_stats",
		mcp.NewTool("envoy_stats",
			mcp.WithDescription("Get Envoy server statistics. Includes request counts, connection metrics, and per-cluster stats."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
			mcp.WithString("filter",
				mcp.Description("Regex filter for stats (e.g. 'cluster\\..*\\.(membership|rq)')."),
			),
			mcp.WithString("format",
				mcp.Description("Output format: text (default), json, prometheus."),
			),
		),
		r.handleEnvoyStats,
	)

	r.register(s, "envoy_clusters_health",
		mcp.NewTool("envoy_clusters_health",
			mcp.WithDescription("Get Envoy cluster health summary. Shows membership status, pressure, and failover information for each cluster."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyClustersHealth,
	)

	r.register(s, "envoy_server_info",
		mcp.NewTool("envoy_server_info",
			mcp.WithDescription("Get Envoy server information including version, uptime, current state, and hot restart status."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyServerInfo,
	)

	r.register(s, "envoy_certs",
		mcp.NewTool("envoy_certs",
			mcp.WithDescription("Get TLS certificate information from Envoy. Shows certificate chains, expiration dates, and serial numbers."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyCerts,
	)

	r.register(s, "envoy_ready",
		mcp.NewTool("envoy_ready",
			mcp.WithDescription("Check if Envoy is ready to accept traffic. Returns live if ready, or an error if not."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyReady,
	)

	r.register(s, "envoy_runtime",
		mcp.NewTool("envoy_runtime",
			mcp.WithDescription("Get Envoy runtime configuration. Shows feature flags and runtime override values."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target (e.g. public, private, inter-dc, inter-venture, ode-private). A ready Envoy pod of that ingress class is reached via Kubernetes port-forward. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override. Defaults to the ingress class ContourConfiguration spec.envoy.network.adminPort (Contour default 9001)."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyRuntime,
	)

	r.register(s, "envoy_memory",
		mcp.NewTool("envoy_memory",
			mcp.WithDescription("Get Envoy memory allocation details (heap size, allocated, page heap)."),
			mcp.WithString("ingress_class",
				mcp.Description("Envoy ingress class to target. Use list_envoy_ingress_classes to discover."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Envoy pod name to target. Defaults to the first ready pod of the ingress class."),
			),
			mcp.WithNumber("admin_port",
				mcp.Description("Envoy admin port override."),
			),
			mcp.WithString("envoy_url",
				mcp.Description("Direct Envoy admin API base URL (advanced; bypasses ingress-class/pod targeting)."),
			),
		),
		r.handleEnvoyMemory,
	)

	// ─── IngressClass Discovery & Contour Debug Tools ───

	r.register(s, "list_envoy_ingress_classes",
		mcp.NewTool("list_envoy_ingress_classes",
			mcp.WithDescription("List Envoy ingress classes running in the ingress namespace, with pod readiness counts and the admin port resolved from each ingress class ContourConfiguration. Use this first to discover valid 'ingress_class' values for the envoy_* tools."),
		),
		r.handleListEnvoyIngressClasses,
	)

	r.register(s, "list_envoy_pods",
		mcp.NewTool("list_envoy_pods",
			mcp.WithDescription("List Envoy pods in the ingress namespace, optionally filtered by ingress class. Shows pod name, node, IP, phase, and readiness. Useful for targeting a specific pod with the envoy_* tools (daemonset state differs per node)."),
			mcp.WithString("ingress_class",
				mcp.Description("Ingress class to filter by (e.g. public). Leave empty for all ingress classes."),
			),
		),
		r.handleListEnvoyPods,
	)

	r.register(s, "get_contour_dag",
		mcp.NewTool("get_contour_dag",
			mcp.WithDescription("Get Contour's computed routing DAG (directed acyclic graph, DOT format) from a Contour pod's debug server. This is the authoritative view of how Contour interpreted all HTTPProxy/Ingress config before programming Envoy — gold for debugging routing discrepancies."),
			mcp.WithString("ingress_class",
				mcp.Required(),
				mcp.Description("Contour ingress class (e.g. public, private, inter-dc)."),
			),
			mcp.WithString("pod",
				mcp.Description("Specific Contour pod name. Defaults to the first ready Contour pod of the ingress class."),
			),
		),
		r.handleContourDAG,
	)

	return nil
}

// ─── Envoy target resolution ───

// envoyTarget builds the Envoy admin target for a tool call from its
// envoy_url / ingress_class / pod / admin_port arguments. Resolution order:
// envoy_url (direct) > pod > ingress class (first ready pod) > server default ingress class.
// With none set, an empty target is returned and the client falls back to its
// default URL, if any.
func (r *Registry) envoyTarget(ctx context.Context, req mcp.CallToolRequest) (envoy.Target, error) {
	if url := reqString(req, "envoy_url"); url != "" {
		return envoy.Target{URL: url}, nil
	}

	ingressClass := reqString(req, "ingress_class")
	pod := reqString(req, "pod")
	if ingressClass == "" && pod == "" {
		ingressClass = r.defaultIngressClass
		if ingressClass == "" {
			return envoy.Target{}, nil
		}
	}
	if r.pods == nil {
		return envoy.Target{}, fmt.Errorf("ingress-class/pod targeting requires Kubernetes access")
	}

	if pod == "" {
		selector := envoySelector + "," + ingressClassLabel + "=" + ingressClass
		pods, err := r.pods.ListPods(ctx, r.ingressNS, selector)
		if err != nil {
			return envoy.Target{}, err
		}
		for _, p := range pods {
			if p.Ready {
				pod = p.Name
				break
			}
		}
		if pod == "" {
			return envoy.Target{}, fmt.Errorf("no ready Envoy pods for ingress class %q in namespace %s (check list_envoy_ingress_classes)", ingressClass, r.ingressNS)
		}
	} else if ingressClass == "" {
		// Derive the ingress class from the pod's labels so the admin port lookup works.
		pods, err := r.pods.ListPods(ctx, r.ingressNS, envoySelector)
		if err != nil {
			return envoy.Target{}, err
		}
		for _, p := range pods {
			if p.Name == pod {
				ingressClass = p.Labels[ingressClassLabel]
				break
			}
		}
	}

	port := req.GetInt("admin_port", 0)
	if port <= 0 {
		ports, err := r.contourClient.PortsForIngressClass(ctx, ingressClass)
		if err != nil {
			port = contour.DefaultEnvoyAdminPort
		} else {
			port = ports.AdminPort
		}
	}

	return envoy.Target{Namespace: r.ingressNS, Pod: pod, Port: port}, nil
}

// register is a helper that adds a tool and handler, incrementing the count.
func (r *Registry) register(s *server.MCPServer, name string, tool mcp.Tool, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	s.AddTool(tool, handler)
	r.toolCount++
}

// ─── Argument extraction helpers ───

// reqString extracts a string argument from a CallToolRequest using mcp-go's GetString helper.
func reqString(req mcp.CallToolRequest, key string) string {
	return req.GetString(key, "")
}

func jsonString(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("error marshaling JSON: %v", err)
	}
	return string(b)
}

func textResult(v interface{}) *mcp.CallToolResult {
	return mcp.NewToolResultText(jsonString(v))
}

func errorResult(format string, args ...interface{}) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...))
}

func textResultFromString(s string) *mcp.CallToolResult {
	return mcp.NewToolResultText(s)
}

// ─── Contour HTTPProxy Handlers ───

func (r *Registry) handleListHTTPProxies(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace := reqString(req, "namespace")

	proxies, err := r.contourClient.GetHTTPProxySummary(ctx, namespace)
	if err != nil {
		return errorResult("Failed to list HTTPProxies: %v", err), nil
	}

	if len(proxies) == 0 {
		msg := "No HTTPProxies found"
		if namespace != "" {
			msg = fmt.Sprintf("No HTTPProxies found in namespace '%s'", namespace)
		}
		return textResultFromString(msg), nil
	}

	return textResult(map[string]interface{}{
		"count":   len(proxies),
		"proxies": proxies,
	}), nil
}

func (r *Registry) handleGetHTTPProxy(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := reqString(req, "name")
	namespace := reqString(req, "namespace")

	if name == "" || namespace == "" {
		return errorResult("Both 'name' and 'namespace' are required"), nil
	}

	proxy, err := r.contourClient.GetHTTPProxy(ctx, name, namespace)
	if err != nil {
		return errorResult("Failed to get HTTPProxy %s/%s: %v", namespace, name, err), nil
	}

	return textResult(proxy), nil
}

func (r *Registry) handleGetHTTPProxyStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := reqString(req, "name")
	namespace := reqString(req, "namespace")

	if name == "" || namespace == "" {
		return errorResult("Both 'name' and 'namespace' are required"), nil
	}

	status, err := r.contourClient.GetHTTPProxyStatus(ctx, name, namespace)
	if err != nil {
		return errorResult("Failed to get HTTPProxy status: %v", err), nil
	}

	conditions, _ := r.contourClient.GetHTTPProxyConditions(ctx, name, namespace)

	return textResult(map[string]interface{}{
		"name":       name,
		"namespace":  namespace,
		"status":     status,
		"conditions": conditions,
	}), nil
}

func (r *Registry) handleGetHTTPProxyRoutes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := reqString(req, "name")
	namespace := reqString(req, "namespace")

	if name == "" || namespace == "" {
		return errorResult("Both 'name' and 'namespace' are required"), nil
	}

	routes, err := r.contourClient.GetHTTPProxyRoutes(ctx, name, namespace)
	if err != nil {
		return errorResult("Failed to get HTTPProxy routes: %v", err), nil
	}

	if len(routes) == 0 {
		return textResultFromString(fmt.Sprintf("HTTPProxy %s/%s has no routes defined", namespace, name)), nil
	}

	return textResult(map[string]interface{}{
		"name":      name,
		"namespace": namespace,
		"routes":    routes,
	}), nil
}

func (r *Registry) handleGetHTTPProxyTree(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := reqString(req, "name")
	namespace := reqString(req, "namespace")

	if name == "" || namespace == "" {
		return errorResult("Both 'name' and 'namespace' are required"), nil
	}

	tree, err := r.contourClient.GetHTTPProxyTree(ctx, name, namespace)
	if err != nil {
		return errorResult("Failed to get HTTPProxy tree: %v", err), nil
	}

	return textResult(tree), nil
}

func (r *Registry) handleSearchByFQDN(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fqdn := reqString(req, "fqdn")
	if fqdn == "" {
		return errorResult("'fqdn' is required"), nil
	}

	proxies, err := r.contourClient.ListHTTPProxiesByFQDN(ctx, fqdn)
	if err != nil {
		return errorResult("Failed to search HTTPProxies by FQDN: %v", err), nil
	}

	if len(proxies) == 0 {
		return textResultFromString(fmt.Sprintf("No HTTPProxies found matching FQDN '%s'", fqdn)), nil
	}

	return textResult(map[string]interface{}{
		"fqdn":    fqdn,
		"count":   len(proxies),
		"proxies": proxies,
	}), nil
}

func (r *Registry) handleSearchByBackend(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serviceName := reqString(req, "service_name")
	namespace := reqString(req, "namespace")

	if serviceName == "" {
		return errorResult("'service_name' is required"), nil
	}

	proxies, err := r.contourClient.ListHTTPProxiesByBackend(ctx, serviceName, namespace)
	if err != nil {
		return errorResult("Failed to search HTTPProxies by backend: %v", err), nil
	}

	if len(proxies) == 0 {
		msg := fmt.Sprintf("No HTTPProxies found routing to service '%s'", serviceName)
		if namespace != "" {
			msg = fmt.Sprintf("No HTTPProxies found routing to service '%s' in namespace '%s'", serviceName, namespace)
		}
		return textResultFromString(msg), nil
	}

	return textResult(map[string]interface{}{
		"service_name": serviceName,
		"namespace":    namespace,
		"count":        len(proxies),
		"proxies":      proxies,
	}), nil
}

func (r *Registry) handleListInvalidProxies(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	invalid, err := r.contourClient.ListInvalidHTTPProxies(ctx)
	if err != nil {
		return errorResult("Failed to list invalid HTTPProxies: %v", err), nil
	}

	if len(invalid) == 0 {
		return textResultFromString("All HTTPProxies have Valid status"), nil
	}

	summaries := make([]map[string]interface{}, 0, len(invalid))
	for _, p := range invalid {
		name, _, _ := extractString(p, "metadata", "name")
		ns, _, _ := extractString(p, "metadata", "namespace")
		status, _, _ := extractString(p, "status", "currentStatus")
		desc, _, _ := extractString(p, "status", "description")
		summaries = append(summaries, map[string]interface{}{
			"name":        name,
			"namespace":   ns,
			"status":      status,
			"description": desc,
		})
	}

	return textResult(map[string]interface{}{
		"count":           len(summaries),
		"invalid_proxies": summaries,
	}), nil
}

// ─── Contour TLS/Extension Handlers ───

func (r *Registry) handleListTLSCertDelegations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace := reqString(req, "namespace")
	delegations, err := r.contourClient.ListTLSCertDelegations(ctx, namespace)
	if err != nil {
		return errorResult("Failed to list TLSCertificateDelegations: %v", err), nil
	}

	if len(delegations) == 0 {
		return textResultFromString("No TLSCertificateDelegations found"), nil
	}

	return textResult(map[string]interface{}{
		"count":       len(delegations),
		"delegations": delegations,
	}), nil
}

func (r *Registry) handleListExtensionServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace := reqString(req, "namespace")
	services, err := r.contourClient.ListExtensionServices(ctx, namespace)
	if err != nil {
		return errorResult("Failed to list ExtensionServices: %v", err), nil
	}

	if len(services) == 0 {
		return textResultFromString("No ExtensionServices found"), nil
	}

	return textResult(map[string]interface{}{
		"count":    len(services),
		"services": services,
	}), nil
}

// ─── Envoy Admin API Handlers ───

func (r *Registry) handleEnvoyConfigDump(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	resourceType := reqString(req, "resource_type")

	if resourceType != "" {
		validTypes := map[string]bool{
			"listener": true, "route": true, "cluster": true,
			"endpoint": true, "secret": true, "scoped_route": true,
		}
		if !validTypes[resourceType] {
			return errorResult("Invalid resource_type '%s'. Valid types: listener, route, cluster, endpoint, secret, scoped_route", resourceType), nil
		}

		dump, err := r.envoyClient.GetConfigDumpFiltered(ctx, target, resourceType)
		if err != nil {
			return errorResult("Failed to get Envoy config dump: %v", err), nil
		}
		return textResult(dump), nil
	}

	dump, err := r.envoyClient.GetConfigDump(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy config dump: %v", err), nil
	}
	return textResult(dump), nil
}

func (r *Registry) handleEnvoyListeners(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	listeners, err := r.envoyClient.GetListeners(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy listeners: %v", err), nil
	}

	return textResult(map[string]interface{}{
		"count":     len(listeners),
		"listeners": listeners,
	}), nil
}

func (r *Registry) handleEnvoyRoutes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	routes, err := r.envoyClient.GetRoutes(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy routes: %v", err), nil
	}

	return textResult(map[string]interface{}{
		"count":  len(routes),
		"routes": routes,
	}), nil
}

func (r *Registry) handleEnvoyClusters(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	clusters, err := r.envoyClient.GetClusters(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy clusters: %v", err), nil
	}

	return textResult(map[string]interface{}{
		"count":    len(clusters),
		"clusters": clusters,
	}), nil
}

func (r *Registry) handleEnvoyEndpoints(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	endpoints, err := r.envoyClient.GetEndpoints(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy endpoints: %v", err), nil
	}

	return textResult(map[string]interface{}{
		"count":     len(endpoints),
		"endpoints": endpoints,
	}), nil
}

func (r *Registry) handleEnvoyStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	filter := reqString(req, "filter")
	format := reqString(req, "format")

	if filter != "" {
		stats, err := r.envoyClient.GetStatsFiltered(ctx, target, filter)
		if err != nil {
			return errorResult("Failed to get Envoy stats: %v", err), nil
		}
		return textResultFromString(stats), nil
	}

	if strings.EqualFold(format, "json") {
		stats, err := r.envoyClient.GetStatsAsJSON(ctx, target)
		if err != nil {
			return errorResult("Failed to get Envoy stats: %v", err), nil
		}
		return textResult(stats), nil
	}

	stats, err := r.envoyClient.GetStats(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy stats: %v", err), nil
	}
	return textResultFromString(stats), nil
}

func (r *Registry) handleEnvoyClustersHealth(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	health, err := r.envoyClient.GetClustersHealth(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy clusters health: %v", err), nil
	}
	return textResultFromString(health), nil
}

func (r *Registry) handleEnvoyServerInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	info, err := r.envoyClient.GetServerInfo(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy server info: %v", err), nil
	}
	return textResult(info), nil
}

func (r *Registry) handleEnvoyCerts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	certs, err := r.envoyClient.GetCerts(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy certs: %v", err), nil
	}
	return textResultFromString(certs), nil
}

func (r *Registry) handleEnvoyReady(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	status, err := r.envoyClient.GetReady(ctx, target)
	if err != nil {
		return textResult(map[string]interface{}{
			"ready": false,
			"error": err.Error(),
		}), nil
	}
	return textResult(map[string]interface{}{
		"ready":  true,
		"status": status,
	}), nil
}

func (r *Registry) handleEnvoyRuntime(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	runtime, err := r.envoyClient.GetRuntime(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy runtime: %v", err), nil
	}
	return textResult(runtime), nil
}

// extractString is a helper to extract nested strings from unstructured maps.
func extractString(m map[string]interface{}, keys ...string) (string, bool, error) {
	current := interface{}(m)
	for i, key := range keys {
		inner, ok := current.(map[string]interface{})
		if !ok {
			return "", false, nil
		}
		current = inner[key]
		if current == nil {
			return "", false, nil
		}
		if i == len(keys)-1 {
			if s, ok := current.(string); ok {
				return s, true, nil
			}
		}
	}
	return "", false, nil
}

// ─── IngressClass Discovery & Contour Debug Handlers ───

func (r *Registry) handleListEnvoyIngressClasses(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if r.pods == nil {
		return errorResult("IngressClass discovery requires Kubernetes access"), nil
	}
	pods, err := r.pods.ListPods(ctx, r.ingressNS, envoySelector)
	if err != nil {
		return errorResult("Failed to list Envoy pods: %v", err), nil
	}
	if len(pods) == 0 {
		return textResultFromString(fmt.Sprintf("No Envoy pods found in namespace '%s'", r.ingressNS)), nil
	}

	type ingressClassSummary struct {
		IngressClass string `json:"ingress_class"`
		Pods         int    `json:"pods"`
		ReadyPods    int    `json:"readyPods"`
		AdminPort    int    `json:"adminPort"`
	}
	byIngressClass := map[string]*ingressClassSummary{}
	for _, p := range pods {
		ingressClass := p.Labels[ingressClassLabel]
		if ingressClass == "" {
			ingressClass = "(unlabeled)"
		}
		fs, ok := byIngressClass[ingressClass]
		if !ok {
			fs = &ingressClassSummary{IngressClass: ingressClass}
			byIngressClass[ingressClass] = fs
		}
		fs.Pods++
		if p.Ready {
			fs.ReadyPods++
		}
	}

	ingressClasses := make([]ingressClassSummary, 0, len(byIngressClass))
	for ingressClass, fs := range byIngressClass {
		ports, err := r.contourClient.PortsForIngressClass(ctx, ingressClass)
		if err != nil {
			fs.AdminPort = contour.DefaultEnvoyAdminPort
		} else {
			fs.AdminPort = ports.AdminPort
		}
		ingressClasses = append(ingressClasses, *fs)
	}
	sort.Slice(ingressClasses, func(i, j int) bool { return ingressClasses[i].IngressClass < ingressClasses[j].IngressClass })

	return textResult(map[string]interface{}{
		"namespace":      r.ingressNS,
		"count":          len(ingressClasses),
		"ingressClasses": ingressClasses,
	}), nil
}

func (r *Registry) handleListEnvoyPods(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if r.pods == nil {
		return errorResult("Pod discovery requires Kubernetes access"), nil
	}
	selector := envoySelector
	ingressClass := reqString(req, "ingress_class")
	if ingressClass != "" {
		selector += "," + ingressClassLabel + "=" + ingressClass
	}
	pods, err := r.pods.ListPods(ctx, r.ingressNS, selector)
	if err != nil {
		return errorResult("Failed to list Envoy pods: %v", err), nil
	}
	return textResult(map[string]interface{}{
		"namespace": r.ingressNS,
		"count":     len(pods),
		"pods":      pods,
	}), nil
}

func (r *Registry) handleContourDAG(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ingressClass := reqString(req, "ingress_class")
	if ingressClass == "" {
		return errorResult("'ingress_class' is required"), nil
	}
	if r.pods == nil {
		return errorResult("Contour DAG access requires Kubernetes access"), nil
	}

	pod := reqString(req, "pod")
	if pod == "" {
		selector := contourSelector + "," + ingressClassLabel + "=" + ingressClass
		pods, err := r.pods.ListPods(ctx, r.ingressNS, selector)
		if err != nil {
			return errorResult("Failed to list Contour pods: %v", err), nil
		}
		for _, p := range pods {
			if p.Ready {
				pod = p.Name
				break
			}
		}
		if pod == "" {
			return errorResult("No ready Contour pods for ingress class '%s' in namespace %s", ingressClass, r.ingressNS), nil
		}
	}

	ports, err := r.contourClient.PortsForIngressClass(ctx, ingressClass)
	if err != nil {
		return errorResult("Failed to resolve Contour debug port: %v", err), nil
	}

	dag, err := r.contourClient.GetDAG(ctx, r.ingressNS, pod, ports.DebugPort)
	if err != nil {
		return errorResult("Failed to get Contour DAG from %s/%s: %v", r.ingressNS, pod, err), nil
	}
	return textResultFromString(dag), nil
}

func (r *Registry) handleEnvoyMemory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := r.envoyTarget(ctx, req)
	if err != nil {
		return errorResult("Failed to resolve Envoy target: %v", err), nil
	}
	memory, err := r.envoyClient.GetMemory(ctx, target)
	if err != nil {
		return errorResult("Failed to get Envoy memory: %v", err), nil
	}
	return textResult(memory), nil
}
