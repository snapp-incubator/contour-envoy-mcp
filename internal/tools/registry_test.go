package tools

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestReqString(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params = mcp.CallToolParams{
		Arguments: map[string]any{
			"name":      "test-proxy",
			"namespace": "production",
			"count":     42,
		},
	}

	tests := []struct {
		key      string
		expected string
	}{
		{"name", "test-proxy"},
		{"namespace", "production"},
		{"nonexistent", ""},
	}

	for _, tc := range tests {
		result := reqString(req, tc.key)
		if result != tc.expected {
			t.Errorf("reqString(%q) = %q, want %q", tc.key, result, tc.expected)
		}
	}
}

func TestJsonString(t *testing.T) {
	result := jsonString(map[string]string{"key": "value"})
	expected := "{\n  \"key\": \"value\"\n}"
	if result != expected {
		t.Errorf("jsonString() = %q, want %q", result, expected)
	}
}

func TestExtractString(t *testing.T) {
	m := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test",
		},
	}

	val, found, err := extractString(m, "metadata", "name")
	if err != nil || !found || val != "test" {
		t.Errorf("extractString() = %q, found=%v, err=%v", val, found, err)
	}

	_, found, err = extractString(m, "metadata", "missing")
	if err != nil || found {
		t.Errorf("expected not found for missing key, got found=%v", found)
	}
}

func TestExtractStringNested(t *testing.T) {
	m := map[string]interface{}{
		"status": map[string]interface{}{
			"currentStatus": "invalid",
			"description":   "virtualhost already exists under different namespace",
		},
	}

	status, found, _ := extractString(m, "status", "currentStatus")
	if !found || status != "invalid" {
		t.Errorf("expected status='invalid', got found=%v status=%q", found, status)
	}

	desc, found, _ := extractString(m, "status", "description")
	if !found || desc != "virtualhost already exists under different namespace" {
		t.Errorf("unexpected description: %q", desc)
	}
}
