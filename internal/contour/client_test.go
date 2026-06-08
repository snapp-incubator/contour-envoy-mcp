package contour

import (
	"testing"
)

func TestMatchFQDN(t *testing.T) {
	tests := []struct {
		pattern string
		fqdn    string
		match   bool
	}{
		{"app.example.com", "app.example.com", true},
		{"app.example.com", "other.example.com", false},
		{"*.example.com", "app.example.com", true},
		{"*.example.com", "foo.bar.example.com", true},
		{"*.example.com", "example.com", false},
		{"", "anything.com", false},
	}

	for _, tc := range tests {
		result := matchFQDN(tc.pattern, tc.fqdn)
		if result != tc.match {
			t.Errorf("matchFQDN(%q, %q) = %v, want %v", tc.pattern, tc.fqdn, result, tc.match)
		}
	}
}

func TestExtractItems(t *testing.T) {
	items := extractItems(nil)
	if len(items) != 0 {
		t.Errorf("expected empty slice for nil input, got %d items", len(items))
	}
}

func TestToJSON(t *testing.T) {
	result, err := ToJSON(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	if result != "{\n  \"key\": \"value\"\n}" {
		t.Errorf("unexpected JSON output: %s", result)
	}
}

func TestProxyHasBackend(t *testing.T) {
	proxy := map[string]interface{}{
		"spec": map[string]interface{}{
			"routes": []interface{}{
				map[string]interface{}{
					"services": []interface{}{
						map[string]interface{}{
							"name":      "web-service",
							"namespace": "default",
						},
					},
				},
			},
		},
	}

	if !proxyHasBackend(proxy, "web-service", "default") {
		t.Error("expected proxy to have backend 'web-service' in 'default'")
	}

	if proxyHasBackend(proxy, "other-service", "default") {
		t.Error("expected proxy to NOT have backend 'other-service'")
	}

	// Empty namespace should match any namespace
	if !proxyHasBackend(proxy, "web-service", "") {
		t.Error("expected proxy to match with empty namespace filter")
	}
}

func TestProxyHasBackendTCPProxy(t *testing.T) {
	proxy := map[string]interface{}{
		"spec": map[string]interface{}{
			"tcpproxy": map[string]interface{}{
				"service": map[string]interface{}{
					"name": "tcp-backend",
				},
			},
		},
	}

	if !proxyHasBackend(proxy, "tcp-backend", "") {
		t.Error("expected proxy to have TCP proxy backend 'tcp-backend'")
	}
}

func TestProxyHasBackendIncludes(t *testing.T) {
	proxy := map[string]interface{}{
		"spec": map[string]interface{}{
			"includes": []interface{}{
				map[string]interface{}{
					"name": "included-proxy",
				},
			},
		},
	}

	if !proxyHasBackend(proxy, "included-proxy", "") {
		t.Error("expected proxy to match included proxy name")
	}
}
