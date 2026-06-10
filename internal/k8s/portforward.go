package k8s

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// ForwardPod opens a port-forward tunnel to a port inside the given pod and
// returns a local base URL (http://127.0.0.1:<localport>) plus a close
// function that tears the tunnel down. This is the only way to reach the
// Envoy admin listener and the Contour debug server, which Contour binds to
// 127.0.0.1 inside the pod by design: port-forward connects inside the pod's
// network namespace, so localhost-bound ports are reachable.
func (c *Client) ForwardPod(ctx context.Context, namespace, pod string, port int) (string, func(), error) {
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(pod).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(c.config)
	if err != nil {
		return "", nil, fmt.Errorf("creating SPDY round tripper: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})

	// Local port 0 lets the kernel pick a free port; concurrent tool calls
	// each get their own tunnel.
	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", port)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return "", nil, fmt.Errorf("creating port forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()

	select {
	case <-readyCh:
	case err := <-errCh:
		return "", nil, fmt.Errorf("port-forward to %s/%s:%d: %w", namespace, pod, port, err)
	case <-ctx.Done():
		close(stopCh)
		return "", nil, fmt.Errorf("port-forward to %s/%s:%d: %w", namespace, pod, port, ctx.Err())
	}

	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		close(stopCh)
		return "", nil, fmt.Errorf("getting forwarded local port for %s/%s:%d: %w", namespace, pod, port, err)
	}

	return fmt.Sprintf("http://127.0.0.1:%d", ports[0].Local), func() { close(stopCh) }, nil
}
