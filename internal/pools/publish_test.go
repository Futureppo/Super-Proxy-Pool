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
			Strategy:           "round_robin",
			Enabled:            true,
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
	if !strings.Contains(probe, "mixed-port: 17891") || !strings.Contains(probe, "GLOBAL") || !strings.Contains(probe, "manual-10-node-a") || !strings.Contains(probe, "log-level: debug") {
		t.Fatalf("unexpected probe config:\n%s", probe)
	}
}
