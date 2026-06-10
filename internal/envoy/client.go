// Package envoy provides a client for querying the Envoy admin API
// (config_dump, stats, clusters, listeners, routes, certs, server_info).
//
// In Contour deployments the Envoy admin interface is bound to a unix socket,
// with a read-only allowlist listener on 127.0.0.1:<adminPort> programmed by
// Contour inside each Envoy pod. That listener is not reachable over the pod
// network, so this client reaches it through a Kubernetes port-forward tunnel
// (see PodForwarder). A direct URL mode is kept for local debugging and for
// deployments that expose the admin endpoint on the network.
package envoy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PodForwarder opens a tunnel to a localhost-bound port inside a pod and
// returns a locally reachable base URL plus a close function.
type PodForwarder interface {
	ForwardPod(ctx context.Context, namespace, pod string, port int) (baseURL string, close func(), err error)
}

// Target identifies which Envoy admin endpoint a call should hit.
// Resolution order: URL (direct) > Pod (port-forward) > client default URL.
type Target struct {
	// URL is a direct admin base URL (e.g. http://127.0.0.1:9001). Wins when set.
	URL string
	// Namespace/Pod/Port select a pod whose localhost admin listener is
	// reached via port-forward.
	Namespace string
	Pod       string
	Port      int
}

// AdminClient communicates with the Envoy admin interface.
type AdminClient struct {
	baseURL    string
	forwarder  PodForwarder
	httpClient *http.Client
}

// NewAdminClient creates a new Envoy admin API client. baseURL is an optional
// default direct admin URL; forwarder enables pod targets and may be nil.
func NewAdminClient(baseURL string, forwarder PodForwarder) *AdminClient {
	return &AdminClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		forwarder: forwarder,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ErrNoTarget is returned when a call has no way to reach an Envoy admin API.
var ErrNoTarget = errors.New("no Envoy target: pass 'fleet' or 'pod' (cluster mode), 'envoy_url' (direct mode), or start the server with -envoy-admin-url")

func noop() {}

// resolve returns the base URL to use for a call and a cleanup function that
// must be called when the request is done (it closes the port-forward tunnel,
// if one was opened).
func (c *AdminClient) resolve(ctx context.Context, t Target) (string, func(), error) {
	if t.URL != "" {
		return strings.TrimRight(t.URL, "/"), noop, nil
	}
	if t.Pod != "" {
		if c.forwarder == nil {
			return "", nil, fmt.Errorf("pod target %s/%s requested but no Kubernetes port-forwarder is configured", t.Namespace, t.Pod)
		}
		return c.forwarder.ForwardPod(ctx, t.Namespace, t.Pod, t.Port)
	}
	if c.baseURL != "" {
		return c.baseURL, noop, nil
	}
	return "", nil, ErrNoTarget
}

func (c *AdminClient) targetJSON(ctx context.Context, t Target, path string) (map[string]interface{}, error) {
	base, done, err := c.resolve(ctx, t)
	if err != nil {
		return nil, err
	}
	defer done()
	return c.getJSON(ctx, base+path)
}

func (c *AdminClient) targetText(ctx context.Context, t Target, path string) (string, error) {
	base, done, err := c.resolve(ctx, t)
	if err != nil {
		return "", err
	}
	defer done()
	return c.getText(ctx, base+path)
}

// GetConfigDump retrieves the full Envoy configuration dump.
func (c *AdminClient) GetConfigDump(ctx context.Context, t Target) (map[string]interface{}, error) {
	return c.targetJSON(ctx, t, "/config_dump")
}

// GetConfigDumpFiltered retrieves a filtered config dump for a specific type.
// resourceTypes can be: "listener", "route", "cluster", "endpoint", "secret", "scoped_route".
func (c *AdminClient) GetConfigDumpFiltered(ctx context.Context, t Target, resourceType string) (map[string]interface{}, error) {
	return c.targetJSON(ctx, t, fmt.Sprintf("/config_dump?resource_type=%s", resourceType))
}

// GetListeners returns Envoy listener configuration.
func (c *AdminClient) GetListeners(ctx context.Context, t Target) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, t, "listener")
	if err != nil {
		return nil, err
	}
	listeners, _, _ := nestedSlice(dump, "configs")
	return listeners, nil
}

// GetRoutes returns Envoy route configuration.
func (c *AdminClient) GetRoutes(ctx context.Context, t Target) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, t, "route")
	if err != nil {
		return nil, err
	}
	routes, _, _ := nestedSlice(dump, "configs")
	return routes, nil
}

// GetClusters returns Envoy cluster configuration.
func (c *AdminClient) GetClusters(ctx context.Context, t Target) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, t, "cluster")
	if err != nil {
		return nil, err
	}
	clusters, _, _ := nestedSlice(dump, "configs")
	return clusters, nil
}

// GetEndpoints returns Envoy endpoint configuration.
func (c *AdminClient) GetEndpoints(ctx context.Context, t Target) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, t, "endpoint")
	if err != nil {
		return nil, err
	}
	endpoints, _, _ := nestedSlice(dump, "configs")
	return endpoints, nil
}

// GetSecrets returns Envoy secret/TLS configuration.
func (c *AdminClient) GetSecrets(ctx context.Context, t Target) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, t, "secret")
	if err != nil {
		return nil, err
	}
	secrets, _, _ := nestedSlice(dump, "configs")
	return secrets, nil
}

// GetStats returns Envoy server statistics.
func (c *AdminClient) GetStats(ctx context.Context, t Target) (string, error) {
	return c.targetText(ctx, t, "/stats")
}

// GetStatsFormat returns Envoy stats in a specific format (text, json, html, prometheus).
func (c *AdminClient) GetStatsFormat(ctx context.Context, t Target, format string) (string, error) {
	return c.targetText(ctx, t, fmt.Sprintf("/stats?format=%s", format))
}

// GetStatsFiltered returns Envoy stats filtered by a pattern.
func (c *AdminClient) GetStatsFiltered(ctx context.Context, t Target, filter string) (string, error) {
	return c.targetText(ctx, t, fmt.Sprintf("/stats?filter=%s", filter))
}

// GetClustersHealth returns cluster health information from the Envoy admin API.
func (c *AdminClient) GetClustersHealth(ctx context.Context, t Target) (string, error) {
	return c.targetText(ctx, t, "/clusters")
}

// GetServerInfo returns Envoy server information.
func (c *AdminClient) GetServerInfo(ctx context.Context, t Target) (map[string]interface{}, error) {
	return c.targetJSON(ctx, t, "/server_info")
}

// GetCerts returns TLS certificate information from Envoy.
func (c *AdminClient) GetCerts(ctx context.Context, t Target) (string, error) {
	return c.targetText(ctx, t, "/certs")
}

// GetReady returns Envoy readiness status.
func (c *AdminClient) GetReady(ctx context.Context, t Target) (string, error) {
	return c.targetText(ctx, t, "/ready")
}

// GetRuntime returns Envoy runtime configuration.
func (c *AdminClient) GetRuntime(ctx context.Context, t Target) (map[string]interface{}, error) {
	return c.targetJSON(ctx, t, "/runtime")
}

// GetStatsAsJSON returns Envoy stats in JSON format.
func (c *AdminClient) GetStatsAsJSON(ctx context.Context, t Target) (map[string]interface{}, error) {
	return c.targetJSON(ctx, t, "/stats?format=json")
}

// GetMemory returns Envoy memory allocation details.
func (c *AdminClient) GetMemory(ctx context.Context, t Target) (map[string]interface{}, error) {
	return c.targetJSON(ctx, t, "/memory")
}

// --- HTTP helpers ---

func (c *AdminClient) getJSON(ctx context.Context, url string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request to %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("envoy admin API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding JSON response: %w", err)
	}
	return result, nil
}

func (c *AdminClient) getText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("executing request to %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("envoy admin API returned status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

// nestedSlice extracts a nested slice from a map.
func nestedSlice(m map[string]interface{}, keys ...string) ([]interface{}, bool, error) {
	current := interface{}(m)
	for _, key := range keys {
		inner, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		current = inner[key]
	}
	if current == nil {
		return nil, false, nil
	}
	if slice, ok := current.([]interface{}); ok {
		return slice, true, nil
	}
	return nil, false, nil
}
