package contour

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ContourConfigurationGVR is the GVR for Contour's ContourConfiguration CRD,
// which holds per-ingress-class settings like the Envoy admin port and debug server.
var ContourConfigurationGVR = schema.GroupVersionResource{
	Group:    "projectcontour.io",
	Version:  "v1alpha1",
	Resource: "contourconfigurations",
}

const (
	// DefaultEnvoyAdminPort is Contour's default for envoy.network.adminPort:
	// the read-only admin allowlist listener bound to 127.0.0.1 in Envoy pods.
	DefaultEnvoyAdminPort = 9001
	// DefaultDebugPort is Contour's default debug server port (serves /debug/dag).
	DefaultDebugPort = 6060
)

// PodForwarder opens a tunnel to a localhost-bound port inside a pod.
type PodForwarder interface {
	ForwardPod(ctx context.Context, namespace, pod string, port int) (baseURL string, close func(), err error)
}

// SetForwarder enables pod port-forward access (Contour debug server).
func (c *Client) SetForwarder(f PodForwarder) {
	c.forwarder = f
}

// ListContourConfigurations returns all ContourConfiguration resources in the
// default (ingress) namespace.
func (c *Client) ListContourConfigurations(ctx context.Context) ([]map[string]interface{}, error) {
	list, err := c.dynamicClient.Resource(ContourConfigurationGVR).Namespace(c.defaultNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing ContourConfigurations in %s: %w", c.defaultNS, err)
	}
	return extractItems(list.Items), nil
}

// IngressClassPorts holds the per-ingress-class ports resolved from a ContourConfiguration.
type IngressClassPorts struct {
	AdminPort int `json:"adminPort"`
	DebugPort int `json:"debugPort"`
}

// PortsForIngressClass resolves the Envoy admin port and Contour debug port for a
// an ingress class ( e.g. "public", "private", "inter-dc") by reading the
// ContourConfiguration. Falls back to Contour defaults when the
// configuration cannot be found or does not set the fields.
func (c *Client) PortsForIngressClass(ctx context.Context, ingressClass string) (IngressClassPorts, error) {
	ports := IngressClassPorts{AdminPort: DefaultEnvoyAdminPort, DebugPort: DefaultDebugPort}

	configs, err := c.ListContourConfigurations(ctx)
	if err != nil {
		return ports, err
	}

	cfg := matchIngressClassConfig(configs, ingressClass)
	if cfg == nil {
		return ports, nil
	}

	if v, ok, _ := unstructured.NestedInt64(cfg, "spec", "envoy", "network", "adminPort"); ok && v > 0 {
		ports.AdminPort = int(v)
	}
	if v, ok, _ := unstructured.NestedInt64(cfg, "spec", "debug", "port"); ok && v > 0 {
		ports.DebugPort = int(v)
	}
	return ports, nil
}

// matchIngressClassConfig finds the ContourConfiguration belonging to an ingress class.
// Prefers the exact conventional name (contour-<ingress-class>-configuration) so that
// e.g. ingress class "private" does not match "contour-ode-private-configuration",
// then falls back to a word-boundary substring match.
func matchIngressClassConfig(configs []map[string]interface{}, ingressClass string) map[string]interface{} {
	exact := fmt.Sprintf("contour-%s-configuration", ingressClass)
	var loose map[string]interface{}
	for _, cfg := range configs {
		name, _, _ := unstructured.NestedString(cfg, "metadata", "name")
		if name == exact {
			return cfg
		}
		if loose == nil && strings.Contains("-"+name+"-", "-"+ingressClass+"-") {
			loose = cfg
		}
	}
	return loose
}

// GetDAG fetches Contour's computed routing DAG (DOT graph format) from the
// debug server of a Contour pod. The debug server is bound to 127.0.0.1
// inside the pod, so this goes through a port-forward tunnel.
func (c *Client) GetDAG(ctx context.Context, namespace, pod string, port int) (string, error) {
	if c.forwarder == nil {
		return "", fmt.Errorf("no Kubernetes port-forwarder configured")
	}
	base, done, err := c.forwarder.ForwardPod(ctx, namespace, pod, port)
	if err != nil {
		return "", err
	}
	defer done()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/debug/dag", nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching DAG from %s/%s:%d: %w", namespace, pod, port, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading DAG response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("contour debug server returned status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}
