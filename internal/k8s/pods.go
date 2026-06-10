package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodInfo is a compact view of a pod for tool output and pod selection.
type PodInfo struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Node      string            `json:"node,omitempty"`
	IP        string            `json:"ip,omitempty"`
	Phase     string            `json:"phase"`
	Ready     bool              `json:"ready"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// ListPods returns pods in a namespace matching a label selector.
func (c *Client) ListPods(ctx context.Context, namespace, labelSelector string) ([]PodInfo, error) {
	list, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, fmt.Errorf("listing pods in %s (selector %q): %w", namespace, labelSelector, err)
	}

	pods := make([]PodInfo, 0, len(list.Items))
	for i := range list.Items {
		pods = append(pods, toPodInfo(&list.Items[i]))
	}
	return pods, nil
}

func toPodInfo(p *corev1.Pod) PodInfo {
	ready := false
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			ready = true
			break
		}
	}
	return PodInfo{
		Name:      p.Name,
		Namespace: p.Namespace,
		Node:      p.Spec.NodeName,
		IP:        p.Status.PodIP,
		Phase:     string(p.Status.Phase),
		Ready:     ready,
		Labels:    p.Labels,
	}
}
