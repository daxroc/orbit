package config

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Mode != ModeCluster {
		t.Errorf("expected default mode cluster, got %q", cfg.Mode)
	}
	if cfg.GRPCPort != 9090 {
		t.Errorf("expected grpc port 9090, got %d", cfg.GRPCPort)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("expected http port 8080, got %d", cfg.HTTPPort)
	}
	if cfg.TCPReceiverPortStart != 10000 {
		t.Errorf("expected tcp receiver port 10000, got %d", cfg.TCPReceiverPortStart)
	}
	if cfg.UDPReceiverPortStart != 11000 {
		t.Errorf("expected udp receiver port 11000, got %d", cfg.UDPReceiverPortStart)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected log level info, got %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected log format json, got %q", cfg.LogFormat)
	}
	if cfg.ScenariosConfigPath != "/etc/orbit/scenarios.yaml" {
		t.Errorf("expected scenarios path /etc/orbit/scenarios.yaml, got %q", cfg.ScenariosConfigPath)
	}
}

func TestValidate_MissingAuthToken(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PodName = "test-pod"
	cfg.AuthToken = ""

	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing auth token")
	}
}

func TestValidate_MissingPodName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AuthToken = "secret"
	cfg.PodName = ""

	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing pod name")
	}
}

func TestValidate_InvalidMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AuthToken = "secret"
	cfg.PodName = "test"
	cfg.Mode = "invalid"

	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestValidate_AllModes(t *testing.T) {
	modes := []Mode{ModeCluster, ModeSatellite, ModeStandalone}
	for _, m := range modes {
		cfg := DefaultConfig()
		cfg.AuthToken = "secret"
		cfg.PodName = "test"
		cfg.Mode = m

		if err := cfg.Validate(); err != nil {
			t.Errorf("mode %q should be valid, got error: %v", m, err)
		}
	}
}

func TestSatelliteTarget_ResolveTarget(t *testing.T) {
	tests := []struct {
		name     string
		sat      SatelliteTarget
		flowType string
		want     string
	}{
		{"http default port", SatelliteTarget{Host: "10.0.0.1"}, "http", "10.0.0.1:8080"},
		{"http custom port", SatelliteTarget{Host: "10.0.0.1", HTTPPort: 9999}, "http", "10.0.0.1:9999"},
		{"grpc default port", SatelliteTarget{Host: "10.0.0.1"}, "grpc", "10.0.0.1:9090"},
		{"grpc custom port", SatelliteTarget{Host: "10.0.0.1", GRPCPort: 5050}, "grpc", "10.0.0.1:5050"},
		{"tcp-stream default port", SatelliteTarget{Host: "10.0.0.1"}, "tcp-stream", "10.0.0.1:10000"},
		{"tcp-stream custom port", SatelliteTarget{Host: "10.0.0.1", TCPPort: 20000}, "tcp-stream", "10.0.0.1:20000"},
		{"connection-churn uses tcp port", SatelliteTarget{Host: "10.0.0.1"}, "connection-churn", "10.0.0.1:10000"},
		{"connection-churn custom tcp port", SatelliteTarget{Host: "10.0.0.1", TCPPort: 20000}, "connection-churn", "10.0.0.1:20000"},
		{"udp-stream default port", SatelliteTarget{Host: "10.0.0.1"}, "udp-stream", "10.0.0.1:11000"},
		{"udp-stream custom port", SatelliteTarget{Host: "10.0.0.1", UDPPort: 22000}, "udp-stream", "10.0.0.1:22000"},
		{"icmp returns host only", SatelliteTarget{Host: "10.0.0.1"}, "icmp", "10.0.0.1"},
		{"unknown type returns empty", SatelliteTarget{Host: "10.0.0.1"}, "unknown", ""},
		{"empty host returns empty for http", SatelliteTarget{Host: ""}, "http", ""},
		{"empty host returns empty for tcp", SatelliteTarget{Host: ""}, "tcp-stream", ""},
		{"empty host returns empty for icmp", SatelliteTarget{Host: ""}, "icmp", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.sat.ResolveTarget(tt.flowType)
			if got != tt.want {
				t.Errorf("ResolveTarget(%q) = %q, want %q", tt.flowType, got, tt.want)
			}
		})
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	os.Setenv("ORBIT_AUTH_TOKEN", "env-token")
	os.Setenv("ORBIT_POD_NAME", "env-pod")
	os.Setenv("ORBIT_NAMESPACE", "env-ns")
	os.Setenv("ORBIT_NODE_NAME", "env-node")
	defer func() {
		os.Unsetenv("ORBIT_AUTH_TOKEN")
		os.Unsetenv("ORBIT_POD_NAME")
		os.Unsetenv("ORBIT_NAMESPACE")
		os.Unsetenv("ORBIT_NODE_NAME")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.AuthToken != "env-token" {
		t.Errorf("expected auth token 'env-token', got %q", cfg.AuthToken)
	}
	if cfg.PodName != "env-pod" {
		t.Errorf("expected pod name 'env-pod', got %q", cfg.PodName)
	}
	if cfg.Namespace != "env-ns" {
		t.Errorf("expected namespace 'env-ns', got %q", cfg.Namespace)
	}
	if cfg.NodeName != "env-node" {
		t.Errorf("expected node name 'env-node', got %q", cfg.NodeName)
	}
}
