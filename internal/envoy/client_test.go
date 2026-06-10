package envoy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminClient_GetServerInfo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server_info" {
			t.Errorf("expected /server_info, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"1.30.0","state":"LIVE","uptime":"3600s"}`))
	}))
	defer ts.Close()

	client := NewAdminClient(ts.URL, nil)
	info, err := client.GetServerInfo(context.Background(), Target{})
	if err != nil {
		t.Fatalf("GetServerInfo failed: %v", err)
	}

	if info["version"] != "1.30.0" {
		t.Errorf("expected version 1.30.0, got %v", info["version"])
	}
	if info["state"] != "LIVE" {
		t.Errorf("expected state LIVE, got %v", info["state"])
	}
}

func TestAdminClient_GetReady(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			t.Errorf("expected /ready, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("LIVE"))
	}))
	defer ts.Close()

	client := NewAdminClient(ts.URL, nil)
	status, err := client.GetReady(context.Background(), Target{})
	if err != nil {
		t.Fatalf("GetReady failed: %v", err)
	}
	if status != "LIVE" {
		t.Errorf("expected LIVE, got %s", status)
	}
}

func TestAdminClient_GetStats(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("cluster.test_cluster.membership_total: 3\ntotal_requests: 1500\n"))
	}))
	defer ts.Close()

	client := NewAdminClient(ts.URL, nil)
	stats, err := client.GetStats(context.Background(), Target{})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats == "" {
		t.Error("expected non-empty stats")
	}
}

func TestAdminClient_GetCerts(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("certificates: []"))
	}))
	defer ts.Close()

	client := NewAdminClient(ts.URL, nil)
	certs, err := client.GetCerts(context.Background(), Target{})
	if err != nil {
		t.Fatalf("GetCerts failed: %v", err)
	}
	if certs == "" {
		t.Error("expected non-empty certs")
	}
}

func TestAdminClient_GetClustersHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("test_cluster::default_priority::max_connections::1024"))
	}))
	defer ts.Close()

	client := NewAdminClient(ts.URL, nil)
	health, err := client.GetClustersHealth(context.Background(), Target{})
	if err != nil {
		t.Fatalf("GetClustersHealth failed: %v", err)
	}
	if health == "" {
		t.Error("expected non-empty health")
	}
}

func TestAdminClient_ConfigDump(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"configs": [], "version_info": "v1"}`))
	}))
	defer ts.Close()

	client := NewAdminClient(ts.URL, nil)
	dump, err := client.GetConfigDump(context.Background(), Target{})
	if err != nil {
		t.Fatalf("GetConfigDump failed: %v", err)
	}
	if dump["version_info"] != "v1" {
		t.Errorf("expected v1, got %v", dump["version_info"])
	}
}

func TestAdminClient_URLOverride(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"override"}`))
	}))
	defer ts.Close()

	client := NewAdminClient("", nil)
	info, err := client.GetServerInfo(context.Background(), Target{URL: ts.URL})
	if err != nil {
		t.Fatalf("GetServerInfo with override failed: %v", err)
	}
	if info["version"] != "override" {
		t.Errorf("expected 'override', got %v", info["version"])
	}
}

func TestAdminClient_StatsFiltered(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter := r.URL.Query().Get("filter")
		if filter != "cluster\\..*" {
			t.Errorf("expected filter 'cluster\\..*', got %s", filter)
		}
		_, _ = w.Write([]byte("cluster.test: 1"))
	}))
	defer ts.Close()

	client := NewAdminClient(ts.URL, nil)
	stats, err := client.GetStatsFiltered(context.Background(), Target{}, `cluster\..*`)
	if err != nil {
		t.Fatalf("GetStatsFiltered failed: %v", err)
	}
	if stats == "" {
		t.Error("expected non-empty filtered stats")
	}
}

func TestAdminClient_NoTarget(t *testing.T) {
	client := NewAdminClient("", nil)
	_, err := client.GetServerInfo(context.Background(), Target{})
	if err == nil {
		t.Error("expected error when no target is configured")
	}
}

func TestAdminClient_PodTargetWithoutForwarder(t *testing.T) {
	client := NewAdminClient("", nil)
	_, err := client.GetServerInfo(context.Background(), Target{Namespace: "ns", Pod: "envoy-abc", Port: 9001})
	if err == nil {
		t.Error("expected error for pod target without forwarder")
	}
}

func TestAdminClient_ErrorHandling(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer ts.Close()

	client := NewAdminClient(ts.URL, nil)
	_, err := client.GetServerInfo(context.Background(), Target{})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}
