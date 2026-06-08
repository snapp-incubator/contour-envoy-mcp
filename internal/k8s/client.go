// Package k8s provides Kubernetes client initialization and shared configuration.
package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps Kubernetes client interfaces.
type Client struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	config        *rest.Config
}

// NewClient creates a new Kubernetes client from kubeconfig path and optional context.
func NewClient(kubeconfig, kubeContext string) (*Client, error) {
	config, err := buildConfig(kubeconfig, kubeContext)
	if err != nil {
		return nil, fmt.Errorf("building kube config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	return &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		config:        config,
	}, nil
}

// Clientset returns the typed Kubernetes clientset.
func (c *Client) Clientset() *kubernetes.Clientset {
	return c.clientset
}

// DynamicClient returns the dynamic Kubernetes client.
func (c *Client) DynamicClient() dynamic.Interface {
	return c.dynamicClient
}

// Config returns the REST config.
func (c *Client) Config() *rest.Config {
	return c.config
}

func buildConfig(kubeconfig, kubeContext string) (*rest.Config, error) {
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	// Try in-cluster config first if no kubeconfig is specified
	if kubeconfig == "" {
		if config, err := rest.InClusterConfig(); err == nil {
			return config, nil
		}
		// Fall back to default kubeconfig location
		if home, err := os.UserHomeDir(); err == nil {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfig

	configOverrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		configOverrides.CurrentContext = kubeContext
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	return clientConfig.ClientConfig()
}
