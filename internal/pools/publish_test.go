package pools

import (
	"strings"
	"testing"

	"super-proxy-pool/internal/models"
)

func TestBuildPublishBundle(t *testing.T) {
	poolList := []models.ProxyPool{
		{
			ID:                 1,
			Name:               "demo",
			Protocol:           "http",
			ListenHost:         "0.0.0.0",
			ListenPort:         18080,
			Strategy:           "round_robin",
			Enabled:            true,
			AuthEnabled:        true,
			AuthUsername:       "user",
			AuthPasswordSecret: "pass",
		},
	}
	member := models.RuntimeNode{
		SourceType:     "manual",
		SourceNodeID:   10,
		DisplayName:    "node-a",
		Protocol:       "trojan",
		Server:         "demo.example.com",
		Port:           443,
		Enabled:        true,
		NormalizedJSON: `{"type":"trojan","server":"demo.example.com","port":443,"password":"secret"}`,
	}

	bundle, err := BuildPublishBundle(
		"secret-token",
		"127.0.0.1:19090",
		"127.0.0.1:19091",
		17891,
		"https://www.gstatic.com/generate_204",
		"debug",
		poolList,
		map[int64][]models.RuntimeNode{1: {member}},
		[]models.RuntimeNode{member},
	)
	if err != nil {
		t.Fatalf("BuildPublishBundle() error = %v", err)
	}

	prod := string(bundle.ProdConfig)
	probe := string(bundle.ProbeConfig)
	if !strings.Contains(prod, "listeners:") || !strings.Contains(prod, "pool-group-1") || !strings.Contains(prod, "round-robin") || !strings.Contains(prod, "log-level: debug") {
		t.Fatalf("unexpected prod config:\n%s", prod)
	}
	if !strings.Contains(prod, "username: user") || !strings.Contains(prod, "password: pass") {
		t.Fatalf("expected listener auth in prod config:\n%s", prod)
	}
	if !strings.Contains(probe, "mixed-port: 17891") ||
		!strings.Contains(probe, "GLOBAL") ||
		!strings.Contains(probe, "manual-10-node-a") ||
		!strings.Contains(probe, "SPEED_SLOT_1") ||
		!strings.Contains(probe, "speed-slot-1") ||
		!strings.Contains(probe, "port: 17892") ||
		!strings.Contains(probe, "log-level: debug") {
		t.Fatalf("unexpected probe config:\n%s", probe)
	}
}

func TestBuildPublishBundleRespectsFailoverToggleForLoadBalance(t *testing.T) {
	member := models.RuntimeNode{
		SourceType:     "manual",
		SourceNodeID:   10,
		DisplayName:    "node-a",
		Protocol:       "trojan",
		Server:         "demo.example.com",
		Port:           443,
		Enabled:        true,
		NormalizedJSON: `{"type":"trojan","server":"demo.example.com","port":443,"password":"secret"}`,
	}

	withFailover, err := BuildPublishBundle(
		"secret-token",
		"127.0.0.1:19090",
		"127.0.0.1:19091",
		17891,
		"https://www.gstatic.com/generate_204",
		"debug",
		[]models.ProxyPool{{
			ID:              1,
			Name:            "with-failover",
			Protocol:        "http",
			ListenHost:      "0.0.0.0",
			ListenPort:      18080,
			Strategy:        "round_robin",
			FailoverEnabled: true,
			Enabled:         true,
		}},
		map[int64][]models.RuntimeNode{1: {member}},
		[]models.RuntimeNode{member},
	)
	if err != nil {
		t.Fatalf("BuildPublishBundle(withFailover) error = %v", err)
	}

	withoutFailover, err := BuildPublishBundle(
		"secret-token",
		"127.0.0.1:19090",
		"127.0.0.1:19091",
		17891,
		"https://www.gstatic.com/generate_204",
		"debug",
		[]models.ProxyPool{{
			ID:              2,
			Name:            "without-failover",
			Protocol:        "http",
			ListenHost:      "0.0.0.0",
			ListenPort:      18081,
			Strategy:        "round_robin",
			FailoverEnabled: false,
			Enabled:         true,
		}},
		map[int64][]models.RuntimeNode{2: {member}},
		[]models.RuntimeNode{member},
	)
	if err != nil {
		t.Fatalf("BuildPublishBundle(withoutFailover) error = %v", err)
	}

	withConfig := string(withFailover.ProdConfig)
	withoutConfig := string(withoutFailover.ProdConfig)
	if !strings.Contains(withConfig, "url: https://www.gstatic.com/generate_204") || !strings.Contains(withConfig, "interval: 300") || !strings.Contains(withConfig, "lazy: true") {
		t.Fatalf("expected health-check fields when failover is enabled:\n%s", withConfig)
	}
	if strings.Contains(withoutConfig, "url: https://www.gstatic.com/generate_204") || strings.Contains(withoutConfig, "interval: 300") || strings.Contains(withoutConfig, "lazy: true") {
		t.Fatalf("did not expect health-check fields when failover is disabled:\n%s", withoutConfig)
	}
}
