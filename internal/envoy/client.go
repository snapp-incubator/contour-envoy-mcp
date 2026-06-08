// Package envoy provides a client for querying the Envoy admin API
// (config_dump, stats, clusters, listeners, routes, certs, server_info).
package envoy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AdminClient communicates with the Envoy admin interface.
type AdminClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAdminClient creates a new Envoy admin API client.
// baseURL is the scheme+host+port of the Envoy admin endpoint (e.g. http://envoy.projectcontour:9001).
// If empty, each tool call must provide the URL explicitly.
func NewAdminClient(baseURL string) *AdminClient {
	return &AdminClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// resolveURL returns the admin API URL, preferring the per-call override.
func (c *AdminClient) resolveURL(override string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return c.baseURL
}

// GetConfigDump retrieves the full Envoy configuration dump.
func (c *AdminClient) GetConfigDump(ctx context.Context, urlOverride string) (map[string]interface{}, error) {
	url := c.resolveURL(urlOverride) + "/config_dump"
	return c.getJSON(ctx, url)
}

// GetConfigDumpFiltered retrieves a filtered config dump for a specific type.
// resourceTypes can be: "listener", "route", "cluster", "endpoint", "secret", "scoped_route".
func (c *AdminClient) GetConfigDumpFiltered(ctx context.Context, urlOverride, resourceType string) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/config_dump?resource_type=%s", c.resolveURL(urlOverride), resourceType)
	return c.getJSON(ctx, url)
}

// GetListeners returns Envoy listener configuration.
func (c *AdminClient) GetListeners(ctx context.Context, urlOverride string) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, urlOverride, "listener")
	if err != nil {
		return nil, err
	}
	listeners, _, _ := nestedSlice(dump, "configs")
	return listeners, nil
}

// GetRoutes returns Envoy route configuration.
func (c *AdminClient) GetRoutes(ctx context.Context, urlOverride string) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, urlOverride, "route")
	if err != nil {
		return nil, err
	}
	routes, _, _ := nestedSlice(dump, "configs")
	return routes, nil
}

// GetClusters returns Envoy cluster configuration.
func (c *AdminClient) GetClusters(ctx context.Context, urlOverride string) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, urlOverride, "cluster")
	if err != nil {
		return nil, err
	}
	clusters, _, _ := nestedSlice(dump, "configs")
	return clusters, nil
}

// GetEndpoints returns Envoy endpoint configuration.
func (c *AdminClient) GetEndpoints(ctx context.Context, urlOverride string) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, urlOverride, "endpoint")
	if err != nil {
		return nil, err
	}
	endpoints, _, _ := nestedSlice(dump, "configs")
	return endpoints, nil
}

// GetSecrets returns Envoy secret/TLS configuration.
func (c *AdminClient) GetSecrets(ctx context.Context, urlOverride string) ([]interface{}, error) {
	dump, err := c.GetConfigDumpFiltered(ctx, urlOverride, "secret")
	if err != nil {
		return nil, err
	}
	secrets, _, _ := nestedSlice(dump, "configs")
	return secrets, nil
}

// GetStats returns Envoy server statistics.
func (c *AdminClient) GetStats(ctx context.Context, urlOverride string) (string, error) {
	url := c.resolveURL(urlOverride) + "/stats"
	return c.getText(ctx, url)
}

// GetStatsFormat returns Envoy stats in a specific format (text, json, html, prometheus).
func (c *AdminClient) GetStatsFormat(ctx context.Context, urlOverride, format string) (string, error) {
	url := fmt.Sprintf("%s/stats?format=%s", c.resolveURL(urlOverride), format)
	return c.getText(ctx, url)
}

// GetStatsFiltered returns Envoy stats filtered by a pattern.
func (c *AdminClient) GetStatsFiltered(ctx context.Context, urlOverride, filter string) (string, error) {
	url := fmt.Sprintf("%s/stats?filter=%s", c.resolveURL(urlOverride), filter)
	return c.getText(ctx, url)
}

// GetClustersHealth returns cluster health information from the Envoy admin API.
func (c *AdminClient) GetClustersHealth(ctx context.Context, urlOverride string) (string, error) {
	url := c.resolveURL(urlOverride) + "/clusters"
	return c.getText(ctx, url)
}

// GetServerInfo returns Envoy server information.
func (c *AdminClient) GetServerInfo(ctx context.Context, urlOverride string) (map[string]interface{}, error) {
	url := c.resolveURL(urlOverride) + "/server_info"
	return c.getJSON(ctx, url)
}

// GetCerts returns TLS certificate information from Envoy.
func (c *AdminClient) GetCerts(ctx context.Context, urlOverride string) (string, error) {
	url := c.resolveURL(urlOverride) + "/certs"
	return c.getText(ctx, url)
}

// GetReady returns Envoy readiness status.
func (c *AdminClient) GetReady(ctx context.Context, urlOverride string) (string, error) {
	url := c.resolveURL(urlOverride) + "/ready"
	return c.getText(ctx, url)
}

// GetRuntime returns Envoy runtime configuration.
func (c *AdminClient) GetRuntime(ctx context.Context, urlOverride string) (map[string]interface{}, error) {
	url := c.resolveURL(urlOverride) + "/runtime"
	return c.getJSON(ctx, url)
}

// GetStatsAsJSON returns Envoy stats in JSON format.
func (c *AdminClient) GetStatsAsJSON(ctx context.Context, urlOverride string) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/stats?format=json", c.resolveURL(urlOverride))
	return c.getJSON(ctx, url)
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Envoy admin API returned status %d: %s", resp.StatusCode, string(body))
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
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Envoy admin API returned status %d: %s", resp.StatusCode, string(body))
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
