// Package contour provides a client for querying Contour CRDs
// (HTTPProxy, TLSCertificateDelegation, ExtensionService) in Kubernetes.
package contour

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// HTTPProxyGVR is the GroupVersionResource for Contour HTTPProxy.
var HTTPProxyGVR = schema.GroupVersionResource{
	Group:    "projectcontour.io",
	Version:  "v1",
	Resource: "httpproxies",
}

// TLSCertDelegationGVR is the GroupVersionResource for Contour TLSCertificateDelegation.
var TLSCertDelegationGVR = schema.GroupVersionResource{
	Group:    "projectcontour.io",
	Version:  "v1",
	Resource: "tlscertificatedelegations",
}

// ExtensionServiceGVR is the GroupVersionResource for Contour ExtensionService.
var ExtensionServiceGVR = schema.GroupVersionResource{
	Group:    "projectcontour.io",
	Version:  "v1",
	Resource: "extensionservices",
}

// ServiceAPIGVR is the GVR for Contour's ServiceAPI Gateway routes (if enabled).
var ServiceAPIGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

// Client provides methods to query Contour CRDs.
type Client struct {
	dynamicClient dynamic.Interface
	defaultNS     string
	forwarder     PodForwarder
}

// NewClient creates a new Contour client.
func NewClient(dynamicClient dynamic.Interface, defaultNamespace string) *Client {
	return &Client{
		dynamicClient:  dynamicClient,
		defaultNS:      defaultNamespace,
	}
}

// --- HTTPProxy operations ---

// ListHTTPProxies returns HTTPProxies in the given namespace (or all namespaces if empty).
func (c *Client) ListHTTPProxies(ctx context.Context, namespace string) ([]map[string]interface{}, error) {
	ns := namespace
	if ns == "" {
		ns = ""
	}

	list, err := c.dynamicClient.Resource(HTTPProxyGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing HTTPProxies: %w", err)
	}

	return extractItems(list.Items), nil
}

// GetHTTPProxy returns a single HTTPProxy by name and namespace.
func (c *Client) GetHTTPProxy(ctx context.Context, name, namespace string) (map[string]interface{}, error) {
	obj, err := c.dynamicClient.Resource(HTTPProxyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting HTTPProxy %s/%s: %w", namespace, name, err)
	}
	return obj.Object, nil
}

// GetHTTPProxyStatus returns the status of an HTTPProxy.
func (c *Client) GetHTTPProxyStatus(ctx context.Context, name, namespace string) (map[string]interface{}, error) {
	obj, err := c.GetHTTPProxy(ctx, name, namespace)
	if err != nil {
		return nil, err
	}
	status, _, _ := unstructured.NestedFieldCopy(obj, "status")
	if status == nil {
		return map[string]interface{}{"status": "unknown", "message": "No status reported"}, nil
	}
	return map[string]interface{}{
		"name":      name,
		"namespace": namespace,
		"status":    status,
	}, nil
}

// GetHTTPProxyConditions returns the conditions of an HTTPProxy.
func (c *Client) GetHTTPProxyConditions(ctx context.Context, name, namespace string) ([]map[string]interface{}, error) {
	obj, err := c.GetHTTPProxy(ctx, name, namespace)
	if err != nil {
		return nil, err
	}

	conditions, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	if conditions == nil {
		return nil, nil
	}

	result := make([]map[string]interface{}, 0, len(conditions))
	for _, c := range conditions {
		if m, ok := c.(map[string]interface{}); ok {
			result = append(result, m)
		}
	}
	return result, nil
}

// ListHTTPProxiesByFQDN returns HTTPProxies that match a given FQDN (virtual host).
func (c *Client) ListHTTPProxiesByFQDN(ctx context.Context, fqdn string) ([]map[string]interface{}, error) {
	all, err := c.ListHTTPProxies(ctx, "")
	if err != nil {
		return nil, err
	}

	var matched []map[string]interface{}
	for _, proxy := range all {
		vhosts, _, _ := unstructuredNestedStringSlice(proxy, "spec", "virtualhost", "fqdn")
		for _, vh := range vhosts {
			if strings.EqualFold(vh, fqdn) || matchFQDN(vh, fqdn) {
				matched = append(matched, proxy)
				break
			}
		}
		// Also check includes
		includes, _, _ := unstructuredNestedStringSlice(proxy, "spec", "includes", "name")
		_ = includes // includes don't have FQDN directly
	}
	return matched, nil
}

// ListHTTPProxiesByBackend returns HTTPProxies that route to a given backend service.
func (c *Client) ListHTTPProxiesByBackend(ctx context.Context, serviceName, namespace string) ([]map[string]interface{}, error) {
	all, err := c.ListHTTPProxies(ctx, "")
	if err != nil {
		return nil, err
	}

	var matched []map[string]interface{}
	for _, proxy := range all {
		if proxyHasBackend(proxy, serviceName, namespace) {
			matched = append(matched, proxy)
		}
	}
	return matched, nil
}

// GetHTTPProxyTree returns the full proxy tree (root + included proxies) for a given root proxy.
func (c *Client) GetHTTPProxyTree(ctx context.Context, name, namespace string) (map[string]interface{}, error) {
	root, err := c.GetHTTPProxy(ctx, name, namespace)
	if err != nil {
		return nil, err
	}

	tree := map[string]interface{}{
		"root":  root,
		"leaves": []map[string]interface{}{},
	}

	includes, _, _ := unstructured.NestedSlice(root, "spec", "includes")
	if includes != nil {
		leaves := make([]map[string]interface{}, 0)
		for _, inc := range includes {
			if incMap, ok := inc.(map[string]interface{}); ok {
				incName, _, _ := unstructured.NestedString(incMap, "name")
				incNS, _, _ := unstructured.NestedString(incMap, "namespace")
				if incNS == "" {
					incNS = namespace
				}
				if incName != "" {
					leaf, err := c.GetHTTPProxy(ctx, incName, incNS)
					if err != nil {
						leaves = append(leaves, map[string]interface{}{
							"error":    err.Error(),
							"name":     incName,
							"namespace": incNS,
						})
					} else {
						leaves = append(leaves, leaf)
					}
				}
			}
		}
		tree["leaves"] = leaves
	}

	return tree, nil
}

// --- TLSCertificateDelegation operations ---

// ListTLSCertDelegations returns all TLSCertificateDelegations in the given namespace.
func (c *Client) ListTLSCertDelegations(ctx context.Context, namespace string) ([]map[string]interface{}, error) {
	list, err := c.dynamicClient.Resource(TLSCertDelegationGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing TLSCertificateDelegations: %w", err)
	}
	return extractItems(list.Items), nil
}

// --- ExtensionService operations ---

// ListExtensionServices returns all ExtensionServices in the given namespace.
func (c *Client) ListExtensionServices(ctx context.Context, namespace string) ([]map[string]interface{}, error) {
	ns := namespace
	if ns == "" {
		ns = c.defaultNS
	}
	list, err := c.dynamicClient.Resource(ExtensionServiceGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing ExtensionServices: %w", err)
	}
	return extractItems(list.Items), nil
}

// --- Utility ---

// ListInvalidHTTPProxies returns HTTPProxies with non-Valid status.
func (c *Client) ListInvalidHTTPProxies(ctx context.Context) ([]map[string]interface{}, error) {
	all, err := c.ListHTTPProxies(ctx, "")
	if err != nil {
		return nil, err
	}

	var invalid []map[string]interface{}
	for _, proxy := range all {
		currentStatus, _, _ := unstructured.NestedString(proxy, "status", "currentStatus")
		if currentStatus != "Valid" && currentStatus != "" {
			invalid = append(invalid, proxy)
		}
	}
	return invalid, nil
}

// GetHTTPProxySummary returns a lightweight summary for listing.
func (c *Client) GetHTTPProxySummary(ctx context.Context, namespace string) ([]map[string]interface{}, error) {
	proxies, err := c.ListHTTPProxies(ctx, namespace)
	if err != nil {
		return nil, err
	}

	summaries := make([]map[string]interface{}, 0, len(proxies))
	for _, p := range proxies {
		name, _, _ := unstructured.NestedString(p, "metadata", "name")
		ns, _, _ := unstructured.NestedString(p, "metadata", "namespace")
		fqdn, _, _ := unstructured.NestedString(p, "spec", "virtualhost", "fqdn")
		status, _, _ := unstructured.NestedString(p, "status", "currentStatus")
		desc, _, _ := unstructured.NestedString(p, "status", "description")

		summary := map[string]interface{}{
			"name":      name,
			"namespace": ns,
			"fqdn":      fqdn,
			"status":    status,
		}
		if desc != "" {
			summary["description"] = desc
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// GetHTTPProxyRoutes returns the routes defined in an HTTPProxy.
func (c *Client) GetHTTPProxyRoutes(ctx context.Context, name, namespace string) ([]map[string]interface{}, error) {
	obj, err := c.GetHTTPProxy(ctx, name, namespace)
	if err != nil {
		return nil, err
	}

	routes, _, _ := unstructured.NestedSlice(obj, "spec", "routes")
	if routes == nil {
		return nil, nil
	}

	result := make([]map[string]interface{}, 0, len(routes))
	for _, r := range routes {
		if m, ok := r.(map[string]interface{}); ok {
			result = append(result, m)
		}
	}
	return result, nil
}

// extractItems converts unstructured items to plain maps.
func extractItems(items []unstructured.Unstructured) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(items))
	for i := range items {
		result = append(result, items[i].Object)
	}
	return result
}

// unstructuredNestedStringSlice is a helper for extracting string slices from unstructured data.
func unstructuredNestedStringSlice(obj map[string]interface{}, fields ...string) ([]string, bool, error) {
	val, found, err := unstructured.NestedFieldCopy(obj, fields...)
	if !found || err != nil {
		return nil, found, err
	}
	switch v := val.(type) {
	case string:
		return []string{v}, true, nil
	case []string:
		return v, true, nil
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result, true, nil
	default:
		return nil, false, fmt.Errorf("unexpected type %T", val)
	}
}

// matchFQDN checks if a wildcard pattern matches the given fqdn.
func matchFQDN(pattern, fqdn string) bool {
	if pattern == "" {
		return false
	}
	if !strings.HasPrefix(pattern, "*.") {
		return strings.EqualFold(pattern, fqdn)
	}
	suffix := pattern[1:] // remove the *
	return strings.HasSuffix(strings.ToLower(fqdn), strings.ToLower(suffix))
}

// proxyHasBackend checks if the proxy references a given backend service.
func proxyHasBackend(proxy map[string]interface{}, serviceName, namespace string) bool {
	// Check spec.routes[].services[]
	routes, _, _ := unstructured.NestedSlice(proxy, "spec", "routes")
	for _, r := range routes {
		route, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		services, _, _ := unstructured.NestedSlice(route, "services")
		for _, s := range services {
			svc, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			svcName, _, _ := unstructured.NestedString(svc, "name")
			svcNS, _, _ := unstructured.NestedString(svc, "namespace")
			if svcName == serviceName {
				if namespace == "" || svcNS == namespace || svcNS == "" {
					return true
				}
			}
		}
	}

	// Check spec.tcpproxy.service
	tcpService, _, _ := unstructured.NestedString(proxy, "spec", "tcpproxy", "service", "name")
	if tcpService == serviceName {
		return true
	}

	// Check includes (recurse into spec.includes)
	includes, _, _ := unstructured.NestedSlice(proxy, "spec", "includes")
	for _, inc := range includes {
		if incMap, ok := inc.(map[string]interface{}); ok {
			incName, _, _ := unstructured.NestedString(incMap, "name")
			if incName == serviceName {
				return true
			}
		}
	}

	return false
}

// ToJSON marshals a value to indented JSON string.
func ToJSON(v interface{}) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
